package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Accelerator-mzq/ainovel-cli/assets"
	"github.com/Accelerator-mzq/ainovel-cli/internal/agents/ctxpack"
	"github.com/Accelerator-mzq/ainovel-cli/internal/bootstrap"
	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
	"github.com/Accelerator-mzq/ainovel-cli/internal/host/persona"
	"github.com/Accelerator-mzq/ainovel-cli/internal/host/reminder"
	"github.com/Accelerator-mzq/ainovel-cli/internal/host/sim"
	"github.com/Accelerator-mzq/ainovel-cli/internal/rules"
	"github.com/Accelerator-mzq/ainovel-cli/internal/store"
	"github.com/Accelerator-mzq/ainovel-cli/internal/tools"
	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/agentcore/subagent"
)

// agentToRole 把 subagent name 归一为 ModelSet 认得的 role 名。
// architect_short / architect_long 都共用同一个 architect role 配置。
// 跟 host.agentRoleName 同义，因为 build 与 host 互不依赖故各持一份。
func agentToRole(name string) string {
	if strings.HasPrefix(name, "architect_") {
		return "architect"
	}
	// 竞稿写手 writer_<slug> 的 cost 归属到 writer role，否则会按 agent 全名当成独立 role 算错。
	if strings.HasPrefix(name, "writer_") {
		return "writer"
	}
	return name
}

// subagentMaxRetries 给所有 SubAgentConfig 与 Coordinator 统一的 LLM retry 上限。
// 退避策略：指数 1s/2s/4s/8s/16s（受 maxDelay 上限约束），优先服从 server Retry-After。
// 配合 ToolsAreIdempotent=true 让 stream-idle / 503 / 短暂网络抖动这类 retryable
// 错误能在 subagent 层就近重试，而不是把整个 subagent 抛回 coordinator 重派发。
// 项目铁律一保证写类工具走 checkpoint+digest 幂等，重试是安全的。
const subagentMaxRetries = 5

// UsageRecorder 是 BuildCoordinator 可选的用量回调；签名与 OnMessage 一致，
// 每条 agent 消息都会调一次，由 Host 层负责聚合。nil 表示不追踪。
type UsageRecorder func(agentName string, msg agentcore.AgentMessage)

