# 多人格竞稿写作 — 设计文档

- 日期：2026-06-02
- 状态：设计已确认，待实现
- 作者：msc + Claude

## 1. 背景与目标

当前写作流程是 Coordinator 顺序驱动**单个** Writer 逐章创作（`plan_chapter → draft_chapter → check_consistency → commit_chapter`），由 `flow.Route()` 状态机查表决定下一步、`coordinator.FollowUp()` 注入指令、`reminder.StopGuard` 约束工具调用顺序。

本功能新增**多人格竞稿**能力：

1. **多 Writer 竞稿** — 同一章由 N 个带不同作者人格的 Writer 各写一稿。
2. **评审选优** — 新增 Judge 子代理对 N 稿打分选优并给出修改意见。
3. **中选润色提交** — 中选 Writer 在自己被选中的草稿上按 Judge 意见继续修改，再提交。
4. **人格化** — 每个 Writer 绑定一个知名网文作者人格（如乌贼、卖报小郎君、土豆），文风由 LLM 依据作者名自动生成。

## 2. 已确认的核心决策

| # | 决策点 | 结论 |
|---|--------|------|
| 1 | 触发范围 | **全程每章竞稿**（质量优先，成本接受） |
| 2 | 人格 ↔ Writer | **一一对应**，N 个人格 = N 个 Writer，数量可动态配置 |
| 3 | 开关 | **配置 opt-in**；不配置 `personas` → 退回原单 Writer 行为，零成本、完全向后兼容 |
| 4 | 人格来源 | **配置只填作者名** → 启动时 LLM 生成文风 prompt → 缓存到 store，全书复用 |
| 5 | 选优流程 | **新增 Judge** 打分选优 + 给意见 → 中选 Writer 润色 → 走原 check → commit |
| 6 | 执行模型 | **先串行、预留并发接口**（agentcore 原生支持 parallel，将来无痛切换） |
| 7 | 编排层 | **Host 编排器**：把"写第 N 章"拆成 `flow.Route` 状态机的多个子步骤，与现有"弧末 editor 三步走"同构 |

## 3. 关键架构事实（实现依据）

- **写作驱动闭环**：Coordinator 调 `subagent(writer, "写第N章")` → subagent 工具成功 emit `EventToolExecEnd(Tool=="subagent")` → `flow/dispatcher.go` 监听 → `flow.Route(LoadState(store))` 读 store 事实算下一步 → `coordinator.FollowUp(指令文本)` → Coordinator 执行下一个 subagent 调用。Coordinator 始终是 subagent 唯一执行者，Host 只注入"下一步调谁"的文字指令（"LLM 驱动，Host 服务"）。
- **agentcore subagent 执行模式**（`subagent/subagent.go`）：支持 single / parallel / chain / background / team。`executeParallel`（:562）用 goroutine + WaitGroup + 信号量，`maxParallelTasks=8`、`maxConcurrency=4`，LLM 传 `tasks` 数组即触发。→ 串行用多次 single，并发换 parallel，底层现成。
- **草稿存储**（`store/drafts.go`）：当前草稿固定写 `drafts/{ch}.draft.md`；`commit_chapter` 从该固定路径 `LoadDraft` 读稿提交（commit_chapter.go:189）。→ 多 Writer 各写一稿必然互相覆盖，**候选稿隔离存储是必须新增的核心机制**。
- **工具并发安全**：`draft_chapter.ConcurrencySafe()` 返回 `false`（draft_chapter.go:33），写工具禁止并发写同一 store。→ 串行期无碍；并发期靠候选槽路径隔离 + 各 persona 独立工具实例规避竞态。
- **`subagent.Config.Tools` 每 agent 独立**：可为每个 persona writer 注入绑定了 persona id 的专属工具实例。

## 4. 总体设计

竞稿**不绕过 Coordinator**。把"写第 N 章"从单步扩展为 Route 状态机的多子步骤，复用断点恢复 / 事件投影 / 工具链全套现有机制。

### 4.1 向后兼容（最高优先级约束）

配置不写 `writing_contest.personas` 时，`Route` 在"写 next_chapter"分支返回原来的单 `writer` 指令，行为与现状逐字节一致。竞稿是纯 opt-in，默认零成本。

### 4.2 五层改动

#### 层 1：配置层（`bootstrap/config.go`）

新增可选字段：

