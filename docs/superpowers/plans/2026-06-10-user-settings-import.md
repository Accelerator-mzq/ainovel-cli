# 用户设定导入（settings/ 目录直读 + 共创原文保全）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用户把设定文档放 `./settings/` 目录即可全文导入引擎（不经输入框 2000 字限制、不经共创草稿有损压缩）；共创 Ctrl+S 时用户对话原文不再丢失；放宽输入与共创回复限制。

**Architecture:** 设定全文落盘为 store 的 `user_settings.md`，由 `novel_context` 的 Architect 路径直接注入（绕开"Coordinator 传话给 Architect 时改写丢失"的链路）；共创对话的用户原文在 Ctrl+S 时合并落盘到同一文件，A/B 两路共用一条注入管道。`user_settings` 不进 `trimByBudget` 的 trimOrder，永不被裁剪，但注入时自带体量上限。

**Tech Stack:** Go 1.25，无新依赖。测试固件沿用 `store.NewStore(t.TempDir())`。

**根因回顾（来自调查）：**
- `internal/entry/tui/model.go:109` `ta.CharLimit = 2000` — 输入框 2000 字符上限
- `internal/host/cocreate.go:109` `WithMaxTokens(2048)` — 共创每轮回复上限，draft 协议要求每轮全文重写 → 长设定必被压缩
- `internal/entry/startup/cocreate.go:144` `BuildPlan()` 只用 `DraftPrompt()` — Ctrl+S 丢弃对话原文

**执行环境：** Windows 11 + PowerShell；分支 `feature/user-settings-import`（基于 `feature/auto-engine-hardening`，因 docs/user-guide.md 与 novel_context 改动依赖 PR#4）；严格 TDD；中文注释；commit message 用给定文本不加尾注。

---

## Task 1: store — UserSettings 子存储

**Files:**
- Create: `internal/store/settings.go`
- Create: `internal/store/settings_test.go`
- Modify: `internal/store/store.go`（组合根挂载，参照其他子 store 的挂载方式）

- [ ] **Step 1: 写失败测试**（新建 settings_test.go）

```go
package store

import "testing"

// TestUserSettings_SaveLoad 验证设定全文的落盘与读取。
func TestUserSettings_SaveLoad(t *testing.T) {
	s := NewStore(t.TempDir())
	// 未保存时返回空串不报错
	if got, err := s.Settings.LoadUserSettings(); err != nil || got != "" {
		t.Fatalf("empty load = %q, %v", got, err)
	}
	content := "## 文件：世界观.md\n\n修炼境界：练气 → 筑基\n"
	if err := s.Settings.SaveUserSettings(content); err != nil {
		t.Fatal(err)
	}
	got, err := s.Settings.LoadUserSettings()
	if err != nil || got != content {
		t.Fatalf("load = %q, %v", got, err)
	}
	// 覆盖写
	if err := s.Settings.SaveUserSettings("v2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Settings.LoadUserSettings(); got != "v2" {
		t.Fatalf("overwrite load = %q", got)
	}
}
```

- [ ] **Step 2: 确认编译失败**

Run: `go test ./internal/store/ -run TestUserSettings_SaveLoad -v` → FAIL（Settings 未定义）

- [ ] **Step 3: 实现**（新建 settings.go；IO 方法名以 `internal/store/io.go` 实际为准——读取参照 `LoadPremise` 用的读文件方法，写入用原子写 Markdown 的方法）

```go
package store

import "os"

// SettingsStore 管理用户外部设定文档（user_settings.md）。
// 内容来自启动目录 settings/ 文件夹与共创对话原文，由 novel_context
// 的 Architect 路径注入，作为规划与设定生成的最高优先级参考。
type SettingsStore struct{ io *IO }

func NewSettingsStore(io *IO) *SettingsStore { return &SettingsStore{io: io} }

// SaveUserSettings 全量覆盖写入 user_settings.md（原子写）。
func (s *SettingsStore) SaveUserSettings(content string) error {
	return s.io.WriteMarkdown("user_settings.md", content)
}

// LoadUserSettings 读取设定全文；文件不存在返回空串。
func (s *SettingsStore) LoadUserSettings() (string, error) {
	data, err := s.io.ReadFile("user_settings.md")
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
```

