# 画像驱动竞稿人格（替代 StyleBlock）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 竞稿人格的文风信号统一为"语料生成的仿写画像"：`./simulate/personas/<作者名>/` 语料 → 人格画像 → 启动时与主画像 LLM 融合（缓存）→ 竞稿写手经专属 novel_context 注入单一融合画像；删除凭作者名生成 StyleBlock 的旧机制。

**Architecture:** 生成期融合、运行期单信号。`/simulate` 增量生成主画像 + N 份人格画像（store: `meta/simulation_personas.json`）；装配时 `persona.EnsureFused` 按（主画像 UpdatedAt + 人格画像 UpdatedAt）缓存键融合（store: `meta/contest_fused_profiles.json`）；`build.go` 给每个竞稿写手 `ContextTool.WithProfileSource` 专属实例；缺任一人格画像则 build.go 与 host.go 经共享谓词 `persona.MissingProfiles` 一致禁用竞稿（subagent 不注册 + 路由不 SetContest）。

**Tech Stack:** Go（项目既有栈），无新依赖。测试用既有 `scriptedLLM` / `t.TempDir()` + `store.NewStore` 模式。

**规约：** 注释一律中文。spec 见 `docs/superpowers/specs/2026-06-10-persona-profile-unification-design.md`。上游合并热点：`internal/agents/build.go`、`internal/tools/novel_context*.go`、`internal/host/sim/`。

**设计要点（实现时必须遵守）：**
- 人格画像与融合缓存 key 用**作者名**（中文名 slug 是 index 相关的 `persona{N}`，重排会错位）。
- 竞稿写手 SystemPrompt = writerPrompt 原样，不再拼接任何人格块；文风约束完全经 novel_context 的 `simulation_profile`（即融合画像）注入，现有"## 仿写画像"指导段零修改适配。
- `/simulate` 根目录扫描必须排除 `personas/` 子树，否则人格语料污染主画像。
- 缺画像 → 竞稿禁用（slog 警告 + 回退单 writer），**不阻断启动**：首次配置时用户要能进 TUI 跑 `/simulate`（鸡生蛋）。
- 融合失败 → 人格画像原样兜底 + `Fallback` 标记（缓存视为无效，下次启动重试），不阻断。

---

## Task 1: domain + store — 融合缓存类型与人格画像存取

**Files:**
- Modify: `internal/domain/simulation.go`（文件末尾追加类型）
- Modify: `internal/store/simulation.go`（追加 4 个方法）
- Test: `internal/store/simulation_personas_test.go`（新建）

- [ ] **Step 1: 写失败测试**

新建 `internal/store/simulation_personas_test.go`：

```go
package store

import (
	"path/filepath"
	"testing"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
)

// 人格画像与融合缓存的存取往返：写入后读回一致；文件不存在返回空 map 而非错误。
func TestPersonaProfilesRoundTrip(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "novel"))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}

	// 不存在 → 空 map
	got, err := st.Simulation.LoadPersonaProfiles()
	if err != nil {
		t.Fatalf("LoadPersonaProfiles: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("初始应为空 map，got %d 项", len(got))
	}

	in := map[string]domain.SimulationProfile{
		"乌贼": {
			Version:   domain.SimulationProfileVersion,
			UpdatedAt: "2026-06-10T00:00:00Z",
			Synthesis: domain.SimulationSynthesis{
				Style: domain.SimulationStyle{NarrativeVoice: []string{"阴郁第三人称"}},
			},
		},
	}
	if err := st.Simulation.SavePersonaProfiles(in); err != nil {
		t.Fatalf("SavePersonaProfiles: %v", err)
	}
	got, err = st.Simulation.LoadPersonaProfiles()
	if err != nil {
		t.Fatalf("LoadPersonaProfiles after save: %v", err)
	}
	if got["乌贼"].UpdatedAt != "2026-06-10T00:00:00Z" {
		t.Fatalf("读回 UpdatedAt 不一致: %+v", got["乌贼"])
	}
	if got["乌贼"].Synthesis.Style.NarrativeVoice[0] != "阴郁第三人称" {
		t.Fatalf("读回 synthesis 不一致: %+v", got["乌贼"])
	}
}

func TestFusedProfilesRoundTrip(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "novel"))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}

	got, err := st.Simulation.LoadFusedProfiles()
	if err != nil {
		t.Fatalf("LoadFusedProfiles: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("初始应为空 map，got %d 项", len(got))
	}

	in := map[string]domain.FusedPersonaProfile{
		"乌贼": {
			BaseStamp:    "base-1",
			PersonaStamp: "p-1",
			Fallback:     true,
			Profile:      domain.SimulationProfile{Version: domain.SimulationProfileVersion},
		},
	}
	if err := st.Simulation.SaveFusedProfiles(in); err != nil {
		t.Fatalf("SaveFusedProfiles: %v", err)
	}
	got, err = st.Simulation.LoadFusedProfiles()
	if err != nil {
		t.Fatalf("LoadFusedProfiles after save: %v", err)
	}
	entry := got["乌贼"]
	if entry.BaseStamp != "base-1" || entry.PersonaStamp != "p-1" || !entry.Fallback {
		t.Fatalf("读回融合缓存不一致: %+v", entry)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store/ -run 'TestPersonaProfilesRoundTrip|TestFusedProfilesRoundTrip' -v`
Expected: FAIL（编译错误：`LoadPersonaProfiles`/`FusedPersonaProfile` 未定义）

- [ ] **Step 3: 实现**

`internal/domain/simulation.go` 文件末尾追加：

```go
// FusedPersonaProfile 是竞稿写手的融合画像缓存条目：
// 主画像（BaseStamp）与人格画像（PersonaStamp）任一更新即失效重融合；
// Fallback=true 表示融合 LLM 调用失败、暂用人格画像原样，下次启动重试（缓存视为无效）。
type FusedPersonaProfile struct {
	BaseStamp    string            `json:"base_stamp"`
	PersonaStamp string            `json:"persona_stamp"`
	Fallback     bool              `json:"fallback,omitempty"`
	Profile      SimulationProfile `json:"profile"`
}
```

`internal/store/simulation.go` 文件末尾追加：