```jsonc
"writing_contest": {
  "personas": ["乌贼", "卖报小郎君", "土豆"],   // 只填作者名；数量 = 并行 writer 数
  "judge": { "provider": "...", "model": "..." } // 可选；缺省复用 editor 角色模型
}
```

- `personas` 为空或字段缺失 → 单 Writer 模式（现状）。
- 人格数即 Writer 数（决策 2）。
- 解析后归一化：去空白、去重、保序；非法（全空）按未配置处理。

#### 层 2：人格生成层（新增 `host/persona` 包）

- 启动时（`host.New` / 首次 `Start` 后）对每个作者名调一次 LLM，生成结构化文风 prompt：句式特征、节奏、用词偏好、擅长题材、标志性手法。
- 结果缓存到 store 的 `personas.json`（persona id → 文风 block + 源作者名 + 生成模型 + 时间）。
- 全书只生成一次；`Resume` 时直接读缓存，不重生成，保证全书文风稳定。
- persona id 取作者名的稳定 slug（如 `wuzei`），用于工具路径与 agent 命名。
- 生成失败处理：单个失败 → 用一段通用"模仿 <作者名> 风格"的兜底 block 并告警，不阻塞启动。

#### 层 3：Agent 构建层（`agents/build.go`）

- 现有 1 个 `writer` config → 扩展为 N 个 `writer_<persona>` config：
  - SystemPrompt = 基础 writer prompt + 该人格文风 block。
  - **绑定专属工具实例**：该 persona 的 `draft_chapter` 写入隔离候选槽 `drafts/{ch}.cand-<persona>.md`（其余只读工具可共享）。
  - 复用现有 WriterStopGuard / ContextManagerFactory 机制。
- 新增 `judge` subagent config：
  - 工具：`novel_context`、`read_chapter`（读各候选稿）、新增 `save_verdict`。
  - 模型：`writing_contest.judge` 指定，缺省复用 editor 角色。
  - StopGuard：要求本轮至少产生一次 `save_verdict`。

#### 层 4：候选稿隔离存储（`store/drafts.go`）

- 候选槽：`drafts/{ch}.cand-<persona>.md`（每 persona 一份）。
- 裁定文件：`drafts/{ch}.verdict.json`，结构：
  ```json
  {
    "chapter": 12,
    "winner": "wuzei",
    "scores": [ {"persona":"wuzei","score":8.5,"comment":"..."}, ... ],
    "revision_notes": "给中选 writer 的具体修改意见"
  }
  ```
- **中选稿提升**：Judge 裁定后，Host 把 `cand-<winner>.md` 复制为标准 `{ch}.draft.md`。此后润色/提交完全复用现有单 Writer 工具链（`draft_chapter` append/rewrite → check → commit 均读写 `{ch}.draft.md`，零改动）。
- 新增方法：`SaveCandidate(ch, persona, content)`、`LoadCandidate(ch, persona)`、`ListCandidates(ch)`、`SaveVerdict` / `LoadVerdict`、`PromoteCandidate(ch, persona)`。

#### 层 5：Route 状态机（`flow/router.go`）

竞稿章节的"写第 N 章"子状态机（互斥，自上而下匹配第一个）：

| 事实判定（读 store） | 下一步指令 |
|---|---|
| 候选槽不齐（某 persona 缺 `cand-*.md`） | `writer_<persona[k]>` 写候选 k（串行逐个） |
| 候选齐、无 `verdict.json` | `judge` 选优 + 给修改意见 |
| 有 verdict、`draft.md` 未提升 | Host 提升中选稿为 `draft.md`（store 操作，非 subagent）——见下方触发说明 |
| 已提升、未润色完成 | 中选 `writer_<winner>` 在 `draft.md` 上按 `revision_notes` 润色 |
| 润色完成 | 走原 `check_consistency` → `commit_chapter` |

- 单 Writer 模式：跳过全部上述分支，直接返回原 `writer` 指令。
- `LoadState` 扩展：读候选槽完成情况、verdict 是否存在、draft 是否已提升。
- `Route` 保持纯函数；所有事实经 `State` 显式传入，可单测。