`store.go` 组合根：在 Store struct 与构造函数里挂 `Settings *SettingsStore`（照抄相邻子 store 的写法）。

- [ ] **Step 4: 测试通过** → `go test ./internal/store/ -v`
- [ ] **Step 5: 提交** `feat(store): 新增 user_settings.md 用户设定文档存储`

## Task 2: 设定收集与启动落盘

**Files:**
- Create: `internal/host/settings.go`
- Create: `internal/host/settings_test.go`
- Modify: `internal/host/host.go`（New 末段同步落盘 + 事件提示）
- Modify: `internal/entry/startup/cocreate.go`（BuildPlan 携带用户原文）+ `internal/entry/startup/plan.go`（Plan struct，若字段在别处定义按实际）
- Modify: TUI 启动调用点（把 Plan.UserNotes 经 Host 落盘——以实际调用链为准，见 Step 5）

- [ ] **Step 1: 写失败测试**（新建 internal/host/settings_test.go）

```go
package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCollectUserSettings 验证 settings/ 目录扫描：递归、按路径排序、文件头分隔、忽略非文本扩展。
func TestCollectUserSettings(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "settings", "world")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, "settings", rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("b-角色.md", "主角：林尘")
	write(filepath.Join("world", "a-境界.txt"), "练气→筑基")
	write("ignore.png", "binary")

	content, files, err := CollectUserSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if files != 2 {
		t.Fatalf("files = %d, want 2", files)
	}
	// 按相对路径字典序：b-角色.md 在前还是 world/a-境界.txt 在前由排序规则决定——
	// 断言两段都在且各有文件头
	if !strings.Contains(content, "## 文件：b-角色.md") || !strings.Contains(content, "主角：林尘") {
		t.Fatalf("缺 b-角色.md 段：%q", content)
	}
	if !strings.Contains(content, "a-境界") || !strings.Contains(content, "练气→筑基") {
		t.Fatalf("缺 world/a-境界.txt 段：%q", content)
	}
	if strings.Contains(content, "binary") {
		t.Fatal("不应读取 .png")
	}

	// settings/ 不存在 → 空内容不报错
	content, files, err = CollectUserSettings(t.TempDir())
	if err != nil || content != "" || files != 0 {
		t.Fatalf("missing dir: %q %d %v", content, files, err)
	}
}

// TestCollectUserSettings_SizeCap 验证单文件超限截断并带提示。
func TestCollectUserSettings_SizeCap(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("设", maxSettingsFileRunes+100)
	if err := os.WriteFile(filepath.Join(dir, "settings", "big.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	content, _, err := CollectUserSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "（已截断）") {
		t.Fatal("超限文件应截断并标注")
	}
	if got := len([]rune(content)); got > maxSettingsFileRunes+500 {
		t.Fatalf("截断未生效，len=%d", got)
	}
}
```

- [ ] **Step 2: 确认失败** → `go test ./internal/host/ -run TestCollectUserSettings -v` 编译失败

- [ ] **Step 3: 实现**（新建 internal/host/settings.go）