```go
// LoadPersonaProfiles 读取竞稿人格画像集合（key=作者名）；文件不存在返回空 map。
// key 用作者名而非 slug：中文名 slug 是 index 相关的 persona{N}，重排配置会错位。
func (s *SimulationStore) LoadPersonaProfiles() (map[string]domain.SimulationProfile, error) {
	m := make(map[string]domain.SimulationProfile)
	if err := s.io.ReadJSON("meta/simulation_personas.json", &m); err != nil {
		if os.IsNotExist(err) {
			return map[string]domain.SimulationProfile{}, nil
		}
		return nil, err
	}
	return m, nil
}

// SavePersonaProfiles 全量写回人格画像集合。
func (s *SimulationStore) SavePersonaProfiles(m map[string]domain.SimulationProfile) error {
	return s.io.WriteJSON("meta/simulation_personas.json", m)
}

// LoadFusedProfiles 读取竞稿融合画像缓存（key=作者名）；文件不存在返回空 map。
func (s *SimulationStore) LoadFusedProfiles() (map[string]domain.FusedPersonaProfile, error) {
	m := make(map[string]domain.FusedPersonaProfile)
	if err := s.io.ReadJSON("meta/contest_fused_profiles.json", &m); err != nil {
		if os.IsNotExist(err) {
			return map[string]domain.FusedPersonaProfile{}, nil
		}
		return nil, err
	}
	return m, nil
}

// SaveFusedProfiles 全量写回融合画像缓存。
func (s *SimulationStore) SaveFusedProfiles(m map[string]domain.FusedPersonaProfile) error {
	return s.io.WriteJSON("meta/contest_fused_profiles.json", m)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/store/ ./internal/domain/ -v -run 'TestPersonaProfilesRoundTrip|TestFusedProfilesRoundTrip'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/domain/simulation.go internal/store/simulation.go internal/store/simulation_personas_test.go
git commit -m "feat(store): 人格画像与融合缓存存取（key=作者名）"
```

---

## Task 2: sim scanner — personas 子目录扫描 + 主画像扫描排除

**Files:**
- Modify: `internal/host/sim/scanner.go`
- Test: `internal/host/sim/scanner_test.go`（新建）

- [ ] **Step 1: 写失败测试**

新建 `internal/host/sim/scanner_test.go`：

```go
package sim

import (
	"os"
	"path/filepath"
	"testing"
)

// 主画像扫描必须排除 personas/ 子树，否则人格语料会污染主画像。
func TestScanSourcesSkipsPersonasSubtree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("main corpus"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "personas", "乌贼"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "personas", "乌贼", "p.txt"), []byte("persona corpus"), 0o644); err != nil {
		t.Fatal(err)
	}

	sources, err := scanSources(root)
	if err != nil {
		t.Fatalf("scanSources: %v", err)
	}
	if len(sources) != 1 || sources[0].RelativePath != "a.txt" {
		t.Fatalf("主画像扫描应只含根目录语料，got %+v", sources)
	}
}

func TestScanPersonaDirs(t *testing.T) {
	root := t.TempDir()
	// 两个人格目录：一个有语料、一个为空（空目录也要返回，由调用方告警跳过）
	if err := os.MkdirAll(filepath.Join(root, "personas", "乌贼"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "personas", "乌贼", "p.txt"), []byte("persona corpus"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "personas", "空人格"), 0o755); err != nil {
		t.Fatal(err)
	}
	// personas/ 下的散文件（非目录）应被忽略
	if err := os.WriteFile(filepath.Join(root, "personas", "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := scanPersonaDirs(root)
	if err != nil {
		t.Fatalf("scanPersonaDirs: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("应返回 2 个人格目录，got %d", len(dirs))
	}
	// 按作者名排序：空人格 < 乌贼（按 Unicode 序，断言用名字查找避免依赖排序细节）
	byAuthor := map[string]personaCorpus{}
	for _, d := range dirs {
		byAuthor[d.Author] = d
	}
	if got := byAuthor["乌贼"]; len(got.Sources) != 1 || got.Sources[0].RelativePath != "p.txt" {
		t.Fatalf("乌贼语料扫描错误: %+v", got)
	}
	if got := byAuthor["空人格"]; len(got.Sources) != 0 {
		t.Fatalf("空目录 Sources 应为空: %+v", got)
	}
}

// personas/ 目录不存在时返回 nil, nil（纯主画像场景）。
func TestScanPersonaDirsMissing(t *testing.T) {
	dirs, err := scanPersonaDirs(t.TempDir())
	if err != nil {
		t.Fatalf("缺 personas/ 目录不应报错: %v", err)
	}
	if dirs != nil {
		t.Fatalf("应返回 nil，got %+v", dirs)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/host/sim/ -run 'TestScanSources|TestScanPersonaDirs' -v`
Expected: FAIL（`scanPersonaDirs`/`personaCorpus` 未定义；SkipsPersonasSubtree 测试 got 2 个 source）

- [ ] **Step 3: 实现**

`internal/host/sim/scanner.go`：

(a) 在 `type scannedSource struct` 之后追加：

```go
// personaCorpus 是 personas/<作者名>/ 子目录扫出的人格语料。
type personaCorpus struct {
	Author  string
	Dir     string
	Sources []scannedSource
}

const personasDirName = "personas"
```

(b) `scanSources` 中 WalkDir 回调的目录分支改为跳过顶层 `personas/`。把：

```go
	var out []scannedSource
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
```

改为：

```go
	var out []scannedSource
	personasRoot := filepath.Join(root, personasDirName)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			// personas/ 子树是人格语料，由 scanPersonaDirs 单独扫，不混入当前画像
			if path == personasRoot {
				return filepath.SkipDir
			}
			return nil
		}
```

(c) 文件末尾追加：

```go
// scanPersonaDirs 扫描 root/personas/ 下的作者子目录，返回按作者名排序的人格语料列表。
// personas/ 不存在返回 nil, nil；空子目录也会返回（Sources 为空），由调用方告警跳过。
func scanPersonaDirs(root string) ([]personaCorpus, error) {
	personasRoot := filepath.Join(strings.TrimSpace(root), personasDirName)
	entries, err := os.ReadDir(personasRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []personaCorpus
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(personasRoot, e.Name())
		sources, err := scanSources(dir)
		if err != nil {
			return nil, err
		}
		out = append(out, personaCorpus{Author: e.Name(), Dir: dir, Sources: sources})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Author < out[j].Author })
	return out, nil
}
```

（`os`/`sort`/`strings`/`filepath` 均已在 import 中。）

- [ ] **Step 4: 跑测试确认通过（含既有扫描行为不回归）**

Run: `go test ./internal/host/sim/ -v`
Expected: PASS（含既有 runner 测试）

- [ ] **Step 5: Commit**

```bash
git add internal/host/sim/scanner.go internal/host/sim/scanner_test.go
git commit -m "feat(sim): personas 子目录扫描，主画像扫描排除 personas/ 子树"
```

---

## Task 3: sim runner — 人格画像增量生成管线

**Files:**
- Modify: `internal/host/sim/runner.go`（重构 `Run`，抽出 `runMainProfile` + 新增 `runPersonaProfile`）
- Test: `internal/host/sim/runner_test.go`（追加 2 个测试）

- [ ] **Step 1: 写失败测试**

`internal/host/sim/runner_test.go` 末尾追加：

