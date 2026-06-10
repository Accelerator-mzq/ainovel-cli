package sim

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
	"github.com/voocel/agentcore"
)

const maxSourceRunes = 60000

// emitFunc 是 Run 内部的事件发射回调，传给各画像子流程。
type emitFunc func(stage Stage, current, total int, msg string, err error)

func Run(ctx context.Context, deps Deps, opts Options) (<-chan Event, error) {
	if deps.Store == nil || deps.LLM == nil {
		return nil, fmt.Errorf("deps incomplete")
	}
	if strings.TrimSpace(opts.SourceDir) == "" {
		return nil, fmt.Errorf("source dir is required")
	}

	events := make(chan Event, 32)
	go func() {
		defer close(events)
		emit := func(stage Stage, current, total int, msg string, err error) {
			ev := Event{Time: time.Now(), Stage: stage, Current: current, Total: total, Message: msg, Err: err}
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		}

		emit(StageScan, 0, 0, "扫描 simulate 语料...", nil)
		mainSources, err := scanSources(opts.SourceDir)
		if err != nil {
			emit(StageError, 0, 0, "扫描 simulate 目录失败", err)
			return
		}
		personaDirs, err := scanPersonaDirs(opts.SourceDir)
		if err != nil {
			emit(StageError, 0, 0, "扫描 personas 子目录失败", err)
			return
		}
		if len(mainSources) == 0 && len(personaDirs) == 0 {
			emit(StageError, 0, 0, "simulate 目录中没有可分析的 .txt/.md/.markdown 文件", fmt.Errorf("no simulation sources"))
			return
		}

		// 主画像：根目录语料（personas/ 子树已被扫描排除）。根目录为空时跳过，
		// 不影响既有主画像——纯人格语料场景（无主画像竞稿）合法。
		if len(mainSources) > 0 {
			if !runMainProfile(ctx, deps, opts, mainSources, emit) {
				return
			}
		}

		// 人格画像：personas/<作者名>/ 各自独立增量，逐个落盘保留部分进度。
		if len(personaDirs) > 0 {
			stored, err := deps.Store.Simulation.LoadPersonaProfiles()
			if err != nil {
				emit(StageError, 0, 0, "读取既有人格画像失败", err)
				return
			}
			for _, pc := range personaDirs {
				if len(pc.Sources) == 0 {
					emit(StageScan, 0, 0, fmt.Sprintf("人格「%s」目录为空，跳过（等同缺画像）", pc.Author), nil)
					continue
				}
				if !runPersonaProfile(ctx, deps, pc, stored, emit) {
					return
				}
			}
		}
		emit(StageDone, 0, 0, "仿写画像处理完成", nil)
	}()
	return events, nil
}

// runMainProfile 增量更新主画像；返回 false 表示已发 StageError，调用方需终止。
func runMainProfile(ctx context.Context, deps Deps, opts Options, sources []scannedSource, emit emitFunc) bool {
	existing, err := deps.Store.Simulation.Load()
	if err != nil {
		emit(StageError, 0, len(sources), "读取既有画像失败", err)
		return false
	}
	pending := pendingSources(existing, sources)
	if len(pending) == 0 {
		emit(StageScan, 0, len(sources), "主画像已是最新，未发现新增或变更文章", nil)
		return true
	}
	reports := make([]domain.SimulationSourceReport, 0, len(pending))
	for i, source := range pending {
		if err := ctx.Err(); err != nil {
			emit(StageError, i, len(pending), "用户取消画像分析", err)
			return false
		}
		emit(StageAnalyze, i+1, len(pending), fmt.Sprintf("分析仿写语料 %d/%d：%s", i+1, len(pending), source.RelativePath), nil)
		report, err := AnalyzeSource(ctx, deps.LLM, deps.Prompts.Source, source)
		if err != nil {
			emit(StageError, i+1, len(pending), "语料分析失败", err)
			return false
		}
		reports = append(reports, *report)
	}
	allReports := mergeSourceReports(existing, reports)
	emit(StageMerge, len(pending), len(pending), "合并主画像...", nil)
	synthesis, err := MergeSynthesis(ctx, deps.LLM, deps.Prompts.Merge, existing, allReports)
	if err != nil {
		emit(StageError, len(pending), len(pending), "画像合并失败", err)
		return false
	}
	profile := buildProfile(existing, opts.SourceDir, pending, reports, *synthesis, time.Now())
	if err := deps.Store.Simulation.Save(profile); err != nil {
		emit(StageError, len(pending), len(pending), "保存仿写画像失败", err)
		return false
	}
	emit(StageMerge, len(pending), len(pending), fmt.Sprintf("主画像已更新：新增/变更 %d 篇，累计 %d 篇", len(pending), len(profile.Corpus.Sources)), nil)
	return true
}