```go
package host

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// settings 收集体量上限：单文件 / 全部合计（rune 计）。
// 超限截断并标注——上限取 Architect 上下文预算（novel_context trim 60KB）的安全余量。
const (
	maxSettingsFileRunes  = 30000
	maxSettingsTotalRunes = 60000
)

// settingsExts 允许的设定文件扩展名（与 /simulate 的语料口径一致）。
var settingsExts = map[string]bool{".md": true, ".txt": true, ".markdown": true}

// CollectUserSettings 递归读取 baseDir/settings/ 下的文本文件，
// 按相对路径字典序拼接为带文件头的 Markdown 全文。
// 目录不存在返回空串；单文件与合计超限时截断并标注。
func CollectUserSettings(baseDir string) (content string, files int, err error) {
	root := filepath.Join(baseDir, "settings")
	info, statErr := os.Stat(root)
	if statErr != nil || !info.IsDir() {
		return "", 0, nil // 没有 settings 目录是常态，不是错误
	}

	var paths []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if settingsExts[strings.ToLower(filepath.Ext(path))] {
			paths = append(paths, path)
		}
		return nil
	})
	if walkErr != nil {
		return "", 0, fmt.Errorf("扫描 settings 目录失败: %w", walkErr)
	}
	sort.Strings(paths)

	var b strings.Builder
	total := 0
	for _, p := range paths {
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return "", 0, fmt.Errorf("读取设定文件 %s 失败: %w", p, readErr)
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		if runes := []rune(text); len(runes) > maxSettingsFileRunes {
			text = string(runes[:maxSettingsFileRunes]) + "\n\n（已截断）"
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = filepath.Base(p)
		}
		section := fmt.Sprintf("## 文件：%s\n\n%s\n\n", filepath.ToSlash(rel), text)
		if total+len([]rune(section)) > maxSettingsTotalRunes {
			b.WriteString("\n（更多设定文件因总量超限未纳入，请精简 settings/ 内容）\n")
			break
		}
		b.WriteString(section)
		total += len([]rune(section))
		files++
	}
	return strings.TrimSpace(b.String()), files, nil
}
```

- [ ] **Step 4: 测试通过**

- [ ] **Step 5: Host 启动落盘 + 共创原文保全（接线）**

(a) `internal/host/host.go` New 函数中、`store.RunMeta.Init(...)` 附近插入（位置：store 可用之后）：

```go
	// 同步用户设定：启动目录 settings/ 有内容时全文落盘 user_settings.md，
	// 由 novel_context 的 Architect 路径注入。目录缺失/为空时保留已有落盘内容
	//（设定一旦进书就是书的一部分，删源目录不应清空）。
	if cwd, cerr := os.Getwd(); cerr == nil {
		if content, files, serr := CollectUserSettings(cwd); serr != nil {
			slog.Warn("用户设定收集失败", "module", "boot", "err", serr)
		} else if files > 0 {
			if werr := store.Settings.SaveUserSettings(content); werr != nil {
				slog.Warn("用户设定落盘失败", "module", "boot", "err", werr)
			} else {
				h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info",
					Summary: fmt.Sprintf("已加载用户设定：%d 个文件，共 %d 字", files, len([]rune(content)))})
			}
		}
	}
```

注意：emitEvent 在 h 构造完成后才能用——把这段放在 `h := &Host{...}` 之后、`return h, nil` 之前（events channel buffered=100，订阅前 emit 不丢即可；若现有代码模式是订阅后才 emit，改用 slog.Info 输出加载提示并在注释说明，二选一以现有模式为准）。

(b) 共创原文保全：`internal/entry/startup/cocreate.go` 给 CoCreateSession 加方法：

```go
// UserTranscript 返回共创对话里全部用户原文（按轮次拼接），
// 供 Ctrl+S 后追加落盘到 user_settings.md——草稿是有损压缩，原文必须保全。
func (s *CoCreateSession) UserTranscript() string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	for i, m := range s.history {
		if strings.ToLower(strings.TrimSpace(m.Role)) != "user" {
			continue
		}
		fmt.Fprintf(&b, "### 用户输入 %d\n\n%s\n\n", i+1, strings.TrimSpace(m.Content))
	}
	return strings.TrimSpace(b.String())
}
```

Plan struct（找到其定义处，应在 startup 包）加字段 `UserNotes string`；`BuildPlan()` 返回时带 `UserNotes: s.UserTranscript()`。