```go
// 只有人格语料、根目录为空：应生成人格画像而不生成主画像（无主画像场景合法）。
// 重跑时按指纹增量，0 次 LLM 调用。
func TestRunnerGeneratesPersonaProfiles(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "simulate")
	personaDir := filepath.Join(sourceDir, "personas", "乌贼")
	if err := os.MkdirAll(personaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(personaDir, "sample.txt"), []byte("persona corpus"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := store.NewStore(filepath.Join(dir, "output", "novel"))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	llm := &scriptedLLM{responses: []string{
		validSourceReportJSON("persona tone"),
		validSynthesisJSON("persona synthesis"),
	}}
	drainRun(t, st, llm, sourceDir)
	if got := llm.calls.Load(); got != 2 {
		t.Fatalf("人格画像生成 LLM 调用 = %d, want 2（1 analyze + 1 merge）", got)
	}

	profiles, err := st.Simulation.LoadPersonaProfiles()
	if err != nil {
		t.Fatalf("LoadPersonaProfiles: %v", err)
	}
	p, ok := profiles["乌贼"]
	if !ok || len(p.Corpus.Sources) != 1 {
		t.Fatalf("人格画像未生成或语料数错误: %+v", profiles)
	}
	// 主画像不应被人格语料污染
	main, err := st.Simulation.Load()
	if err != nil {
		t.Fatal(err)
	}
	if main != nil {
		t.Fatalf("根目录无语料，主画像不应生成: %+v", main)
	}

	// 重跑：指纹未变，0 次调用
	llm2 := &scriptedLLM{}
	drainRun(t, st, llm2, sourceDir)
	if got := llm2.calls.Load(); got != 0 {
		t.Fatalf("增量重跑 LLM 调用 = %d, want 0", got)
	}
}

// 空人格目录：告警跳过（等同缺画像），主画像照常生成。
func TestRunnerSkipsEmptyPersonaDir(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "simulate")
	if err := os.MkdirAll(filepath.Join(sourceDir, "personas", "空人格"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "a.txt"), []byte("main corpus"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := store.NewStore(filepath.Join(dir, "output", "novel"))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	llm := &scriptedLLM{responses: []string{
		validSourceReportJSON("main tone"),
		validSynthesisJSON("main synthesis"),
	}}
	events, err := Run(context.Background(), Deps{
		Store:   st,
		LLM:     llm,
		Prompts: Prompts{Source: "source prompt", Merge: "merge prompt"},
	}, Options{SourceDir: sourceDir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sawEmptyWarn bool
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("simulate errored: %v", ev.Err)
		}
		if strings.Contains(ev.Message, "目录为空") {
			sawEmptyWarn = true
		}
	}
	if !sawEmptyWarn {
		t.Fatal("空人格目录应产生'目录为空'跳过提示")
	}
	main, _ := st.Simulation.Load()
	if main == nil || len(main.Corpus.Sources) != 1 {
		t.Fatalf("主画像应照常生成: %+v", main)
	}
	profiles, _ := st.Simulation.LoadPersonaProfiles()
	if len(profiles) != 0 {
		t.Fatalf("空目录不应生成人格画像: %+v", profiles)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/host/sim/ -run 'TestRunnerGeneratesPersonaProfiles|TestRunnerSkipsEmptyPersonaDir' -v`
Expected: FAIL（根目录为空时 Run 报 "no simulation sources"；空目录无跳过提示）

- [ ] **Step 3: 重构 Run**

`internal/host/sim/runner.go` 中把整个 `Run` 函数替换为下面三个函数（`AnalyzeSource`/`MergeSynthesis`/其余 helper 不动）：

```go
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
```

- [ ] **Step 4: 跑全包测试确认通过（既有测试断言"画像已是最新"/StageDone 子串均兼容）**

Run: `go test ./internal/host/sim/ -v`
Expected: PASS（4 个既有 + 2 个新增）

- [ ] **Step 5: Commit**

```bash
git add internal/host/sim/runner.go internal/host/sim/runner_test.go
git commit -m "feat(sim): /simulate 增量生成人格画像（personas/<作者名>/，逐个落盘）"
```

---

## Task 4: 融合 prompt 资产 + sim.FuseSynthesis

**Files:**
- Create: `assets/prompts/simulation-persona-fuse.md`
- Modify: `assets/load.go`（Prompts 结构 + 加载项）
- Create: `internal/host/sim/fuse.go`
- Test: `internal/host/sim/fuse_test.go`（新建）

- [ ] **Step 1: 写失败测试**

新建 `internal/host/sim/fuse_test.go`：

```go
package sim

import (
	"context"
	"testing"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
)

func minimalProfile(voice string) *domain.SimulationProfile {
	return &domain.SimulationProfile{
		Version: domain.SimulationProfileVersion,
		Synthesis: domain.SimulationSynthesis{
			Style: domain.SimulationStyle{NarrativeVoice: []string{voice}},
		},
	}
}

func TestFuseSynthesisParsesLLMOutput(t *testing.T) {
	llm := &scriptedLLM{responses: []string{validSynthesisJSON("fused texture")}}
	syn, err := FuseSynthesis(context.Background(), llm, "fuse prompt", minimalProfile("base voice"), minimalProfile("persona voice"))
	if err != nil {
		t.Fatalf("FuseSynthesis: %v", err)
	}
	if len(syn.Style.ProseTexture) == 0 || syn.Style.ProseTexture[0] != "fused texture" {
		t.Fatalf("融合 synthesis 解析错误: %+v", syn.Style)
	}
	if got := llm.calls.Load(); got != 1 {
		t.Fatalf("LLM 调用 = %d, want 1", got)
	}
}

func TestFuseSynthesisRequiresInputs(t *testing.T) {
	llm := &scriptedLLM{}
	if _, err := FuseSynthesis(context.Background(), llm, "", minimalProfile("a"), minimalProfile("b")); err == nil {
		t.Error("空 prompt 应报错")
	}
	if _, err := FuseSynthesis(context.Background(), llm, "p", nil, minimalProfile("b")); err == nil {
		t.Error("nil base 应报错（无主画像时调用方直接退化，不应进入融合）")
	}
	if _, err := FuseSynthesis(context.Background(), llm, "p", minimalProfile("a"), nil); err == nil {
		t.Error("nil persona 应报错")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/host/sim/ -run TestFuseSynthesis -v`
Expected: FAIL（`FuseSynthesis` 未定义）

- [ ] **Step 3: 实现 sim/fuse.go**

新建 `internal/host/sim/fuse.go`：

```go
package sim

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
	"github.com/voocel/agentcore"
)

// FuseSynthesis 把"主画像基底 + 人格画像变奏"融合为一份 synthesis，
// 供竞稿写手作为唯一文风信号。融合规则在 systemPrompt
// （assets/prompts/simulation-persona-fuse.md）中声明：人格主导风格层、主画像主导结构层。
// 无主画像时调用方应直接用人格画像退化，不要调本函数。
func FuseSynthesis(ctx context.Context, llm LLMChat, systemPrompt string, base, persona *domain.SimulationProfile) (*domain.SimulationSynthesis, error) {
	if strings.TrimSpace(systemPrompt) == "" {
		return nil, fmt.Errorf("fuse prompt is required")
	}
	if base == nil || persona == nil {
		return nil, fmt.Errorf("fuse requires both base and persona profiles")
	}
	resp, err := llm.Generate(ctx, []agentcore.Message{
		agentcore.SystemMsg(systemPrompt),
		agentcore.UserMsg(buildFuseUserPrompt(base, persona)),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("llm fuse profile: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("llm fuse profile: nil response")
	}
	var synthesis domain.SimulationSynthesis
	if err := parseJSONPayload(resp.Message.TextContent(), &synthesis); err != nil {
		return nil, fmt.Errorf("parse fused synthesis: %w", err)
	}
	return &synthesis, nil
}

func buildFuseUserPrompt(base, persona *domain.SimulationProfile) string {
	payload := map[string]any{
		"base_profile":    domain.CompactSimulationProfile(base),
		"persona_profile": domain.CompactSimulationProfile(persona),
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return "Fuse the persona profile onto the base profile. Return only the requested JSON object.\n\n" + string(data)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/host/sim/ -run TestFuseSynthesis -v`
Expected: PASS