// BuildCoordinator 组装 Coordinator Agent 及其 SubAgent。
// 返回 Agent、AskUserTool、WriterRestorePack 以及 Coordinator 的 ContextEngine
// 引用——Host 层 /model 切换时需要直接调 SetContextWindow + SetReserveTokens
// 联动新模型的窗口（writer/architect/editor 走 ContextManagerFactory 自动重建，
// 不需要 ref；只有常驻的 coordinator 需要）。
// Host 层通过 Agent.Subscribe 获取事件流,不再需要 emit 回调。
func BuildCoordinator(
	cfg bootstrap.Config,
	store *store.Store,
	models *bootstrap.ModelSet,
	bundle assets.Bundle,
	recordUsage UsageRecorder,
) (*agentcore.Agent, *tools.AskUserTool, *ctxpack.WriterRestorePack, *corecontext.ContextEngine) {
	// 共享工具
	rulesOpts := rules.DefaultOptions(bundle.RulesFS)
	contextTool := tools.NewContextTool(store, bundle.References, cfg.Style, rulesOpts)
	readChapter := tools.NewReadChapterTool(store)
	askUser := tools.NewAskUserTool()

	architectTools := []agentcore.Tool{
		contextTool,
		tools.NewSaveFoundationTool(store),
	}
	writerTools := []agentcore.Tool{
		contextTool,
		readChapter,
		tools.NewPlanChapterTool(store),
		tools.NewDraftChapterTool(store),
		tools.NewEditChapterTool(store),
		tools.NewCheckConsistencyTool(store),
		tools.NewCommitChapterTool(store).WithRules(rulesOpts),
	}
	editorTools := []agentcore.Tool{
		contextTool,
		readChapter,
		tools.NewSaveReviewTool(store),
		tools.NewSaveArcSummaryTool(store),
		tools.NewSaveVolumeSummaryTool(store),
	}

	// Provider failover 只记日志,不通知宿主
	reportFailover := func(ev bootstrap.FailoverEvent) {
		slog.Warn("provider 切换",
			"module", "agent",
			"role", ev.Role,
			"reason", ev.Reason,
			"from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel),
			"err", ev.Err,
		)
	}

	architectModel := models.ForRoleWithFailover("architect", reportFailover)
	writerModel := models.ForRoleWithFailover("writer", reportFailover)
	editorModel := models.ForRoleWithFailover("editor", reportFailover)
	coordinatorModel := models.ForRoleWithFailover("coordinator", reportFailover)

	// Coordinator 的 ContextManager 在 Agent 构造时一次性生成，按启动模型解析。
	// 运行中 /model 切换到更小窗口的模型时，建议用户显式配置 context_window 兜底。
	_, coordinatorModelName, _ := models.CurrentSelection("coordinator")
	coordinatorContextWindow, coordinatorSource := cfg.ResolveContextWindow(coordinatorModelName)
	// Writer 的 ContextManager 由工厂每次调用重建，窗口随模型 swap 动态跟随（见下方工厂）。
	_, writerModelName, _ := models.CurrentSelection("writer")
	writerContextWindow, writerSource := cfg.ResolveContextWindow(writerModelName)
	bootstrap.LogContextWindowChoice("coordinator", coordinatorModelName, coordinatorContextWindow, coordinatorSource)
	bootstrap.LogContextWindowChoice("writer", writerModelName, writerContextWindow, writerSource)

	// modelLookup 写入 session 时给每条 assistant 消息附 _meta:{provider,model}，
	// 让 replay 不再依赖"当前 ModelSet"来反推历史 cost，运行中切换模型也能精确算。
	modelLookup := func(agentName string) (string, string) {
		role := agentToRole(agentName)
		provider, name, _ := models.CurrentSelection(role)
		return provider, name
	}
	baseOnMsg := store.Sessions.SubAgentLogger(modelLookup)
	onMsg := func(agentName, task string, msg agentcore.AgentMessage) {
		baseOnMsg(agentName, task, msg)
		if recordUsage != nil {
			recordUsage(agentName, msg)
		}
	}
	baseCoordinatorLog := store.Sessions.CoordinatorLogger(modelLookup)
	coordinatorOnMessage := func(msg agentcore.AgentMessage) {
		baseCoordinatorLog(msg)
		if recordUsage != nil {
			recordUsage("coordinator", msg)
		}
	}

	architectStopGuardFactory := func(_, _ string) agentcore.StopGuard {
		return reminder.NewArchitectStopGuard(store)
	}
	architectShort := subagent.Config{
		Name:               "architect_short",
		Description:        "短篇规划师：为单卷、单冲突、高密度故事生成紧凑设定与扁平大纲",
		Model:              architectModel,
		SystemPrompt:       bundle.Prompts.ArchitectShort,
		Tools:              architectTools,
		MaxTurns:           15,
		MaxRetries:         subagentMaxRetries,
		ToolsAreIdempotent: true,
		OnMessage:          onMsg,
		StopAfterToolResult: func(toolName string, result json.RawMessage) bool {
			r := decodeSaveFoundationResult(toolName, result)
			return r.Type == "outline" && r.FoundationReady
		},
		StopGuardFactory: architectStopGuardFactory,
	}
	architectLong := subagent.Config{
		Name:               "architect_long",
		Description:        "长篇规划师：为连载型、可持续升级的故事生成分层设定与卷弧大纲",
		Model:              architectModel,
		SystemPrompt:       bundle.Prompts.ArchitectLong,
		Tools:              architectTools,
		MaxTurns:           20,
		MaxRetries:         subagentMaxRetries,
		ToolsAreIdempotent: true,
		OnMessage:          onMsg,
		StopAfterToolResult: func(toolName string, result json.RawMessage) bool {
			r := decodeSaveFoundationResult(toolName, result)
			switch r.Type {
			case "update_compass", "expand_arc", "complete_book":
				return true
			default:
				return false
			}
		},
		StopGuardFactory: architectStopGuardFactory,
	}

	writerPrompt := bundle.Prompts.Writer
	if style, ok := bundle.Styles[cfg.Style]; ok {
		writerPrompt += "\n\n" + style
	}

	restore := &ctxpack.WriterRestorePack{}
	restore.Refresh(store)

	writer := subagent.Config{
		Name:               "writer",
		Description:        "创作者：自主完成一章的构思、写作、自审和提交",
		Model:              writerModel,
		SystemPrompt:       writerPrompt,
		Tools:              writerTools,
		MaxTurns:           30,
		MaxRetries:         subagentMaxRetries,
		ToolsAreIdempotent: true,
		StopAfterTools:     []string{"commit_chapter"},
		OnMessage:          onMsg,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return reminder.NewWriterStopGuard(store)
		},
		ContextManagerFactory: func(model agentcore.ChatModel) agentcore.ContextManager {
			// 每次 subagent(writer) 调用都会重建，从当前 runModel 读取最新模型名。
			// /model 切换 writer 后下一章自动用新窗口。
			window, _ := cfg.ResolveContextWindow(bootstrap.ModelName(model))
			return newContextManager(contextManagerConfig{
				Model:            model,
				ContextWindow:    window,
				ReserveTokens:    bootstrap.CompactReserveTokens(window),
				KeepRecentTokens: 20000,
				Agent:            "writer",
				ToolMicrocompact: &corecontext.ToolResultMicrocompactConfig{
					IdleThreshold: 5 * time.Minute,
				},
				ExtraStrategies: []corecontext.Strategy{
					ctxpack.NewStoreSummaryCompact(ctxpack.StoreSummaryCompactConfig{
						Store:            store,
						KeepRecentTokens: 20000,
					}),
				},
				Summary: &corecontext.FullSummaryConfig{
					PostSummaryHooks:    []corecontext.PostSummaryHook{restore.Hook()},
					SystemPrompt:        ctxpack.WriterSummarySystemPrompt,
					SummaryPrompt:       ctxpack.WriterSummaryPrompt,
					UpdateSummaryPrompt: ctxpack.WriterUpdateSummaryPrompt,
					TurnPrefixPrompt:    ctxpack.WriterTurnPrefixPrompt,
				},
			})
		},
	}

	editor := subagent.Config{
		Name:               "editor",
		Description:        "审阅者：阅读原文，从结构和审美两个层面发现问题",
		Model:              editorModel,
		SystemPrompt:       bundle.Prompts.Editor,
		Tools:              editorTools,
		MaxTurns:           20,
		MaxRetries:         subagentMaxRetries,
		ToolsAreIdempotent: true,
		OnMessage:          onMsg,
		// 仅摘要类终态产物命中即停；save_review 不再硬停——StopAfterTool 退出会绕过
		// StopGuard（agentcore loop.go），若 save_review 硬停，"被派生成弧摘要却先复核"
		// 的 editor 会在 save_review 处被砍断、够不到 save_arc_summary。评审/摘要任务的
		// 收尾改由任务感知的 NewEditorStopGuard 把关。
		StopAfterToolResult: func(toolName string, _ json.RawMessage) bool {
			return toolName == "save_arc_summary" || toolName == "save_volume_summary"
		},
		StopGuardFactory: func(_, task string) agentcore.StopGuard {
			return reminder.NewEditorStopGuard(store, task)
		},
	}

	// ---- 多人格竞稿装配 ----
	// 人格文风信号 = 语料生成的人格画像与主画像的融合画像（生成期融合，运行期单信号）。
	// 人格画像必须先经 /simulate 从 ./simulate/personas/<作者名>/ 语料生成；
	// 缺任一画像则整体禁用竞稿（回退单 writer），不阻断启动——
	// 首次配置时用户需要能进 TUI 跑 /simulate（鸡生蛋）。host.go 用同一谓词跳过 SetContest。
	contestCfg := cfg.WritingContest.Normalize()
	var contestSubagents []subagent.Config
	if contestCfg.Enabled() {
		missing, merr := persona.MissingProfiles(store, contestCfg.Personas)
		switch {
		case merr != nil:
			slog.Warn("读取人格画像失败，竞稿已禁用", "module", "agent", "err", merr)
		case len(missing) > 0:
			// 一并列出已有画像名：配置作者名与目录名拼写错位时，对照两个列表即可定位
			available, _ := persona.AvailableProfiles(store)
			slog.Warn("人格画像缺失，竞稿已禁用：请把对应作者语料放入 ./simulate/personas/<作者名>/ 并运行 /simulate，重启生效",
				"module", "agent", "missing", strings.Join(missing, "、"), "available", strings.Join(available, "、"))
		default:
			fuse := func(ctx context.Context, base, pp *domain.SimulationProfile) (*domain.SimulationSynthesis, error) {
				return sim.FuseSynthesis(ctx, writerModel, bundle.Prompts.SimulationPersonaFuse, base, pp)
			}
			// EnsureFused 最多串行 N 次融合 LLM 调用（缓存命中则 0 次），
			// 加总超时避免冷启动让 host.New 挂起；失败由内部兜底人格画像，不阻断。
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			resolved, perr := persona.EnsureFused(ctx, store, contestCfg.Personas, fuse)
			if perr != nil {
				slog.Warn("融合画像异常，按已得结果继续", "module", "agent", "err", perr)
			}
			for _, p := range resolved {
				pSlug := p.Slug
				prof := p.Profile
				if p.Fallback {
					slog.Warn("融合画像生成失败，暂用人格画像原样（下次启动重试）", "module", "agent", "persona", p.Author)
				}
				// 专属 ContextTool：simulation_profile 即该写手的融合画像（运行期唯一文风信号）。
				// writer.md 既有"## 仿写画像"指导段语义直接适配，无需任何 prompt 拼接。
				personaContext := contextTool.WithProfileSource(func() (*domain.SimulationProfile, error) {
					return prof, nil
				})
				personaTools := []agentcore.Tool{
					personaContext,
					readChapter,
					tools.NewPlanChapterTool(store),
					tools.NewDraftPersonaTool(store, pSlug),
					tools.NewCheckConsistencyTool(store),
					tools.NewCommitChapterTool(store).WithRules(rulesOpts),
				}
				contestSubagents = append(contestSubagents, subagent.Config{
					Name:               "writer_" + pSlug,
					Description:        fmt.Sprintf("竞稿写手（人格：%s）", p.Author),
					Model:              writerModel,
					SystemPrompt:       writerPrompt,
					Tools:              personaTools,
					MaxTurns:           30,
					MaxRetries:         subagentMaxRetries,
					ToolsAreIdempotent: true,
					OnMessage:          onMsg,
					// 注意：不设 StopAfterTools。
					// 候选阶段需要在 draft_persona 后停止（由 CandidateStopGuard 保证）；
					// 润色阶段需要推进到 commit_chapter（由 WriterStopGuard 保证）。
					// 若固定 StopAfterTools:commit_chapter，候选阶段则无法在 draft_persona 干净停止。
					// StopGuardFactory 通过检测 task 文本中是否含"润色"来切换两阶段语义。
					StopGuardFactory: func(_, task string) agentcore.StopGuard {
						// task 含"润色" → 中选 writer 走润色+提交，要求 commit。
						// 否则为候选阶段 → 要求写候选草稿。
						if strings.Contains(task, "润色") {
							return reminder.NewWriterStopGuard(store)
						}
						return reminder.NewCandidateStopGuard(store)
					},
				})
			}

			// Judge：固定复用 editor 模型。
			// ModelSet 没有 ForRef 入口，自定义 judge 模型属后续增强，暂不实现（YAGNI）。
			// 仅在确有 >=2 份候选 persona 时才注册 judge。
			if len(resolved) >= 2 {
				if contestCfg.Judge != nil {
					slog.Warn("writing_contest.judge 模型配置暂不生效，当前复用 editor 模型", "module", "agent")
				}
				// judge 读候选稿需要 persona 的 slug 列表，从已解析的 resolved 提取。
				judgeSlugs := make([]string, 0, len(resolved))
				for _, p := range resolved {
					judgeSlugs = append(judgeSlugs, p.Slug)
				}
				contestSubagents = append(contestSubagents, subagent.Config{
					Name:         "judge",
					Description:  "选优裁判：对比多份候选稿，选优并给修改意见",
					Model:        editorModel,
					SystemPrompt: bundle.Prompts.Judge,
					// read_candidates 一次性读本章所有候选稿；readChapter 保留供 judge 读已提交终稿做连贯性参考。
					Tools:              []agentcore.Tool{contextTool, readChapter, tools.NewReadCandidatesTool(store, judgeSlugs), tools.NewSaveVerdictTool(store)},
					MaxTurns:           15,
					MaxRetries:         subagentMaxRetries,
					ToolsAreIdempotent: true,
					OnMessage:          onMsg,
					StopAfterTools:     []string{"save_verdict"},
					StopGuardFactory: func(_, _ string) agentcore.StopGuard {
						return reminder.NewJudgeStopGuard(store)
					},
				})
			}
		}
	}

	allSubagents := []subagent.Config{architectShort, architectLong, writer, editor}
	allSubagents = append(allSubagents, contestSubagents...)
	subagentTool := subagent.New(allSubagents...)

	coordinatorEngine := newContextManager(contextManagerConfig{
		Model:            coordinatorModel,
		ContextWindow:    coordinatorContextWindow,
		ReserveTokens:    bootstrap.CompactReserveTokens(coordinatorContextWindow),
		KeepRecentTokens: 30000,
		Agent:            "coordinator",
		CommitOnProject:  true,
	})

	agent := agentcore.NewAgent(
		agentcore.WithModel(coordinatorModel),
		agentcore.WithSystemPrompt(bundle.Prompts.Coordinator),
		agentcore.WithTools(subagentTool, contextTool),
		agentcore.WithMaxTurns(100_000),
		agentcore.WithOnMessage(coordinatorOnMessage),
		agentcore.WithToolsAreIdempotent(true),
		// subagent 是流程主通道；真实错误应显式返回给 Host，而不是在单次 run 内永久禁用工具。
		agentcore.WithMaxToolErrors(0),
		agentcore.WithMaxRetries(subagentMaxRetries),
		agentcore.WithContextManager(coordinatorEngine),
		agentcore.WithStopGuard(reminder.NewStopGuard(store, nil)),
		// phase=complete 时硬拦截 subagent 派发，防止 Writer 死循环。
		agentcore.WithToolGate(completePhaseGate(store)),
	)
	return agent, askUser, restore, coordinatorEngine
}