(c) Host 加落盘入口（internal/host/settings.go 追加）：

```go
// AppendCoCreateTranscript 把共创对话用户原文追加进 user_settings.md。
// 在已有设定（settings/ 目录内容）之后以独立章节追加；无已有内容则单独成文。
func (h *Host) AppendCoCreateTranscript(transcript string) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return
	}
	existing, err := h.store.Settings.LoadUserSettings()
	if err != nil {
		slog.Warn("读取已有用户设定失败，跳过共创原文保全", "module", "boot", "err", err)
		return
	}
	section := "# 共创对话用户原文（备查）\n\n" + transcript
	merged := section
	if strings.TrimSpace(existing) != "" {
		// 去重：重复 Ctrl+S（重开同名书）时替换旧的共创段而不是无限追加
		if idx := strings.Index(existing, "# 共创对话用户原文（备查）"); idx >= 0 {
			existing = strings.TrimSpace(existing[:idx])
		}
		if existing != "" {
			merged = existing + "\n\n" + section
		}
	}
	if err := h.store.Settings.SaveUserSettings(merged); err != nil {
		slog.Warn("共创原文落盘失败", "module", "boot", "err", err)
	}
}
```

(d) TUI 调用点：找到共创模式 Ctrl+S 后用 Plan 启动 Host 的位置（grep `BuildPlan\(\)` 在 internal/entry/tui 的调用），在 `StartPrepared(plan.StartPrompt)` 之前调用 `runtime.AppendCoCreateTranscript(plan.UserNotes)`。

(e) 为 (b) 加单测（cocreate_test.go 或新建，构造 history 含 user/assistant 混合，断言只取 user 且带轮次头）。

- [ ] **Step 6: 全量验证 + 提交**

`go build ./... && go vet ./... && go test ./...` 全绿。
提交：`feat(host): settings/ 目录设定收集落盘 + 共创对话用户原文保全`

## Task 3: novel_context — Architect 路径注入 user_settings

**Files:**
- Modify: `internal/tools/novel_context_builders.go`（buildArchitectFoundation 末尾）
- Test: `internal/tools/novel_context_test.go`（追加；固件参照现有 architect 路径用例）

- [ ] **Step 1: 写失败测试**

```go
// TestNovelContext_UserSettingsInjected 验证 Architect 路径注入用户设定全文。
func TestNovelContext_UserSettingsInjected(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Settings.SaveUserSettings("## 文件：境界.md\n\n练气→筑基"); err != nil {
		t.Fatal(err)
	}
	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	// user_settings 在 foundation envelope 下（具体嵌套位置以 envelope.apply 的输出结构为准，
	// 实施时打印 out 确认后写精确断言——目标：值包含 "练气→筑基"）
	if !strings.Contains(string(raw), "练气→筑基") {
		t.Fatalf("architect 路径未注入 user_settings: %s", raw)
	}
}
```

- [ ] **Step 2: 确认失败**

- [ ] **Step 3: 实现**（buildArchitectFoundation 末尾、`foundation_status` 之前插入）

```go
	// 用户外部设定文档：最高优先级的规划依据。不进 trimByBudget 的 trimOrder
	//（永不裁剪），体量已在收集端（CollectUserSettings）限制。
	if settings, err := t.store.Settings.LoadUserSettings(); err == nil && settings != "" {
		envelope.Foundation["user_settings"] = settings
	} else {
		warn("user_settings", err)
	}
```

确认 `user_settings` 不在 novel_context.go trimOrder 列表中（不加即可）。

- [ ] **Step 4: 测试通过 + 提交** `feat(tools): novel_context Architect 路径注入 user_settings 设定全文`

## Task 4: prompt 引导与输入限制放宽