- [ ] **Step 5: 新增融合 prompt 资产并接入 assets**

新建 `assets/prompts/simulation-persona-fuse.md`：

```markdown
# 角色

你是文风画像融合器。输入是 JSON：`base_profile`（主画像，当前作品的整体仿写方向）与 `persona_profile`（人格画像，某位作者的个人文风，来自真实语料分析）。你要产出一份融合画像的 synthesis JSON，作为一个写作 AI 的**唯一**文风约束。

# 融合规则

1. 风格层以人格画像为主导：`style`（narrative_voice / sentence_rhythm / prose_texture / perspective / mood）与 `lexicon` 优先取 persona_profile 的条目；base_profile 的同类条目仅在不与人格冲突时补充。
2. 结构层以主画像为基底：`plot_design`、`hook_design`、`pacing_density`、`reader_engagement` 以 base_profile 为基础，融入 persona_profile 中明显的个人手法作为变奏。
3. `style.do_not_copy` 取两者并集，一条都不能删。
4. `role_guidance` 只需融合 writer 维度（其余角色不消费融合画像），coordinator/architect/editor 可留空数组。
5. 每个数组去重、去空、不超过 12 条，保留最具操作性的条目。
6. 只输出 JSON 对象本身，不要任何解释文字或代码围栏。

# 输出结构

与输入画像的 synthesis 相同：

{"style":{"narrative_voice":[],"sentence_rhythm":[],"prose_texture":[],"perspective":[],"mood":[],"do_not_copy":[]},"lexicon":{"common_words":[],"emotion_words":[],"scene_words":[],"transition_words":[],"signature_phrases":[]},"plot_design":{"opening_patterns":[],"escalation_patterns":[],"turning_point_patterns":[],"payoff_patterns":[]},"hook_design":{"hook_types":[],"placement":[],"cliffhanger_patterns":[],"payoff_rules":[]},"pacing_density":{"scene_density":[],"information_release":[],"dialogue_action_ratio":[],"compression_rules":[]},"reader_engagement":{"methods":[],"emotional_drivers":[],"progression_rewards":[],"anti_patterns":[]},"role_guidance":{"coordinator":[],"architect":[],"writer":[],"editor":[]}}
```

`assets/load.go` 改两处：

(a) `Prompts` 结构体追加字段（`SimulationMerge string` 之后）：

```go
	SimulationPersonaFuse string
```

(b) `loadPrompts()` 返回值追加（`SimulationMerge:` 行之后）：

```go
		SimulationPersonaFuse: mustRead(promptsFS, "prompts/simulation-persona-fuse.md"),
```

- [ ] **Step 6: 编译 + assets 测试**

Run: `go build ./... && go test ./assets/ -v`
Expected: PASS（embed 加载成功；若 mustRead panic 说明文件名拼错）

- [ ] **Step 7: Commit**

```bash
git add assets/prompts/simulation-persona-fuse.md assets/load.go internal/host/sim/fuse.go internal/host/sim/fuse_test.go
git commit -m "feat(sim): 画像融合 FuseSynthesis + simulation-persona-fuse prompt"
```

---

## Task 5: persona resolver — MissingProfiles + EnsureFused

**Files:**
- Create: `internal/host/persona/resolver.go`（与 generator.go 同包并存，slugFor 复用；旧 Generator 在 Task 8 删除）
- Test: `internal/host/persona/resolver_test.go`（新建）

- [ ] **Step 1: 写失败测试**

新建 `internal/host/persona/resolver_test.go`：