// runPersonaProfile 增量更新单个人格画像并立即落盘（部分进度可保留）；
// 返回 false 表示已发 StageError，调用方需终止。
func runPersonaProfile(ctx context.Context, deps Deps, pc personaCorpus, stored map[string]domain.SimulationProfile, emit emitFunc) bool {
	var existing *domain.SimulationProfile
	if p, ok := stored[pc.Author]; ok {
		cp := p
		existing = &cp
	}
	pending := pendingSources(existing, pc.Sources)
	if len(pending) == 0 {
		emit(StageScan, 0, len(pc.Sources), fmt.Sprintf("人格「%s」画像已是最新", pc.Author), nil)
		return true
	}
	reports := make([]domain.SimulationSourceReport, 0, len(pending))
	for i, source := range pending {
		if err := ctx.Err(); err != nil {
			emit(StageError, i, len(pending), "用户取消画像分析", err)
			return false
		}
		emit(StageAnalyze, i+1, len(pending), fmt.Sprintf("分析人格「%s」语料 %d/%d：%s", pc.Author, i+1, len(pending), source.RelativePath), nil)
		report, err := AnalyzeSource(ctx, deps.LLM, deps.Prompts.Source, source)
		if err != nil {
			emit(StageError, i+1, len(pending), "人格语料分析失败", err)
			return false
		}
		reports = append(reports, *report)
	}
	allReports := mergeSourceReports(existing, reports)
	emit(StageMerge, len(pending), len(pending), fmt.Sprintf("合并人格「%s」画像...", pc.Author), nil)
	synthesis, err := MergeSynthesis(ctx, deps.LLM, deps.Prompts.Merge, existing, allReports)
	if err != nil {
		emit(StageError, len(pending), len(pending), "人格画像合并失败", err)
		return false
	}
	profile := buildProfile(existing, pc.Dir, pending, reports, *synthesis, time.Now())
	stored[pc.Author] = profile
	if err := deps.Store.Simulation.SavePersonaProfiles(stored); err != nil {
		emit(StageError, len(pending), len(pending), "保存人格画像失败", err)
		return false
	}
	emit(StageMerge, len(pending), len(pending), fmt.Sprintf("人格「%s」画像已更新：新增/变更 %d 篇，累计 %d 篇", pc.Author, len(pending), len(profile.Corpus.Sources)), nil)
	return true
}