**Files:**
- Modify: `assets/prompts/architect-short.md` + `architect-long.md`（文件名以 assets/prompts/ 实际为准）
- Modify: `internal/host/cocreate.go`（system prompt + MaxTokens）
- Modify: `internal/entry/tui/model.go:109`（CharLimit）

- [ ] **Step 1: architect prompts**

两个 architect prompt 在"输入/上下文"相关章节追加一条（措辞对齐文件现有风格）：

```markdown
- novel_context 返回中若含 `user_settings`（用户外部设定文档），它是最高优先级的设定依据：
  境界体系、世界规则、人物设定必须以它为准展开，与你的自由发挥冲突时服从 user_settings；
  缺失的细节才由你补全。
```

- [ ] **Step 2: cocreate 调整**

(a) `coCreateSystemPrompt` 的 `<draft>` 段说明追加一句：

```
如果用户粘贴了大段设定/资料原文，草稿里**不要复述原文**，用一行"（详细设定见用户原文，已由系统保全）"指代即可——原文会由系统完整保留并交给创作引擎，草稿只需记录讨论得出的增量决策。
```

(b) `internal/host/cocreate.go:109` `WithMaxTokens(2048)` → `WithMaxTokens(4096)`（draft 全文重写协议下 2048 容易截断，放宽一倍；注释说明）。

- [ ] **Step 3: CharLimit**

`internal/entry/tui/model.go:109` `ta.CharLimit = 2000` → `ta.CharLimit = 10000`，注释：`// 放宽到 1 万字符：支持粘贴中等篇幅设定段落；超长设定走 settings/ 目录导入`。

- [ ] **Step 4: 验证 + 提交**

`go build ./... && go test ./...` 全绿（prompt 改动无单测，靠 Task 5 后的人工冒烟）。
提交：`feat(prompts): user_settings 遵循引导 + 共创草稿不复述原文 + 输入限制放宽`

## Task 5: 文档

**Files:**
- Modify: `docs/user-guide.md`
- Modify: `README.md`（最小改动：特性列表一条）

- [ ] **Step 1: user-guide.md**

(a) 第 2 节（写一本书）在 2.1 启动之后插入小节「**导入已有设定**」：settings/ 目录用法（支持 .md/.txt/.markdown、递归、字典序、单文件 3 万字/总量 6 万字上限）、何时生效（启动时同步，Architect 规划全程遵循）、与 rules.md 的分工（settings=世界观/人物全文素材，rules=持续硬约束红线）。
(b) 第 6 节输出树 `personas.json` 附近加一行 `user_settings.md`。
(c) FAQ 中「设定怎么导入」相关条目（若上一轮没加则新增）改写为指向 settings/ 目录为首选路径；并新增一条 FAQ：「共创里贴的设定会丢吗？」答：Ctrl+S 时用户原文自动保全进 user_settings.md，但推荐长设定直接走 settings/ 目录。

- [ ] **Step 2: README.md**

特性列表加一条：

```markdown
- **设定文档直读** — 把世界观/人物设定（.md/.txt）放 `settings/` 目录，启动时全文注入规划上下文，绕开输入框长度与对话压缩损耗；共创对话中的用户原文也会自动保全
```

- [ ] **Step 3: 终验 + 提交**

`go build ./... && go vet ./... && go test ./...` 全绿。
提交：`docs: 设定导入（settings/ 目录）使用说明`

---

## 总验收清单

- [ ] `go build ./... && go vet ./... && go test ./...` 全绿
- [ ] 放置 `settings/世界观.md` 启动新书 → TUI 出现"已加载用户设定"事件；`output/{书名}/user_settings.md` 含全文；novel_context（chapter=0）返回 user_settings
- [ ] 共创贴设定 → Ctrl+S → `user_settings.md` 含「共创对话用户原文（备查）」段
- [ ] 不放 settings/、不用共创 → 行为零变化（user_settings.md 不产生）
- [ ] 输入框可粘贴 1 万字符以内文本