```go
package persona

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
	"github.com/Accelerator-mzq/ainovel-cli/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(filepath.Join(t.TempDir(), "novel"))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	return st
}

func seedPersonaProfile(t *testing.T, st *store.Store, author, updatedAt string) {
	t.Helper()
	m, err := st.Simulation.LoadPersonaProfiles()
	if err != nil {
		t.Fatal(err)
	}
	m[author] = domain.SimulationProfile{
		Version:   domain.SimulationProfileVersion,
		UpdatedAt: updatedAt,
		Synthesis: domain.SimulationSynthesis{
			Style: domain.SimulationStyle{NarrativeVoice: []string{author + " voice"}},
		},
	}
	if err := st.Simulation.SavePersonaProfiles(m); err != nil {
		t.Fatal(err)
	}
}

func seedBaseProfile(t *testing.T, st *store.Store, updatedAt string) {
	t.Helper()
	if err := st.Simulation.Save(domain.SimulationProfile{
		Version:   domain.SimulationProfileVersion,
		UpdatedAt: updatedAt,
		Synthesis: domain.SimulationSynthesis{
			Style: domain.SimulationStyle{NarrativeVoice: []string{"base voice"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

// countingFuse 返回固定 synthesis 并计数；err 非 nil 时模拟融合失败。
func countingFuse(calls *int, failErr error) FuseFunc {
	return func(_ context.Context, _, persona *domain.SimulationProfile) (*domain.SimulationSynthesis, error) {
		*calls++
		if failErr != nil {
			return nil, failErr
		}
		return &domain.SimulationSynthesis{
			Style: domain.SimulationStyle{NarrativeVoice: []string{"fused:" + persona.Synthesis.Style.NarrativeVoice[0]}},
		}, nil
	}
}

func TestMissingProfiles(t *testing.T) {
	st := newTestStore(t)
	seedPersonaProfile(t, st, "乌贼", "t1")
	missing, err := MissingProfiles(st, []string{"乌贼", "肘子"})
	if err != nil {
		t.Fatalf("MissingProfiles: %v", err)
	}
	if len(missing) != 1 || missing[0] != "肘子" {
		t.Fatalf("missing = %v, want [肘子]", missing)
	}
	// AvailableProfiles 用于缺失告警时提示拼写错位
	available, err := AvailableProfiles(st)
	if err != nil {
		t.Fatalf("AvailableProfiles: %v", err)
	}
	if len(available) != 1 || available[0] != "乌贼" {
		t.Fatalf("available = %v, want [乌贼]", available)
	}
}

// 有主画像：首跑融合并缓存；重跑 0 次融合；主画像更新后缓存失效重融合。
func TestEnsureFusedCachesAndInvalidates(t *testing.T) {
	st := newTestStore(t)
	seedBaseProfile(t, st, "base-t1")
	seedPersonaProfile(t, st, "乌贼", "p-t1")
	seedPersonaProfile(t, st, "肘子", "p-t1")
	authors := []string{"乌贼", "肘子"}

	calls := 0
	out, err := EnsureFused(context.Background(), st, authors, countingFuse(&calls, nil))
	if err != nil {
		t.Fatalf("EnsureFused: %v", err)
	}
	if calls != 2 || len(out) != 2 {
		t.Fatalf("首跑融合调用 = %d（want 2），结果 %d 个", calls, len(out))
	}
	// slug 必须按 index 与 Slugs() 一致（中文名 → persona{N}），防张冠李戴
	if out[0].Slug != "persona1" || out[1].Slug != "persona2" {
		t.Fatalf("slug 错位: %q / %q", out[0].Slug, out[1].Slug)
	}
	if out[0].Profile.Synthesis.Style.NarrativeVoice[0] != "fused:乌贼 voice" {
		t.Fatalf("融合结果未生效: %+v", out[0].Profile.Synthesis.Style)
	}

	// 缓存命中：0 次融合
	calls = 0
	if _, err := EnsureFused(context.Background(), st, authors, countingFuse(&calls, nil)); err != nil {
		t.Fatalf("二次 EnsureFused: %v", err)
	}
	if calls != 0 {
		t.Fatalf("缓存命中仍融合了 %d 次", calls)
	}

	// 主画像更新 → 全部失效重融合
	seedBaseProfile(t, st, "base-t2")
	calls = 0
	if _, err := EnsureFused(context.Background(), st, authors, countingFuse(&calls, nil)); err != nil {
		t.Fatalf("主画像更新后 EnsureFused: %v", err)
	}
	if calls != 2 {
		t.Fatalf("主画像更新后融合调用 = %d, want 2", calls)
	}
}

// 无主画像：退化为人格画像本身，不调融合。
func TestEnsureFusedNoBaseUsesPersonaProfile(t *testing.T) {
	st := newTestStore(t)
	seedPersonaProfile(t, st, "乌贼", "p-t1")

	calls := 0
	out, err := EnsureFused(context.Background(), st, []string{"乌贼"}, countingFuse(&calls, nil))
	if err != nil {
		t.Fatalf("EnsureFused: %v", err)
	}
	if calls != 0 {
		t.Fatalf("无主画像不应调融合，calls = %d", calls)
	}
	if out[0].Profile.Synthesis.Style.NarrativeVoice[0] != "乌贼 voice" {
		t.Fatalf("应退化为人格画像原样: %+v", out[0].Profile.Synthesis.Style)
	}
}

// 融合失败：人格画像兜底 + Fallback 标记；下次启动重试（Fallback 缓存视为无效）。
func TestEnsureFusedFallbackRetries(t *testing.T) {
	st := newTestStore(t)
	seedBaseProfile(t, st, "base-t1")
	seedPersonaProfile(t, st, "乌贼", "p-t1")

	calls := 0
	out, err := EnsureFused(context.Background(), st, []string{"乌贼"}, countingFuse(&calls, fmt.Errorf("llm down")))
	if err != nil {
		t.Fatalf("融合失败不应返回 error（兜底不阻断）: %v", err)
	}
	if !out[0].Fallback {
		t.Fatal("融合失败应标记 Fallback")
	}
	if out[0].Profile.Synthesis.Style.NarrativeVoice[0] != "乌贼 voice" {
		t.Fatalf("兜底应为人格画像原样: %+v", out[0].Profile.Synthesis.Style)
	}

	// 重试：Fallback 缓存无效 → 再次调融合，成功后覆盖
	calls = 0
	out, err = EnsureFused(context.Background(), st, []string{"乌贼"}, countingFuse(&calls, nil))
	if err != nil {
		t.Fatalf("重试 EnsureFused: %v", err)
	}
	if calls != 1 {
		t.Fatalf("Fallback 缓存应触发重试，calls = %d, want 1", calls)
	}
	if out[0].Fallback || out[0].Profile.Synthesis.Style.NarrativeVoice[0] != "fused:乌贼 voice" {
		t.Fatalf("重试成功后应为融合结果: %+v", out[0])
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/host/persona/ -run 'TestMissingProfiles|TestEnsureFused' -v`
Expected: FAIL（`FuseFunc`/`MissingProfiles`/`EnsureFused` 未定义）

- [ ] **Step 3: 实现 resolver.go**

新建 `internal/host/persona/resolver.go`：

```go
// internal/host/persona/resolver.go
package persona

import (
	"context"
	"fmt"
	"sort"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
	"github.com/Accelerator-mzq/ainovel-cli/internal/store"
)

// FuseFunc 把"主画像基底 + 人格画像变奏"融合为一份 synthesis。
// 注入以便测试与解耦具体 LLM（实际实现为 sim.FuseSynthesis 的闭包包装）。
type FuseFunc func(ctx context.Context, base, persona *domain.SimulationProfile) (*domain.SimulationSynthesis, error)

// Resolved 是竞稿写手装配所需的完整人格信息：身份（Author/Slug）+ 运行期唯一文风信号（Profile）。
type Resolved struct {
	Author   string
	Slug     string
	Profile  *domain.SimulationProfile
	Fallback bool // true 表示融合失败、Profile 为人格画像原样兜底
}

// MissingProfiles 返回 authors 中没有人格画像的作者名列表。
// build.go（subagent 注册）与 host.go（Dispatcher 路由）共用此谓词，
// 保证"缺画像 → 竞稿禁用"在两个装配入口上的判定一致。
func MissingProfiles(st *store.Store, authors []string) ([]string, error) {
	stored, err := st.Simulation.LoadPersonaProfiles()
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, a := range authors {
		if _, ok := stored[a]; !ok {
			missing = append(missing, a)
		}
	}
	return missing, nil
}

// AvailableProfiles 返回已生成人格画像的作者名列表（排序稳定输出），
// 供缺画像告警时一并展示——配置作者名与 ./simulate/personas/ 目录名拼写错位时，
// 用户对照两个列表即可定位。
func AvailableProfiles(st *store.Store) ([]string, error) {
	stored, err := st.Simulation.LoadPersonaProfiles()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(stored))
	for name := range stored {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// EnsureFused 返回 authors 对应的融合画像列表：缓存命中（主画像与人格画像
// UpdatedAt 均未变且非 Fallback）直接复用；否则调 fuse 重融合并写回缓存。
// 失败兜底人格画像原样 + Fallback 标记，不阻断（下次启动重试）。
// 调用方应先用 MissingProfiles 确认画像齐全。
func EnsureFused(ctx context.Context, st *store.Store, authors []string, fuse FuseFunc) ([]Resolved, error) {
	// 主画像读失败按"无主画像"退化处理：读错误 ≠ 永久损坏，退化融合优于阻断竞稿。
	base, _ := st.Simulation.Load()
	stored, err := st.Simulation.LoadPersonaProfiles()
	if err != nil {
		return nil, fmt.Errorf("load persona profiles: %w", err)
	}
	// 缓存损坏时静默重建（重新融合比阻断流程更合适，对齐旧 personas.json 策略）
	cache, _ := st.Simulation.LoadFusedProfiles()
	if cache == nil {
		cache = map[string]domain.FusedPersonaProfile{}
	}

	baseStamp := ""
	if base != nil {
		baseStamp = base.UpdatedAt
	}
	out := make([]Resolved, 0, len(authors))
	dirty := false

	for i, author := range authors {
		pp, ok := stored[author]
		if !ok {
			// 防御：调用方已查 MissingProfiles，此处命中说明启动期文件被外部改动
			return out, fmt.Errorf("persona profile missing for %q", author)
		}
		// slug 必须按当前 index 重算，与 Slugs() 完全一致（中文名 → persona{N}），
		// 否则重排配置会让 build.go 注册与 host.go 路由张冠李戴。
		slug := slugFor(author, i)

		if c, hit := cache[author]; hit && !c.Fallback && c.BaseStamp == baseStamp && c.PersonaStamp == pp.UpdatedAt {
			prof := c.Profile
			out = append(out, Resolved{Author: author, Slug: slug, Profile: &prof})
			continue
		}

		var resolved Resolved
		if base == nil {
			// 无主画像：融合退化为人格画像本身，不调 LLM（"贴近主画像"目标自动放宽）
			prof := pp
			resolved = Resolved{Author: author, Slug: slug, Profile: &prof}
		} else if syn, ferr := fuse(ctx, base, &pp); ferr != nil || syn == nil {
			prof := pp
			resolved = Resolved{Author: author, Slug: slug, Profile: &prof, Fallback: true}
		} else {
			fused := domain.SimulationProfile{
				Version:   domain.SimulationProfileVersion,
				CreatedAt: pp.CreatedAt,
				UpdatedAt: pp.UpdatedAt,
				Corpus:    pp.Corpus, // 沿用人格语料清单，compact 注入时 source_files 显示真实来源
				Synthesis: *syn,
			}
			resolved = Resolved{Author: author, Slug: slug, Profile: &fused}
		}
		out = append(out, resolved)
		cache[author] = domain.FusedPersonaProfile{
			BaseStamp:    baseStamp,
			PersonaStamp: pp.UpdatedAt,
			Fallback:     resolved.Fallback,
			Profile:      *resolved.Profile,
		}
		dirty = true
	}

	if dirty {
		if err := st.Simulation.SaveFusedProfiles(cache); err != nil {
			return out, fmt.Errorf("cache fused profiles: %w", err)
		}
	}
	return out, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/host/persona/ -v`