func AnalyzeSource(ctx context.Context, llm LLMChat, systemPrompt string, source scannedSource) (*domain.SimulationSourceReport, error) {
	if strings.TrimSpace(systemPrompt) == "" {
		return nil, fmt.Errorf("source prompt is required")
	}
	resp, err := llm.Generate(ctx, []agentcore.Message{
		agentcore.SystemMsg(systemPrompt),
		agentcore.UserMsg(buildSourceUserPrompt(source)),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("llm analyze %s: %w", source.RelativePath, err)
	}
	if resp == nil {
		return nil, fmt.Errorf("llm analyze %s: nil response", source.RelativePath)
	}
	var report domain.SimulationSourceReport
	if err := parseJSONPayload(resp.Message.TextContent(), &report); err != nil {
		return nil, fmt.Errorf("parse source report %s: %w", source.RelativePath, err)
	}
	if strings.TrimSpace(report.Summary) == "" {
		return nil, fmt.Errorf("source report %s: summary is required", source.RelativePath)
	}
	now := time.Now().Format(time.RFC3339)
	report.RelativePath = source.RelativePath
	report.SHA256 = source.SHA256
	report.Fingerprint = source.Fingerprint
	report.AnalyzedAt = now
	return &report, nil
}

func MergeSynthesis(ctx context.Context, llm LLMChat, systemPrompt string, existing *domain.SimulationProfile, reports []domain.SimulationSourceReport) (*domain.SimulationSynthesis, error) {
	if strings.TrimSpace(systemPrompt) == "" {
		return nil, fmt.Errorf("merge prompt is required")
	}
	resp, err := llm.Generate(ctx, []agentcore.Message{
		agentcore.SystemMsg(systemPrompt),
		agentcore.UserMsg(buildMergeUserPrompt(existing, reports)),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("llm merge profile: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("llm merge profile: nil response")
	}
	var synthesis domain.SimulationSynthesis
	if err := parseJSONPayload(resp.Message.TextContent(), &synthesis); err != nil {
		return nil, fmt.Errorf("parse synthesis: %w", err)
	}
	return &synthesis, nil
}

func pendingSources(existing *domain.SimulationProfile, sources []scannedSource) []scannedSource {
	if existing == nil {
		return sources
	}
	known := make(map[string]struct{}, len(existing.Corpus.Sources))
	for _, source := range existing.Corpus.Sources {
		known[domain.SimulationSourceFingerprint(source.RelativePath, source.SHA256)] = struct{}{}
	}
	var pending []scannedSource
	for _, source := range sources {
		if _, ok := known[source.Fingerprint]; ok {
			continue
		}
		pending = append(pending, source)
	}
	return pending
}

func buildProfile(
	existing *domain.SimulationProfile,
	sourceDir string,
	pending []scannedSource,
	reports []domain.SimulationSourceReport,
	synthesis domain.SimulationSynthesis,
	now time.Time,
) domain.SimulationProfile {
	stamp := now.Format(time.RFC3339)
	profile := domain.SimulationProfile{
		Version:   domain.SimulationProfileVersion,
		CreatedAt: stamp,
		UpdatedAt: stamp,
		Corpus: domain.SimulationCorpusManifest{
			SourceDir: filepath.ToSlash(sourceDir),
		},
		Synthesis: synthesis,
	}
	if existing != nil {
		profile.CreatedAt = existing.CreatedAt
		if profile.CreatedAt == "" {
			profile.CreatedAt = stamp
		}
		profile.Corpus.Sources = append(profile.Corpus.Sources, existing.Corpus.Sources...)
		profile.SourceReports = append(profile.SourceReports, existing.SourceReports...)
	}

	for i, source := range pending {
		source.AnalyzedAt = stamp
		profile.Corpus.Sources = replaceSourceByPath(profile.Corpus.Sources, source.SimulationSource)
		if i < len(reports) {
			report := reports[i]
			report.AnalyzedAt = stamp
			profile.SourceReports = replaceReportByPath(profile.SourceReports, report)
		}
	}
	sortProfile(&profile)
	return profile
}

func mergeSourceReports(existing *domain.SimulationProfile, reports []domain.SimulationSourceReport) []domain.SimulationSourceReport {
	var merged []domain.SimulationSourceReport
	if existing != nil {
		merged = append(merged, existing.SourceReports...)
	}
	for _, report := range reports {
		merged = replaceReportByPath(merged, report)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].RelativePath == merged[j].RelativePath {
			return merged[i].Fingerprint < merged[j].Fingerprint
		}
		return merged[i].RelativePath < merged[j].RelativePath
	})
	return merged
}

func replaceSourceByPath(sources []domain.SimulationSource, next domain.SimulationSource) []domain.SimulationSource {
	out := sources[:0]
	for _, source := range sources {
		if source.RelativePath == next.RelativePath {
			continue
		}
		out = append(out, source)
	}
	return append(out, next)
}

func replaceReportByPath(reports []domain.SimulationSourceReport, next domain.SimulationSourceReport) []domain.SimulationSourceReport {
	out := reports[:0]
	for _, report := range reports {
		if report.RelativePath == next.RelativePath {
			continue
		}
		out = append(out, report)
	}
	return append(out, next)
}

func sortProfile(profile *domain.SimulationProfile) {
	sort.Slice(profile.Corpus.Sources, func(i, j int) bool {
		if profile.Corpus.Sources[i].RelativePath == profile.Corpus.Sources[j].RelativePath {
			return profile.Corpus.Sources[i].Fingerprint < profile.Corpus.Sources[j].Fingerprint
		}
		return profile.Corpus.Sources[i].RelativePath < profile.Corpus.Sources[j].RelativePath
	})
	sort.Slice(profile.SourceReports, func(i, j int) bool {
		if profile.SourceReports[i].RelativePath == profile.SourceReports[j].RelativePath {
			return profile.SourceReports[i].Fingerprint < profile.SourceReports[j].Fingerprint
		}
		return profile.SourceReports[i].RelativePath < profile.SourceReports[j].RelativePath
	})
}

func buildSourceUserPrompt(source scannedSource) string {
	payload := map[string]any{
		"relative_path": source.RelativePath,
		"sha256":        source.SHA256,
		"size_bytes":    source.SizeBytes,
		"content":       compactSourceContent(source.content),
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return "Analyze this simulation corpus source and return only the requested JSON object.\n\n" + string(data)
}

func buildMergeUserPrompt(existing *domain.SimulationProfile, reports []domain.SimulationSourceReport) string {
	payload := map[string]any{
		"existing_profile": domain.CompactSimulationProfile(existing),
		"source_reports":   reports,
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return "Merge these reports into a reusable writing simulation profile. Return only the requested JSON object.\n\n" + string(data)
}

func compactSourceContent(s string) string {
	runes := []rune(s)
	if len(runes) <= maxSourceRunes {
		return s
	}
	head := maxSourceRunes * 3 / 4
	tail := maxSourceRunes - head
	return string(runes[:head]) + "\n\n[...truncated...]\n\n" + string(runes[len(runes)-tail:])
}