**"提升中选稿"步骤的触发**：该步是纯 store 操作，不调 subagent，因而不会自然产生 `EventToolExecEnd` 来驱动下一次 `Dispatch`。处理方式：`judge` 的 `save_verdict` 成功返回（这本身触发 `ToolExecEnd`）后，在 `dispatcher.Dispatch` 计算路由前，由 Host 检测"有 verdict 且未提升"并**同步执行提升**，再继续算路由直接得出"润色"指令。即提升与紧随其后的润色指令在同一次 Dispatch 内完成，不占用独立的状态机往返。恢复场景下若崩溃在提升前，`Route` 仍能依据 verdict 存在 + draft 未提升的事实重做提升。

### 4.3 串行 → 并发预留接口

把"候选稿生成"抽象为 `CandidateStrategy` 接口：

```go
type CandidateStrategy interface {
    // 给定缺失的候选 persona 列表，返回 Route 应下达的指令
    NextCandidateInstruction(chapter int, pending []string) *Instruction
}
```

- **串行实现（本期）**：返回单个 `writer_<persona>` 指令，逐个补齐。
- **并发实现（将来）**：返回一条 `subagent(tasks=[...])` parallel 指令，命中 agentcore `executeParallel`。

切换只替换策略实现，Route / store / checkpoint / 恢复逻辑均不动。

## 5. 数据流（竞稿一章，串行）

```
Route: 候选槽空 → writer_乌贼 写候选
  → cand-wuzei.md 落盘 → ToolExecEnd → Dispatch
Route: 缺卖报 → writer_卖报 写候选
  → cand-maibao.md 落盘 → Dispatch
Route: 缺土豆 → writer_土豆 写候选
  → cand-tudou.md 落盘 → Dispatch
Route: 候选齐、无 verdict → judge 选优
  → verdict.json(winner=wuzei + notes) 落盘 → Dispatch
Route: 有 verdict、未提升 → Host 提升 cand-wuzei → draft.md
  → Dispatch
Route: 已提升、未润色 → writer_乌贼 按 notes 润色 draft.md
  → draft.md 更新 → Dispatch
Route: 润色完成 → check_consistency → commit_chapter
  → chapters/N.md 终稿 + Progress 完成 → Dispatch → 下一章
```

每个 subagent 成功落 checkpoint；崩溃后 `Route` 读 store 事实精确恢复到中断子步骤。

## 6. 错误处理

- **persona writer 失败**：对应候选槽缺失，Route 自动重派（复用 dispatcher 去重 + subagent MaxRetries=5）；同一 persona 连续失败超限 → 标记该 persona 本章弃权，候选数减一继续；全部失败 → 本章降级为单 Writer（用基础 writer 直接写 `draft.md`）并告警。
- **judge 失败**：重试；超限则默认选取字数最多（或首个）候选作为 winner、`revision_notes` 置空并告警，保证流程推进。
- **中选润色失败**：`draft.md` 已是中选原稿，可直接进入 check/commit（润色为增强项，失败不阻塞提交）。
- **配置非法**：personas 全空白 → 按未配置处理；judge 模型解析失败 → 回退 editor 模型。

## 7. 测试计划

| 测试 | 重点 |
|---|---|
| `flow.Route` 竞稿子状态机单测 | 候选不齐/齐/有 verdict/已提升/润色完成 各分支；单 Writer 模式回归 |
| 候选槽存储读写测试 | SaveCandidate/LoadCandidate/ListCandidates/Verdict/Promote |
| persona 生成缓存测试 | 首次生成→缓存→Resume 读缓存不重生成；生成失败兜底 |
| 恢复测试 | 竞稿中途（候选写一半 / judge 前 / 润色前）崩溃后精确恢复 |
| 向后兼容回归 | 无 personas 配置时 Route 输出与现状一致 |

## 8. 不做（YAGNI）

- 本期不做真并发执行（仅预留接口）。
- 不做关键章/普通章差异化触发（决策为全程竞稿）。
- 不做动态人格抽取（人格整本书固定）。
- 不做人格库内置模板（人格全部由作者名 LLM 生成）。
- 不做 TUI 内人格编辑器（配置文件管理即可）。

## 9. 受影响文件清单

- 新增：`internal/host/persona/`（人格生成 + 缓存）、`internal/tools/save_verdict.go`
- 修改：`internal/bootstrap/config.go`（配置字段）、`internal/agents/build.go`（N persona writer + judge）、`internal/store/drafts.go`（候选槽 + verdict + promote）、`internal/host/flow/router.go` + `state.go`（竞稿子状态机 + LoadState 扩展）、`internal/host/flow/dispatcher.go`（如需提升步骤触发）、`config.example.jsonc`（示例）、`README.md`（文档）