Expected: PASS（含既有 generator 测试，暂未删除）

- [ ] **Step 5: Commit**

```bash
git add internal/host/persona/resolver.go internal/host/persona/resolver_test.go
git commit -m "feat(persona): MissingProfiles 共享谓词 + EnsureFused 融合缓存（兜底重试）"
```

---

## Task 6: ContextTool — WithProfileSource 专属画像源

**Files:**
- Modify: `internal/tools/novel_context.go`（字段 + 方法）
- Modify: `internal/tools/novel_context_builders.go`（buildSimulationProfile 改走可注入源）
- Test: `internal/tools/novel_context_simulation_test.go`（追加 1 个测试）

- [ ] **Step 1: 写失败测试**

`internal/tools/novel_context_simulation_test.go` 末尾追加：

```go
// 竞稿写手的专属 ContextTool 必须返回注入的融合画像，且不影响共享实例（防串台）。
func TestContextToolWithProfileSourceOverrides(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	// store 里放主画像
	if err := st.Simulation.Save(domain.SimulationProfile{
		Version: domain.SimulationProfileVersion,
		Synthesis: domain.SimulationSynthesis{
			Style: domain.SimulationStyle{NarrativeVoice: []string{"base voice"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Start", CoreEvent: "Begin"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 1); err != nil {
		t.Fatal(err)
	}

	fused := &domain.SimulationProfile{
		Version: domain.SimulationProfileVersion,
		Synthesis: domain.SimulationSynthesis{
			Style: domain.SimulationStyle{NarrativeVoice: []string{"fused persona voice"}},
		},
	}
	shared := NewContextTool(st, References{}, "default", rules.LoadOptions{})
	personaTool := shared.WithProfileSource(func() (*domain.SimulationProfile, error) { return fused, nil })

	voiceOf := func(tool *ContextTool) string {
		t.Helper()
		raw, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		section := payload["working_memory"].(map[string]any)
		compact := section["simulation_profile"].(map[string]any)
		style := compact["style"].(map[string]any)
		voices := style["narrative_voice"].([]any)
		return voices[0].(string)
	}

	if got := voiceOf(personaTool); got != "fused persona voice" {
		t.Fatalf("persona 实例应返回融合画像, got %q", got)
	}
	if got := voiceOf(shared); got != "base voice" {
		t.Fatalf("共享实例应仍返回主画像（不串台）, got %q", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/tools/ -run TestContextToolWithProfileSource -v`
Expected: FAIL（`WithProfileSource` 未定义）

- [ ] **Step 3: 实现**

`internal/tools/novel_context.go`：

(a) `ContextTool` 结构体追加字段：

```go
// ContextTool 组装当前章节所需上下文。
type ContextTool struct {
	store     *store.Store
	refs      References
	style     string
	rulesOpts rules.LoadOptions
	// profileSource 可选覆盖 simulation_profile 的来源（竞稿写手注入各自融合画像）；
	// nil 时走 store.Simulation.Load()（主画像，所有非竞稿角色的共享实例）。
	profileSource func() (*domain.SimulationProfile, error)
}
```

(b) `NewContextTool` 之后追加方法：

```go
// WithProfileSource 返回一个 simulation_profile 来源被覆盖的浅拷贝实例。
// 竞稿装配用：每个竞稿写手持有专属实例，注入各自的融合画像（运行期单信号）；
// 原实例不受影响（其余角色继续读主画像）。
func (t *ContextTool) WithProfileSource(fn func() (*domain.SimulationProfile, error)) *ContextTool {
	c := *t
	c.profileSource = fn
	return &c
}

// loadSimulationProfile 按注入源或主画像读取 simulation_profile。
func (t *ContextTool) loadSimulationProfile() (*domain.SimulationProfile, error) {
	if t.profileSource != nil {
		return t.profileSource()
	}
	return t.store.Simulation.Load()
}
```

`internal/tools/novel_context_builders.go` 的 `buildSimulationProfile` 中，把：

```go
	profile, err := t.store.Simulation.Load()
```

改为：

```go
	profile, err := t.loadSimulationProfile()
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/tools/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tools/novel_context.go internal/tools/novel_context_builders.go internal/tools/novel_context_simulation_test.go
git commit -m "feat(tools): ContextTool.WithProfileSource 专属画像源（竞稿写手单信号注入）"
```

---

## Task 7: build.go 竞稿装配重写 + host.go 路由一致性

**Files:**
- Modify: `internal/agents/build.go`（重写 `// ---- 多人格竞稿装配 ----` 块；删除 `generatePersonaStyle`）
- Modify: `internal/host/host.go`（SetContest 前置画像检查）

**说明：** 装配层无新单测（行为由 Task 1-6 单元测试 + assets 集成测试覆盖），分步验证编译与全量测试。

- [ ] **Step 1: 重写 build.go 竞稿装配块**

把 `internal/agents/build.go` 中从 `// ---- 多人格竞稿装配 ----` 到 judge 注册块结束（即 `allSubagents := ...` 之前的整个 `if contestCfg.Enabled() { ... }` 段）替换为：