// completePhaseGate 返回一个 ToolGate：phase=complete 时拒绝所有 subagent 派发。
// 防止 Coordinator LLM 在书完成后仍调用 Writer/Architect 导致死循环。
func completePhaseGate(st *store.Store) agentcore.ToolGate {
	return func(_ context.Context, req agentcore.GateRequest) (*agentcore.GateDecision, error) {
		if req.Call.Name != "subagent" {
			return nil, nil
		}
		// fail-open：Load 出错或 progress 为空时一律放行，不因瞬时读错误卡死正常派发。
		// 唯一代价是 complete 期恰逢读失败时死锁可能复现（概率极低，可接受）。
		progress, _ := st.Progress.Load()
		if progress != nil && progress.Phase == domain.PhaseComplete {
			return &agentcore.GateDecision{
				Allowed: false,
				Reason:  "全书已完成（phase=complete），无法再调用子代理。请告知用户全书已完结，不支持重写或续写。",
			}, nil
		}
		return nil, nil
	}
}

type saveFoundationResult struct {
	Type            string `json:"type"`
	FoundationReady bool   `json:"foundation_ready"`
}

func decodeSaveFoundationResult(toolName string, result json.RawMessage) saveFoundationResult {
	if toolName != "save_foundation" {
		return saveFoundationResult{}
	}
	var r saveFoundationResult
	_ = json.Unmarshal(result, &r)
	return r
}