```go
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
```

同时：
- 删除文件末尾的 `generatePersonaStyle` 函数全体。
- import 块追加 `"github.com/Accelerator-mzq/ainovel-cli/internal/host/sim"`（`domain`/`context`/`time`/`strings`/`slog`/`persona` 均已存在）。
- 注意 `writerPrompt` 定义（`writerPrompt := bundle.Prompts.Writer` + style 拼接）在 writer config 之前已存在，竞稿块直接复用，**不再拼接任何人格块**。

- [ ] **Step 2: 编译**

Run: `go build ./...`
Expected: 成功，无 unused import（若 `unicode` 等仅被 generator.go 使用则不受影响——本步未动 generator.go）

- [ ] **Step 3: host.go SetContest 前置画像检查**

`internal/host/host.go` 中把：

```go
	// 竞稿模式：向 Dispatcher 注入 persona slug 列表，与 build.go 注册的 agent 命名保持一致。
	// Slugs 与 EnsurePersonas 内部共用同一个 slugFor，保证 writer_<slug> 名字匹配。
	if wc := cfg.WritingContest.Normalize(); wc.Enabled() {
		h.router.SetContest(flow.ContestConfig{
			Personas:    persona.Slugs(wc.Personas), // persona slug 列表
			Concurrency: wc.Concurrency,             // 并发开关透传
			Synopsis:    wc.SynopsisMode(),          // 两段式开关透传
		})
	}
```

替换为：

```go
	// 竞稿模式：向 Dispatcher 注入 persona slug 列表，与 build.go 注册的 agent 命名保持一致。
	// Slugs 与 EnsureFused 内部共用同一个 slugFor，保证 writer_<slug> 名字匹配。
	// 画像缺失时跳过 SetContest——与 build.go 用同一谓词（MissingProfiles），
	// 保证"subagent 不注册"与"路由不派发"一致，否则会派发到不存在的 agent。
	if wc := cfg.WritingContest.Normalize(); wc.Enabled() {
		if missing, perr := persona.MissingProfiles(store, wc.Personas); perr != nil || len(missing) > 0 {
			slog.Warn("竞稿路由未启用：人格画像缺失或读取失败（详见 agent 模块日志）",
				"module", "host", "missing", strings.Join(missing, "、"), "err", perr)
		} else {
			h.router.SetContest(flow.ContestConfig{
				Personas:    persona.Slugs(wc.Personas), // persona slug 列表
				Concurrency: wc.Concurrency,             // 并发开关透传
				Synopsis:    wc.SynopsisMode(),          // 两段式开关透传
			})
		}
	}
```

确认 host.go import 中已有 `log/slog` 与 `strings`（缺则补）。

- [ ] **Step 4: 全量编译 + 测试**

Run: `go build ./... && go test ./...`
Expected: 除 `assets/simulation_contest_test.go` 的 `TestPersonaWriterCarriesBothSignals` 可能仍引用旧拼接语义外全部 PASS（该测试在 Task 8 反转；若本步全绿说明它只测字符串拼接、不依赖 build.go，正常）

- [ ] **Step 5: Commit**

```bash
git add internal/agents/build.go internal/host/host.go
git commit -m "feat(agents): 竞稿写手注入融合画像单信号，缺人格画像时 build/host 一致禁用竞稿"
```

---

## Task 8: 删除旧 StyleBlock 机制

**Files:**
- Delete: `internal/host/persona/generator.go`、`internal/host/persona/generator_test.go`
- Create: `internal/host/persona/slug.go`、`internal/host/persona/slug_test.go`（slugFor/Slugs 保留迁移）
- Modify: `internal/store/contest.go`（删 SavePersonas/LoadPersonas）+ `internal/store/contest_test.go`（删对应测试）
- Modify: `internal/domain/contest.go`（删 Persona 类型）
- Modify: `assets/simulation_contest_test.go`（反转双信号测试）

- [ ] **Step 1: 迁移 slug 工具到独立文件**

⚠ 本步完成后包内 `slugFor`/`Slugs` 暂时重复定义（generator.go 还在），编译会失败——**Step 1 与 Step 2 必须连续执行，中间不要单独跑测试**，Step 5 统一验证。

新建 `internal/host/persona/slug.go`（内容从 generator.go 原样迁移）：

```go
// internal/host/persona/slug.go
package persona

import (
	"fmt"
	"strings"
	"unicode"
)

// slugFor 生成稳定 slug：纯 ASCII 作者名转小写（空格转连字符），
// 含非 ASCII（中文等）则回退 persona{序号}，保证唯一稳定。
func slugFor(author string, index int) string {
	ascii := true
	for _, r := range author {
		if r > unicode.MaxASCII {
			ascii = false
			break
		}
	}
	if !ascii {
		return fmt.Sprintf("persona%d", index+1)
	}
	// 非字母数字一律转连字符，折叠连续连字符并去除首尾，避免污染文件路径
	out := make([]rune, 0, len(author))
	prevHyphen := false
	for _, r := range author {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, unicode.ToLower(r))
			prevHyphen = false
		} else if !prevHyphen {
			out = append(out, '-')
			prevHyphen = true
		}
	}
	slug := strings.Trim(string(out), "-")
	if slug == "" {
		slug = fmt.Sprintf("persona%d", index+1) // 全特殊字符兜底
	}
	return slug
}

// Slugs 把作者名列表转为稳定 slug 列表（与 EnsureFused 一致）。
// build.go 注册与 host.go 路由都依赖此函数推导 agent 命名，必须与 EnsureFused 完全一致。
func Slugs(authors []string) []string {
	out := make([]string, len(authors))
	for i, a := range authors {
		out[i] = slugFor(a, i)
	}
	return out
}
```

新建 `internal/host/persona/slug_test.go`：

```go
package persona

import (
	"reflect"
	"testing"
)

func TestSlugFor(t *testing.T) {
	cases := []struct {
		author string
		index  int
		want   string
	}{
		{"Brandon Sanderson", 0, "brandon-sanderson"}, // ASCII：小写 + 连字符
		{"乌贼", 0, "persona1"},                         // 中文：index 回退
		{"乌贼", 2, "persona3"},                         // index 相关
		{"a..b", 0, "a-b"},                            // 特殊字符折叠
		{"!!!", 1, "persona2"},                        // 全特殊字符兜底
	}
	for _, c := range cases {
		if got := slugFor(c.author, c.index); got != c.want {
			t.Errorf("slugFor(%q, %d) = %q, want %q", c.author, c.index, got, c.want)
		}
	}
}

func TestSlugsMatchesIndexOrder(t *testing.T) {
	got := Slugs([]string{"乌贼", "肘子"})
	want := []string{"persona1", "persona2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Slugs = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: 删除旧 generator 与人格缓存**

```bash
git rm internal/host/persona/generator.go internal/host/persona/generator_test.go
```

`internal/store/contest.go`：删除 `SavePersonas` 与 `LoadPersonas` 两个方法（含注释）。

`internal/store/contest_test.go`：删除引用 `SavePersonas`/`LoadPersonas`/`domain.Persona` 的测试函数（用 `rg -n 'SavePersonas|LoadPersonas|domain\.Persona\b' internal/store/` 定位）。

`internal/domain/contest.go`：删除 `Persona` 类型（文件头注释相应更新为"竞稿裁定与进度类型"；`PersonaScore`/`Verdict`/`ContestProgress` 保留）。

- [ ] **Step 3: 反转 assets 双信号测试**

`assets/simulation_contest_test.go` 中删除 `TestPersonaWriterCarriesBothSignals` 全体，替换为：

```go
// TestContestWriterSingleStyleSignal 验证 StyleBlock 机制删除后的最终态：
// 竞稿写手与普通 writer 共用同一 system prompt（含"## 仿写画像"指导段，
// 运行期文风信号唯一来源是 novel_context 注入的融合画像），不再有
// "## 你的写作人格" prompt 拼接。融合 prompt 资产必须可加载。
func TestContestWriterSingleStyleSignal(t *testing.T) {
	b := Load("default")

	if !strings.Contains(b.Prompts.Writer, simGuidanceAnchor) {
		t.Error("writer prompt 应含仿写画像指导段（竞稿写手复用同一 prompt）")
	}
	if strings.Contains(b.Prompts.Writer, "## 你的写作人格") {
		t.Error("StyleBlock 人格块机制已删除，writer prompt 不应再含人格块标记")
	}
	if strings.TrimSpace(b.Prompts.SimulationPersonaFuse) == "" {
		t.Error("simulation-persona-fuse prompt 应非空")
	}
}
```

（`TestSimulationGuidanceInjectedPerRole` 保留不动——Judge 排除断言继续有效。）

- [ ] **Step 4: 残留引用清零验证**

Run: `rg -n 'StyleBlock|EnsurePersonas|generatePersonaStyle|StyleGenFunc|style_block' --glob '!docs/**' --glob '!*.md'`
Expected: 0 hits

Run: `rg -n 'domain\.Persona\b' --glob '!docs/**'`
Expected: 0 hits（`domain.PersonaScore` 不匹配 `\b`，不算）

- [ ] **Step 5: 全量编译 + 测试**

Run: `go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(persona): 删除 StyleBlock 旧机制（generator/personas.json/Persona 类型），slug 工具独立保留"
```

---

## Task 9: 配置注释与文档

**Files:**
- Modify: `internal/bootstrap/config.go`（Personas 字段注释）
- Modify: `config.example.jsonc`（writing_contest 注释）
- Modify: `docs/user-guide.md`（竞稿 + 仿写章节）

- [ ] **Step 1: 更新 config.go 字段注释**

`internal/bootstrap/config.go` 中把：

```go
	// Personas 是作者名列表（如 ["乌贼","卖报小郎君","土豆"]）。
	// 数量即并行 Writer 数；< 2 时不启用竞稿。文风由启动时 LLM 依作者名生成。
	Personas []string `json:"personas,omitempty"`
```

改为：

```go
	// Personas 是作者名列表（如 ["乌贼","卖报小郎君","土豆"]）。
	// 数量即并行 Writer 数；< 2 时不启用竞稿。
	// 每个作者名必须有对应人格画像：把该作者作品语料放入 ./simulate/personas/<作者名>/
	// 并运行 /simulate 生成；缺任一画像则竞稿整体禁用（启动日志会列出缺失项），重启生效。
	// 运行期写手的文风信号 = 人格画像与主画像的融合画像（经 novel_context 注入，单信号）。
	Personas []string `json:"personas,omitempty"`
```

- [ ] **Step 2: 更新 config.example.jsonc**

定位 `writing_contest` 块（`rg -n 'personas' config.example.jsonc`），把 personas 行的尾注释/邻近说明替换为与上一步一致的语义（语料目录 + /simulate + 缺画像禁用 + 重启生效）。保持 jsonc 原有缩进风格。

- [ ] **Step 3: 更新 user-guide**

`docs/user-guide.md`（`rg -n '竞稿|personas|simulate' docs/user-guide.md` 定位两处章节）：

(a) 多人格竞稿章节：删除"文风由 LLM 依作者名生成"相关描述，替换为以下内容（措辞可贴合上下文微调，要点不可少）：

```markdown
竞稿人格的文风来自**真实语料生成的人格画像**（不再凭作者名想象）：

1. 把每位作者的作品语料（.txt/.md）放入 `./simulate/personas/<作者名>/`（目录名须与配置中的作者名完全一致）；
2. 运行 `/simulate` 生成人格画像（与主画像同一条命令，按指纹增量）；
3. 重启后竞稿生效。缺任一人格画像时竞稿整体禁用（回退单 writer），启动日志会列出缺失项。

文风信号：装配时把"主画像基底 + 人格画像变奏"融合为一份人格专属画像并缓存
（`meta/contest_fused_profiles.json`，主画像或人格语料更新后自动重融合）；
运行期每个竞稿写手只看自己的融合画像（经 novel_context 注入，单一信号）。
没有主画像时融合退化为人格画像本身，竞稿照常。
旧版 `contest/personas.json`（StyleBlock 缓存）已废弃，可手动删除。
```

(b) 仿写画像章节：在 `./simulate` 目录说明处补充子目录约定：

```markdown
`./simulate/` 根目录语料生成主画像；`./simulate/personas/<作者名>/` 子目录语料
生成竞稿人格画像（根目录扫描会自动排除 personas/ 子树，两者互不污染）。
```

- [ ] **Step 4: 全量测试 + Commit**

Run: `go build ./... && go test ./...`
Expected: PASS

```bash
git add internal/bootstrap/config.go config.example.jsonc docs/user-guide.md
git commit -m "docs: 竞稿人格改为语料画像驱动的配置与使用说明"
```

---

## Task 10: 终验

- [ ] **Step 1: 全量验证**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全部 PASS

- [ ] **Step 2: 关键不变量人工核对清单**

- `rg -n 'WithProfileSource' internal/agents/build.go` → 竞稿写手工具列表用 `personaContext`，judge 与普通角色仍用共享 `contextTool`；
- `rg -n 'MissingProfiles' internal/` → 恰好 build.go 与 host.go 两处调用 + persona 包定义；
- `rg -n 'SystemPrompt:' internal/agents/build.go` → 竞稿写手为 `writerPrompt`（无拼接）；
- `meta/simulation_personas.json` / `meta/contest_fused_profiles.json` key 均为作者名（Task 1 测试已锁定）。

- [ ] **Step 3: 冒烟（可选，真实 LLM）**

按 `assets` 既有竞稿 e2e 通道（参考 `docs/superpowers/plans/2026-06-03-candidate-concurrency.md` 的 e2e 做法）：配置 2 个 persona + 对应 `./simulate/personas/` 语料 → `/simulate` → 重启 → 跑一章竞稿，确认候选稿 → verdict → 润色提交全链路，且日志可见"融合画像"相关条目。

- [ ] **Step 4: 收尾**

执行 superpowers:finishing-a-development-branch 流程（合并/PR 决策交用户）。
