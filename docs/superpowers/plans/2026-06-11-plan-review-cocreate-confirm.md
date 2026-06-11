# 规划审阅门禁 + 共创开始确认 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 规划完成后引擎主动暂停等用户审阅大纲（确认词放行/修改意见循环再审）；共创对话中用户明确说"开始"时弹确认模态进入创作。

**Architecture:** A 功能仿 budgetGuard 模式新增 planReviewGuard，与预算门禁经 composeGates 组合挂入 Dispatcher.SetGate；pending 状态由 progress.json 派生（崩溃恢复免费）。B 功能把共创 XML 协议从四标签扩到五标签（新增 `<start_intent>`），TUI 弹确认模态复用 Ctrl+S 启动链路。

**Tech Stack:** Go 1.x，bubbletea TUI，表驱动测试。设计稿：`docs/superpowers/specs/2026-06-11-plan-review-cocreate-confirm-design.md`

**分支：** `feature/plan-review-cocreate-confirm`（已建）。提交信息用中文 conventional commit，结尾加 `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`。

**文件结构总览：**

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/domain/runtime.go` | 改 | Progress 加 PlanReviewed 字段 + PlanReviewPending 纯函数 |
| `internal/domain/plan_review_test.go` | 建 | 判定表驱动测试 |
| `internal/store/progress.go` | 改 | MarkPlanReviewed |
| `internal/bootstrap/config.go` | 改 | PlanReview 三态配置 + EffectivePlanReview |
| `internal/host/planreview.go` | 建 | composeGates + planReviewGuard |
| `internal/host/host.go` | 改 | Option 注入 + 门禁组合装配 + HandleReviewInput + Snapshot 填充 |
| `internal/host/events.go` | 改 | UISnapshot.PlanReviewPending |
| `internal/entry/tui/app.go` | 改 | WithInteractive(true) |
| `internal/entry/tui/events.go` | 改 | reviewInputRuntime cmd |
| `internal/entry/tui/model_update.go` | 改 | 审阅态输入路由 |
| `internal/entry/tui/model.go` | 改 | 审阅态 placeholder；共创确认模态按键；startCoCreation 抽取 |
| `internal/entry/headless/run.go` | 改 | WithInteractive(false) + plan_review=on 审阅循环 |
| `internal/host/cocreate.go` | 改 | 五标签协议 + StartIntent 解析 |
| `internal/entry/startup/cocreate.go` | 改 | session StartIntent 生命周期 |
| `internal/entry/tui/cocreate.go` | 改 | confirmPending 状态 + 模态渲染 + ready 提示升级 |

---

## Task 1（A）: domain 判定纯函数 + Progress 字段

**Files:**
- Modify: `internal/domain/runtime.go`（Progress struct 在 37-56 行）
- Test: `internal/domain/plan_review_test.go`（新建）

- [ ] **Step 1: 写失败测试**

```go
package domain

import "testing"

func TestPlanReviewPending(t *testing.T) {
	cases := []struct {
		name string
		p    *Progress
		want bool
	}{
		{"nil progress", nil, false},
		{"规划期不 pending", &Progress{Phase: PhaseOutline}, false},
		{"规划刚完成未确认", &Progress{Phase: PhaseWriting}, true},
		{"已确认", &Progress{Phase: PhaseWriting, PlanReviewed: true}, false},
		{"已开写当前章", &Progress{Phase: PhaseWriting, CurrentChapter: 1}, false},
		{"有进行中章节", &Progress{Phase: PhaseWriting, InProgressChapter: 1}, false},
		{"有完成章节（旧书兼容）", &Progress{Phase: PhaseWriting, CompletedChapters: []int{1}}, false},
		{"完结不 pending", &Progress{Phase: PhaseComplete}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PlanReviewPending(c.p); got != c.want {
				t.Fatalf("PlanReviewPending = %v, want %v", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/domain/ -run TestPlanReviewPending -v`
Expected: FAIL（`PlanReviewed`、`PlanReviewPending` 未定义）

- [ ] **Step 3: 最小实现**

`Progress` struct 的 `Layered` 字段后追加：

```go
	// 规划审阅门禁：用户已确认大纲（plan_review 功能，确认后置 true 永不回退）
	PlanReviewed bool `json:"plan_reviewed,omitempty"`
```

文件末尾（`ExtractNovelNameFromPremise` 之前）加纯函数：

```go
// PlanReviewPending 规划已完成但用户尚未确认大纲，且写作尚未实际开始。
// 旧书兼容：已有任何章节进度（当前章/进行中/已完成）→ 永不 pending。
func PlanReviewPending(p *Progress) bool {
	return p != nil && p.Phase == PhaseWriting && !p.PlanReviewed &&
		p.CurrentChapter == 0 && p.InProgressChapter == 0 && len(p.CompletedChapters) == 0
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/domain/ -run TestPlanReviewPending -v`
Expected: PASS（8 子用例全过）

- [ ] **Step 5: Commit**

```bash
git add internal/domain/runtime.go internal/domain/plan_review_test.go
git commit -m "feat(domain): 规划审阅 pending 判定纯函数 + PlanReviewed 进度字段

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 2（A）: store 落盘 MarkPlanReviewed

**Files:**
- Modify: `internal/store/progress.go`
- Test: `internal/store/progress_test.go`（追加）

- [ ] **Step 1: 写失败测试**（追加到 progress_test.go 末尾）

```go
func TestMarkPlanReviewed(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if err := store.Progress.MarkPlanReviewed(); err != nil {
		t.Fatalf("MarkPlanReviewed: %v", err)
	}
	p, _ := store.Progress.Load()
	if !p.PlanReviewed {
		t.Fatal("PlanReviewed 应已落盘为 true")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store/ -run TestMarkPlanReviewed -v`
Expected: FAIL（MarkPlanReviewed 未定义）

- [ ] **Step 3: 实现**（progress.go，仿 `SetLayered`（237 行）同款读改写）

```go
// MarkPlanReviewed 标记用户已确认大纲，规划审阅门禁放行。
func (s *ProgressStore) MarkPlanReviewed() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.PlanReviewed = true
		return s.saveUnlocked(p)
	})
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/store/ -run TestMarkPlanReviewed -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/progress.go internal/store/progress_test.go
git commit -m "feat(store): MarkPlanReviewed 规划审阅确认落盘

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 3（A）: bootstrap 配置三态

**Files:**
- Modify: `internal/bootstrap/config.go`（Config struct 在 107 行，Budget 配置在 182-199 行作样板；`mergeConfig` 函数 grep 定位；`ValidateBase` 在 202 行）
- Test: `internal/bootstrap/config_planreview_test.go`（新建）

- [ ] **Step 1: 写失败测试**

```go
package bootstrap

import "testing"

func TestEffectivePlanReview(t *testing.T) {
	cases := []struct {
		val         string
		interactive bool
		want        bool
	}{
		{"", true, true}, {"", false, false},
		{"auto", true, true}, {"auto", false, false},
		{"on", true, true}, {"on", false, true},
		{"off", true, false}, {"off", false, false},
	}
	for _, c := range cases {
		cfg := Config{PlanReview: c.val}
		if got := cfg.EffectivePlanReview(c.interactive); got != c.want {
			t.Fatalf("plan_review=%q interactive=%v: got %v want %v", c.val, c.interactive, got, c.want)
		}
	}
}

func TestValidatePlanReview(t *testing.T) {
	for _, ok := range []string{"", "auto", "on", "off"} {
		if err := validatePlanReview(ok); err != nil {
			t.Fatalf("%q 应合法: %v", ok, err)
		}
	}
	if err := validatePlanReview("yes"); err == nil {
		t.Fatal("非法值应报错")
	}
}

func TestMergeConfig_PlanReview(t *testing.T) {
	got := mergeConfig(Config{}, Config{PlanReview: "on"})
	if got.PlanReview != "on" {
		t.Fatalf("overlay plan_review 未合并: %q", got.PlanReview)
	}
	kept := mergeConfig(Config{PlanReview: "off"}, Config{})
	if kept.PlanReview != "off" {
		t.Fatalf("overlay 为空应保留 base: %q", kept.PlanReview)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bootstrap/ -run "TestEffectivePlanReview|TestValidatePlanReview|TestMergeConfig_PlanReview" -v`
Expected: FAIL（PlanReview 字段、EffectivePlanReview、validatePlanReview 未定义）

- [ ] **Step 3: 实现**

Config struct（`Budget Budget` 字段之后）加：

```go
	// PlanReview 规划完成后是否暂停等待用户审阅大纲："auto"（TUI 开/headless 关，默认）、"on"、"off"
	PlanReview string `json:"plan_review,omitempty"`
```

Budget 方法区之后加：

```go
// PlanReview 取值枚举。
const (
	PlanReviewAuto = "auto"
	PlanReviewOn   = "on"
	PlanReviewOff  = "off"
)

// EffectivePlanReview 报告规划审阅门禁是否启用。
// auto（默认/空值）：交互式入口（TUI）启用、headless 关闭。
// interactive 由入口层装配 Host 时显式传入，不复用 startup.Request.Interactive。
func (c *Config) EffectivePlanReview(interactive bool) bool {
	switch c.PlanReview {
	case PlanReviewOn:
		return true
	case PlanReviewOff:
		return false
	}
	return interactive
}

// validatePlanReview 校验 plan_review 枚举。
func validatePlanReview(v string) error {
	switch v {
	case "", PlanReviewAuto, PlanReviewOn, PlanReviewOff:
		return nil
	}
	return fmt.Errorf("plan_review 取值必须是 auto/on/off，当前: %q", v)
}
```

`ValidateBase`（202 行）函数体内加一段：

```go
	if err := validatePlanReview(c.PlanReview); err != nil {
		return err
	}
```

`mergeConfig`（grep `func mergeConfig` 定位）按 overlay 非空覆盖的既有模式加：

```go
	if overlay.PlanReview != "" {
		out.PlanReview = overlay.PlanReview
	}
```

（注意 mergeConfig 内结果变量名可能不是 `out`，照函数内既有写法对齐。）

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bootstrap/ -run "TestEffectivePlanReview|TestValidatePlanReview|TestMergeConfig_PlanReview" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bootstrap/config.go internal/bootstrap/config_planreview_test.go
git commit -m "feat(config): plan_review 三态配置（auto/on/off）与生效判定

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 4（A）: composeGates + planReviewGuard

**Files:**
- Create: `internal/host/planreview.go`
- Test: `internal/host/planreview_test.go`（新建；测试风格仿 `internal/host/budget_test.go`）

- [ ] **Step 1: 写失败测试**

```go
package host

import (
	"testing"
)

func TestComposeGates(t *testing.T) {
	tr := func() bool { return true }
	fa := func() bool { return false }
	if g := composeGates(); g != nil {
		t.Fatal("无门禁应返回 nil（调用方不挂 gate）")
	}
	if g := composeGates(nil, nil); g != nil {
		t.Fatal("全 nil 应返回 nil")
	}
	if g := composeGates(nil, tr); g == nil || !g() {
		t.Fatal("nil 跳过 + true 应放行")
	}
	if g := composeGates(tr, fa); g() {
		t.Fatal("任一拒绝即拒绝")
	}
	if g := composeGates(tr, tr); !g() {
		t.Fatal("全部放行才放行")
	}
}

// TestPlanReviewGuard_BlockOnceResetApprove 验证：pending 拦截且首次恰好
// 一条提示+abort+notify；Reset 后再拦截重新提示；Approve 内存放行。
func TestPlanReviewGuard_BlockOnceResetApprove(t *testing.T) {
	pending := true
	var events []Event
	aborted := make(chan struct{}, 4)
	notified := make(chan struct{}, 4)
	g := newPlanReviewGuard(
		func() bool { return pending },
		func(ev Event) { events = append(events, ev) },
		func() { aborted <- struct{}{} },
		func() { notified <- struct{}{} },
	)

	if g.Allow() {
		t.Fatal("pending 应拒绝")
	}
	<-aborted
	<-notified
	if len(events) != 1 || events[0].Level != "info" {
		t.Fatalf("首次拦截应恰好一条 info 提示: %+v", events)
	}
	if g.Allow(); len(events) != 1 {
		t.Fatal("同一 pending 期只提示一次")
	}
	g.ResetPrompt()
	if g.Allow(); len(events) != 2 {
		t.Fatal("Reset 后再拦截应重新提示")
	}
	g.Approve()
	if !g.Allow() {
		t.Fatal("Approve 后即使 pending 仍 true 也应内存放行")
	}
	if g.Pending() {
		t.Fatal("Approve 后 Pending 应为 false")
	}
}

func TestPlanReviewGuard_NilNotify(t *testing.T) {
	g := newPlanReviewGuard(func() bool { return true },
		func(Event) {}, func() {}, nil)
	if g.Allow() {
		t.Fatal("pending 应拒绝")
	}
	// 不 panic 即通过
}

func TestPlanReviewGuard_NotPending(t *testing.T) {
	g := newPlanReviewGuard(func() bool { return false },
		func(Event) { t.Fatal("不应发事件") }, func() { t.Fatal("不应 abort") }, nil)
	if !g.Allow() {
		t.Fatal("非 pending 应放行")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/host/ -run "TestComposeGates|TestPlanReviewGuard" -v`
Expected: FAIL（composeGates、newPlanReviewGuard 未定义）

- [ ] **Step 3: 实现** `internal/host/planreview.go` 全文：

```go
package host

import (
	"sync"
	"time"
)

// composeGates 把多个派发门禁组合为一个：全部放行才放行；nil 成员跳过。
// 全部为 nil 时返回 nil，调用方不必挂 gate（Dispatcher.SetGate 是单槽，
// 预算门禁与规划审阅门禁经此组合后装配期一次性挂上）。
func composeGates(gates ...func() bool) func() bool {
	var active []func() bool
	for _, g := range gates {
		if g != nil {
			active = append(active, g)
		}
	}
	switch len(active) {
	case 0:
		return nil
	case 1:
		return active[0]
	}
	return func() bool {
		for _, g := range active {
			if !g() {
				return false
			}
		}
		return true
	}
}

// planReviewGuard 在规划完成、用户尚未确认大纲时拦截写作派发。
// 与 budgetGuard 组合注入 flow.Dispatcher.SetGate；Allow 可能被事件
// goroutine 并发调用，需自带锁。
type planReviewGuard struct {
	pending func() bool // 读 progress 派生（domain.PlanReviewPending）
	emit    func(Event)
	abort   func() // 首次拦截时暂停运行；异步调用避免与 coordinator 事件回调重入
	notify  func() // entry 层回调（headless 起 stdin 审阅循环）；TUI 传 nil

	mu       sync.Mutex
	prompted bool // 同一 pending 期只提示一次
	approved bool // 内存放行：确认落盘失败时本次会话仍不拦截（fail-safe）
}

func newPlanReviewGuard(pending func() bool, emit func(Event), abort, notify func()) *planReviewGuard {
	return &planReviewGuard{pending: pending, emit: emit, abort: abort, notify: notify}
}

// Pending 报告门禁当前是否处于待审阅拦截状态（含内存放行判断）。
func (g *planReviewGuard) Pending() bool {
	g.mu.Lock()
	approved := g.approved
	g.mu.Unlock()
	if approved {
		return false
	}
	return g.pending()
}

// Allow 返回 false 表示大纲待审阅，应拒绝派发新指令。
// 首次拦截 emit 提示 + 异步暂停 + 通知 entry 层。
func (g *planReviewGuard) Allow() bool {
	if !g.Pending() {
		return true
	}
	g.mu.Lock()
	first := !g.prompted
	g.prompted = true
	g.mu.Unlock()
	if first {
		g.emit(Event{Time: time.Now(), Category: "SYSTEM", Level: "info",
			Summary: "规划完成·大纲待审阅：已暂停派发。请查看 layered_outline.md，输入修改意见，或输入「开始」进入写作"})
		// 异步：Allow 在 Dispatcher 的事件回调里被调，同步 Abort 可能与 coordinator 内部锁重入
		go g.abort()
		if g.notify != nil {
			go g.notify()
		}
	}
	return false
}

// ResetPrompt 复位提示标记：用户提交修改意见后，下一次拦截重新提示+暂停。
func (g *planReviewGuard) ResetPrompt() {
	g.mu.Lock()
	g.prompted = false
	g.mu.Unlock()
}

// Approve 在内存中放行。正常路径同时有 MarkPlanReviewed 落盘；
// 落盘失败时本次会话仍放行（重启后会再次询问），绝不卡死创作。
func (g *planReviewGuard) Approve() {
	g.mu.Lock()
	g.approved = true
	g.mu.Unlock()
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/host/ -run "TestComposeGates|TestPlanReviewGuard" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/host/planreview.go internal/host/planreview_test.go
git commit -m "feat(host): 规划审阅门禁 planReviewGuard + 派发门禁组合 composeGates

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 5（A）: Host 装配 + HandleReviewInput + Snapshot

**Files:**
- Modify: `internal/host/host.go`（New 在 69 行；门禁装配在 150-158 行；Snapshot 的 `snap.Phase = string(progress.Phase)` 在 571 行附近）
- Modify: `internal/host/events.go`（UISnapshot struct 在 40 行）
- Modify: `internal/host/budget.go`（12 行注释更新）
- Test: `internal/host/planreview_test.go`（追加确认词纯函数测试）

- [ ] **Step 1: 写失败测试**（追加到 planreview_test.go）

```go
func TestIsPlanReviewConfirm(t *testing.T) {
	for _, yes := range []string{"开始", "确认", "开写", "开始写作", "  开始  "} {
		if !IsPlanReviewConfirm(yes) {
			t.Fatalf("%q 应为确认词", yes)
		}
	}
	for _, no := range []string{"", "开始吧", "把第三卷拆成两卷", "不要开始"} {
		if IsPlanReviewConfirm(no) {
			t.Fatalf("%q 不应为确认词（精确匹配）", no)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/host/ -run TestIsPlanReviewConfirm -v`
Expected: FAIL

- [ ] **Step 3: 实现**

(a) `planreview.go` 末尾加确认词与 HandleReviewInput 所需纯函数：

```go
// 规划审阅确认词：TrimSpace 后精确匹配，避免"不要开始"之类误判。
var planReviewConfirmWords = map[string]bool{
	"开始": true, "确认": true, "开写": true, "开始写作": true,
}

// IsPlanReviewConfirm 报告文本是否为规划审阅确认词。
func IsPlanReviewConfirm(text string) bool {
	return planReviewConfirmWords[strings.TrimSpace(text)]
}
```

（planreview.go import 加 `"strings"`。）

(b) Host Option 注入。`host.go` 中 `func New` 之前加：

```go
// Option 配置 Host 装配期行为。
type Option func(*hostOptions)

type hostOptions struct {
	interactive      bool
	planReviewNotify func()
}

// WithInteractive 声明宿主入口是否交互式（TUI true / headless false），
// 决定 plan_review=auto 时规划审阅门禁是否启用。
func WithInteractive(v bool) Option { return func(o *hostOptions) { o.interactive = v } }

// WithPlanReviewNotify 注入规划审阅触发回调（headless 用它起 stdin 审阅循环）。
func WithPlanReviewNotify(fn func()) Option { return func(o *hostOptions) { o.planReviewNotify = fn } }
```

New 签名改为 `func New(cfg bootstrap.Config, bundle assets.Bundle, opts ...Option) (*Host, error)`（变参向后兼容，现有调用零改动），函数体开头解析：

```go
	var o hostOptions
	for _, opt := range opts {
		opt(&o)
	}
```

(c) Host struct 加字段 `planReview *planReviewGuard`。

(d) 门禁装配（150-158 行）整段替换为组合式：

```go
	// 预算门禁 + 规划审阅门禁：组合后一次性挂上 Dispatcher（装配期只调一次）。
	var budgetAllow, planAllow func() bool
	if cfg.Budget.Enabled() {
		guard := newBudgetGuard(cfg.Budget,
			func() float64 { c, _, _, _, _ := usage.Totals(); return c },
			h.emitEvent,
			func() { h.Abort() },
		)
		budgetAllow = guard.Allow
	}
	if cfg.EffectivePlanReview(o.interactive) {
		guard := newPlanReviewGuard(
			func() bool {
				p, err := store.Progress.Load()
				if err != nil {
					return false // 读失败放行，与 store 读失败的宽松处理一致
				}
				return domain.PlanReviewPending(p)
			},
			h.emitEvent,
			func() { h.Abort() },
			o.planReviewNotify,
		)
		h.planReview = guard
		planAllow = guard.Allow
	}
	if gate := composeGates(budgetAllow, planAllow); gate != nil {
		h.router.SetGate(gate)
	}
```

（host.go 顶部 import 需含 `internal/domain`，已有则不动。`store` 变量名按 New 内实际命名对齐——150 行附近上下文里用的是哪个标识符就用哪个。）

(e) HandleReviewInput（放 host.go 的 Continue 函数之后）：

```go
// HandleReviewInput 处理规划审阅暂停态下的用户输入。
// 确认词 → 内存放行 + 落盘 PlanReviewed + Resume 进入写作，返回 true；
// 其他文本 → 复位提示标记后作为干预注入并恢复（Coordinator 改大纲），
// 处理完成后门禁会再次拦截暂停，循环直到用户确认。
func (h *Host) HandleReviewInput(text string) (approved bool, err error) {
	if IsPlanReviewConfirm(text) {
		if h.planReview != nil {
			h.planReview.Approve()
		}
		if err := h.store.Progress.MarkPlanReviewed(); err != nil {
			slog.Warn("PlanReviewed 落盘失败（本次已内存放行，重启后将再次询问）",
				"module", "host", "err", err)
		}
		_, rerr := h.Resume()
		return true, rerr
	}
	if h.planReview != nil {
		h.planReview.ResetPrompt()
	}
	return false, h.Continue(text)
}
```

（关键：修改意见走 `Continue` 而非 `Steer`——停机态 Steer 只落盘 PendingSteer 不处理，Continue 才注入并自动恢复。）

(f) `events.go` UISnapshot struct 的 `PendingSteer` 字段后加：

```go
	PlanReviewPending bool // 规划完成待用户审阅大纲（plan_review 门禁拦截中）
```

(g) host.go Snapshot 内 `snap.Phase = string(progress.Phase)`（571 行附近，progress 非 nil 的分支里）后加：

```go
	snap.PlanReviewPending = h.planReview != nil && h.planReview.Pending()
```

（注意放在 progress 非 nil 守卫外也安全——Pending 内部自己读 store；按 571 行实际上下文放在同函数即可。）

(h) `budget.go` 12 行注释 `// 注入 flow.Dispatcher.SetGate，...` 改为 `// 经 composeGates 与其他门禁组合后注入 flow.Dispatcher.SetGate，...`。

- [ ] **Step 4: 编译 + 跑包测试**

Run: `go build ./... && go test ./internal/host/ -v -run "TestIsPlanReviewConfirm|TestPlanReviewGuard|TestComposeGates|TestBudgetGuard"`
Expected: 编译通过，全 PASS（含预算门禁旧测试不回归）

- [ ] **Step 5: Commit**

```bash
git add internal/host/host.go internal/host/events.go internal/host/budget.go internal/host/planreview.go internal/host/planreview_test.go
git commit -m "feat(host): 规划审阅门禁装配（Option 注入/门禁组合/HandleReviewInput/Snapshot）

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 6（A）: TUI 审阅态交互

**Files:**
- Modify: `internal/entry/tui/app.go`（17 行 host.New 调用）
- Modify: `internal/entry/tui/events.go`（steerRuntime 在 180 行附近）
- Modify: `internal/entry/tui/model_update.go`（输入路由 290-315 行；消息分发 switch）
- Modify: `internal/entry/tui/model.go`（syncRuntimePlaceholder 在 518 行附近）
- Modify: `internal/entry/tui/panels.go`（snapshotRuntimeStateLabel 在 358 行）

无独立单测（TUI 路由层，包内既有测试保编译；行为靠 Task 9 手动验证脚本）。

- [ ] **Step 1: app.go 17 行**

```go
	rt, err := host.New(cfg, bundle, host.WithInteractive(true))
```

- [ ] **Step 2: events.go 在 `steerRuntime` 函数前加 cmd 与消息类型**

```go
// reviewInputResultMsg 规划审阅输入处理结果。
type reviewInputResultMsg struct {
	approved bool
	err      error
}

func reviewInputRuntime(rt *host.Host, text string) tea.Cmd {
	return func() tea.Msg {
		approved, err := rt.HandleReviewInput(text)
		return reviewInputResultMsg{approved: approved, err: err}
	}
}
```

- [ ] **Step 3: model_update.go 输入路由**（modeRunning case，312 行附近）改为：

```go
	case modeRunning:
		// 不本地回显 USER 事件 —— Host.Continue/Steer 入口已 emit "USER" 事件，
		// 走 events channel 回流到 TUI。架构 §2.3：观察层只观察，不产生事实。
		if m.snapshot.PlanReviewPending {
			return m, reviewInputRuntime(m.runtime, text)
		}
		if !m.snapshot.IsRunning {
			return m, continueRuntime(m.runtime, text)
		}
		return m, steerRuntime(m.runtime, text)
```

- [ ] **Step 4: model_update.go 消息分发 switch**（`abortResultMsg` case 旁）加：

```go
	case reviewInputResultMsg:
		if msg.err != nil {
			m.err = msg.err
		}
		return m, nil, true
```

（返回三元组形态按该 switch 内相邻 case 的实际签名对齐。）

- [ ] **Step 5: model.go syncRuntimePlaceholder 函数体开头**（`if m.mode != modeRunning...` 守卫之后）加：

```go
	if m.snapshot.PlanReviewPending {
		m.textarea.Placeholder = "大纲已生成（layered_outline.md）· 输入修改意见，或输入「开始」进入写作"
		return
	}
```

- [ ] **Step 6: panels.go 状态标签**。grep `snapshotRuntimeStateLabel(` 找调用点，在拿到 label 后加特判：

```go
	if snap.PlanReviewPending && snap.RuntimeState == "paused" {
		label = "待审阅"
	}
```

（调用点变量名按实际对齐；若 label 是内联表达式，先提取为局部变量。）

- [ ] **Step 7: 编译 + 包测试**

Run: `go build ./... && go test ./internal/entry/tui/`
Expected: 编译通过，既有测试 PASS

- [ ] **Step 8: Commit**

```bash
git add internal/entry/tui/
git commit -m "feat(tui): 规划审阅暂停态——专属 placeholder/状态标签/输入路由走 HandleReviewInput

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 7（A）: headless 审阅支持

**Files:**
- Modify: `internal/entry/headless/run.go`（host.New 在 43 行；Stdin 处理在 38-41 行）

- [ ] **Step 1: 实现**。run.go 43 行 `eng, err := host.New(cfg, bundle)` 整段替换为
（先声明 eng 再用闭包捕获，避免 `:=` 作用域问题）：

```go
	var eng *host.Host
	reviewNotify := func() {
		// guard 已用 goroutine 调本回调，循环内阻塞读 stdin 不影响引擎事件流
		runPlanReviewLoop(eng, stdin, stderr)
	}
	created, err := host.New(cfg, bundle,
		host.WithInteractive(false),
		host.WithPlanReviewNotify(reviewNotify))
	if err != nil {
		return err
	}
	eng = created
```

（notify 只会在引擎运行后才被 guard 触发，此时 eng 必已赋值。）

文件末尾加：

```go
// runPlanReviewLoop 在 plan_review=on 的 headless 运行中处理规划审阅：
// 打印提示，读一行 stdin 喂 HandleReviewInput。修改意见注入后引擎恢复运行，
// 下次拦截 notify 会再次触发本函数（guard.ResetPrompt 在 HandleReviewInput
// 修改分支里完成）。EOF（无人值守管道）自动确认，避免卡死自动化。
// 已知限制：与 ask_user 共享 stdin——审阅暂停期间引擎不运行，无并发争用。
func runPlanReviewLoop(eng *host.Host, stdin io.Reader, out io.Writer) {
	fmt.Fprintln(out, "\n[规划审阅] 大纲已生成（layered_outline.md）。输入修改意见，或输入「开始」进入写作：")
	r := bufio.NewReader(stdin)
	for {
		line, err := r.ReadString('\n')
		text := strings.TrimSpace(line)
		if text != "" {
			approved, herr := eng.HandleReviewInput(text)
			if herr != nil {
				fmt.Fprintf(out, "[规划审阅] 处理失败: %v\n", herr)
			}
			if approved {
				fmt.Fprintln(out, "[规划审阅] 已确认，进入写作")
			} else {
				fmt.Fprintln(out, "[规划审阅] 修改意见已注入，调整完成后将再次暂停审阅")
			}
			return
		}
		if err != nil {
			fmt.Fprintln(out, "[规划审阅] stdin 关闭，自动确认进入写作")
			_, _ = eng.HandleReviewInput("开始")
			return
		}
	}
}
```

（import 需补 `bufio`、`io`、`strings`，已有则不动。）

- [ ] **Step 2: 编译验证**

Run: `go build ./... && go vet ./internal/entry/headless/`
Expected: 通过

- [ ] **Step 3: Commit**

```bash
git add internal/entry/headless/run.go
git commit -m "feat(headless): plan_review=on 时 stdin 审阅循环（EOF 自动确认防卡死）

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 8（B）: 共创协议五标签 + StartIntent 解析

**Files:**
- Modify: `internal/host/cocreate.go`（prompt 14-44 行；tag 常量 55-60 行；splitCoCreateMarkers 211 行；extractTagContent 内两处标签表 236/251 行；coCreateLogEntry 157 行及其 defer 填充 94-106 行）
- Modify: `internal/host/events.go`（CoCreateReply 在 182 行）
- Test: `internal/host/cocreate_test.go`（已有则追加，没有则新建）

- [ ] **Step 1: 写失败测试**

```go
func TestParseCoCreate_StartIntent(t *testing.T) {
	raw := "<reply>好的，马上开始</reply>\n<draft>## 主题\n- 测试</draft>\n" +
		"<ready>true</ready>\n<start_intent>true</start_intent>\n<suggestions></suggestions>"
	reply, err := parseCoCreateResponse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reply.StartIntent {
		t.Fatal("start_intent=true 应解析为 true")
	}

	// 缺标签（旧模型/流式截断）→ false
	noTag := "<reply>继续聊</reply>\n<draft>## 主题</draft>\n<ready>false</ready>\n<suggestions></suggestions>"
	reply2, err := parseCoCreateResponse(noTag)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if reply2.StartIntent {
		t.Fatal("缺 start_intent 标签应为 false")
	}

	// false 值
	falseTag := "<reply>再聊聊</reply>\n<draft>## 主题</draft>\n<ready>true</ready>\n" +
		"<start_intent>false</start_intent>\n<suggestions></suggestions>"
	reply3, _ := parseCoCreateResponse(falseTag)
	if reply3.StartIntent {
		t.Fatal("start_intent=false 应为 false")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/host/ -run TestParseCoCreate_StartIntent -v`
Expected: FAIL（StartIntent 字段未定义）

- [ ] **Step 3: 实现**

(a) `events.go` CoCreateReply 加字段（Ready 之后）：

```go
	StartIntent bool // 用户本轮明确要求开始创作（<start_intent> 标签）
```

(b) `cocreate.go` tag 常量加：

```go
	tagStartIntent = "start_intent"
```

(c) prompt 改三处：

- 16 行 `包含四个标签` → `包含五个标签`；
- 19 行 reply 指引句改为：`如果信息已足够开始创作，告诉用户可以按 Ctrl+S 或直接说「开始」。`
- `<ready>false</ready>` 行之后插入一行：

```
<start_intent>false</start_intent>
```

- 输出规范段：`必须使用四个 XML 标签：<reply> / <draft> / <ready> / <suggestions>` 改为
  `必须使用五个 XML 标签：<reply> / <draft> / <ready> / <start_intent> / <suggestions>`；
  `<ready> 只写 true 或 false...` 之后加一条：

```
- <start_intent> 只写 true 或 false。仅当用户在本轮明确要求立即开始创作（如「开始吧」「可以了，开写」）时填 true；用户只是表达满意但没有要求开始、或是否定语境（「先别开始」）都必须填 false。填 true 时 <ready> 必须同时为 true。
```

(d) splitCoCreateMarkers 签名与实现：

```go
func splitCoCreateMarkers(s string) (reply, draft string, ready, startIntent bool, suggestions []string) {
	reply = extractTagContent(s, tagReply)
	draft = extractTagContent(s, tagDraft)
	readyStr := strings.ToLower(extractTagContent(s, tagReady))
	ready = readyStr == "true" || readyStr == "yes"
	intentStr := strings.ToLower(extractTagContent(s, tagStartIntent))
	startIntent = intentStr == "true" || intentStr == "yes"
	suggestions = parseSuggestions(extractTagContent(s, tagSuggestions))
	return
}
```

parseCoCreateResponse 对应改：

```go
	reply, draft, ready, startIntent, suggestions := splitCoCreateMarkers(raw)
	if reply == "" {
		return CoCreateReply{Message: raw, Prompt: "", Ready: false, Raw: raw}, nil
	}
	return CoCreateReply{
		Message:     reply,
		Prompt:      draft,
		Ready:       ready,
		StartIntent: startIntent,
		Suggestions: suggestions,
		Raw:         raw,
	}, nil
```

(e) extractTagContent 两处已知标签表（236 行开标签表、251 行闭标签表）各加 `"<start_intent>"` / `"</start_intent>"`。

（注意：splitCoCreateMarkers 从 4 返回值变 5 返回值——若 `internal/host/cocreate_test.go` 既有测试直接解构调用它，同步更新解构变量个数。）

(f) coCreateLogEntry 加 `ParsedStartIntent bool json:"parsed_start_intent"`（ParsedReady 之后），defer 填充处加 `ParsedStartIntent: reply.StartIntent,`。

- [ ] **Step 4: 跑测试确认通过（含既有解析测试不回归）**

Run: `go test ./internal/host/ -run "TestParseCoCreate|TestSplitCoCreate|TestExtractTag" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/host/cocreate.go internal/host/events.go internal/host/cocreate_test.go
git commit -m "feat(cocreate): 协议扩五标签——start_intent 开始意图解析与日志落盘

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 9（B）: session 透传 StartIntent

**Files:**
- Modify: `internal/entry/startup/cocreate.go`（CoCreateSession struct 11-18 行；ApplyReply 35 行；AppendUser 63 行）
- Test: `internal/entry/startup/cocreate_test.go`（追加）

- [ ] **Step 1: 写失败测试**

```go
func TestCoCreateSession_StartIntentLifecycle(t *testing.T) {
	s := NewCoCreateSession("写一本仙侠")
	s.ApplyReply(host.CoCreateReply{
		Message: "好的", Prompt: "## 主题", Ready: true, StartIntent: true, Raw: "raw",
	})
	if !s.StartIntent() {
		t.Fatal("ApplyReply 应透传 StartIntent")
	}
	s.AppendUser("再改一下主角名字")
	if s.StartIntent() {
		t.Fatal("用户再次发言后 StartIntent 应作废（同 suggestions）")
	}
	s.ApplyReply(host.CoCreateReply{Message: "好", Prompt: "## 主题", Ready: true, Raw: "raw"})
	if s.StartIntent() {
		t.Fatal("下一轮无意图应覆盖为 false")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/entry/startup/ -run TestCoCreateSession_StartIntentLifecycle -v`
Expected: FAIL（StartIntent 方法未定义）

- [ ] **Step 3: 实现**

struct 加字段 `startIntent bool`（ready 之后）。ApplyReply 中 `s.ready = reply.Ready` 后加：

```go
	// start_intent 每轮覆盖（含覆盖为 false）：意图只对本轮回复有效。
	s.startIntent = reply.StartIntent
```

AppendUser 中 `s.suggestions = nil` 后加：

```go
	s.startIntent = false
```

accessor（Ready() 之后）：

```go
func (s *CoCreateSession) StartIntent() bool {
	if s == nil {
		return false
	}
	return s.startIntent
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/entry/startup/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/entry/startup/cocreate.go internal/entry/startup/cocreate_test.go
git commit -m "feat(startup): 共创 session 透传 start_intent（每轮覆盖/用户发言作废）

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 10（B）: TUI 确认模态 + 启动链路抽取

**Files:**
- Modify: `internal/entry/tui/cocreate.go`（cocreateState 68 行；apply 102 行；placeholderForCoCreate 47-59 行；底栏提示 370 行；渲染函数 renderCoCreateModal）
- Modify: `internal/entry/tui/model.go`（共创按键 switch 704-760 行；Ctrl+S 分支 713 行）
- Test: `internal/entry/tui/cocreate_confirm_test.go`（新建）

- [ ] **Step 1: 写失败测试**

```go
package tui

import (
	"testing"

	"github.com/Accelerator-mzq/ainovel-cli/internal/host"
)

func TestCocreateApply_ConfirmPending(t *testing.T) {
	s := newCoCreateState("写一本仙侠")
	s.apply(host.CoCreateReply{Message: "ok", Prompt: "## 主题", Ready: true, StartIntent: true})
	if !s.confirmPending {
		t.Fatal("intent=true 且草稿非空应弹确认")
	}

	s2 := newCoCreateState("写一本仙侠")
	s2.apply(host.CoCreateReply{Message: "ok", Prompt: "", Ready: true, StartIntent: true})
	if s2.confirmPending {
		t.Fatal("草稿为空（CanStart=false）不应弹确认")
	}

	s3 := newCoCreateState("写一本仙侠")
	s3.apply(host.CoCreateReply{Message: "ok", Prompt: "## 主题", Ready: true})
	if s3.confirmPending {
		t.Fatal("无意图不应弹确认")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/entry/tui/ -run TestCocreateApply_ConfirmPending -v`
Expected: FAIL（confirmPending 未定义）

- [ ] **Step 3: 实现状态与触发**

cocreateState struct 加字段：

```go
	confirmPending bool // start_intent 确认模态打开中（Enter 开写 / Esc 返回）
```

apply 方法改为：

```go
func (s *cocreateState) apply(reply host.CoCreateReply) {
	s.awaiting = false
	s.session.ApplyReply(reply)
	// 用户明确说"开始"且草稿可用 → 弹确认模态；Esc 关闭后本轮不再弹
	//（下一轮回复再带意图会重新置位）。
	s.confirmPending = reply.StartIntent && s.session.CanStart()
}
```

- [ ] **Step 4: 抽取启动链路 + 模态按键**。model.go 共创按键处理：

Ctrl+S 分支（713 行）整段替换为：

```go
	case tea.KeyCtrlS:
		return m.startCoCreation(state)
```

并在该函数（共创按键处理函数）体内最前面、现有 `case tea.KeyEsc: return m.exitCoCreate()` 所在 switch 之前，加模态拦截：

```go
	// start_intent 确认模态打开期间，按键只归模态
	if state.confirmPending {
		switch msg.Type {
		case tea.KeyEnter:
			state.confirmPending = false
			return m.startCoCreation(state)
		case tea.KeyEsc:
			state.confirmPending = false
			return m, nil
		}
		return m, nil
	}
```

新增方法（与共创按键处理函数同文件、相邻位置）：

```go
// startCoCreation 以当前草稿进入正式创作——Ctrl+S 与开始意图确认模态共用，
// 启动链路单一来源（buildPlan → startRuntime，含共创原文保全）。
func (m Model) startCoCreation(state *cocreateState) (tea.Model, tea.Cmd) {
	if state.awaiting || !state.canStart() {
		return m, nil
	}
	plan, err := state.buildPlan()
	if err != nil {
		m.err = err
		return m, nil
	}
	state.awaiting = true
	m.textarea.Blur()
	return m, startRuntime(m.runtime, plan)
}
```

（接收者类型 `Model`/指针按该文件相邻方法的实际写法对齐。原 Ctrl+S 分支里的 awaiting/canStart 守卫已收进方法，行为等价。）

- [ ] **Step 5: 模态渲染**。在 cocreate.go 的渲染函数 `renderCoCreateModal`（grep 定位其 return 处）中，最终返回前加 confirmPending 覆盖层，复用 `renderPaddedModalFrame` + `lipgloss.Place`（样板：`internal/entry/tui/command_help.go:67` 的 renderHelpModal）：

```go
// renderStartConfirmOverlay 开始意图确认模态：盖在共创视图之上居中显示。
func renderStartConfirmOverlay(width, height int) string {
	lines := []string{
		"",
		"检测到开始意图",
		"即将以右侧草稿进入正式创作",
		"",
		"[Enter] 确认开写 · [Esc] 返回继续共创",
		"",
	}
	modal := renderPaddedModalFrame(46, len(lines)+4, "开始创作确认", "", lines)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}
```

renderCoCreateModal 最终 return 前加：

```go
	if state.confirmPending {
		return renderStartConfirmOverlay(width, height)
	}
```

（renderPaddedModalFrame 的参数形态以 command_help.go:82 实际签名为准——`(boxW, boxH, title, footer, lines)`；宽 46 容纳最长行，footer 传空串。变量名 `state`/`width`/`height` 按 renderCoCreateModal 实际形参对齐。）

- [ ] **Step 6: ready 提示升级**。cocreate.go 55 行：

```go
	case state.canStart():
		return "✨ 信息已足够 · Ctrl+S 或对 AI 说「开始」即可进入创作"
```

（370 行底栏快捷键提示已含 Ctrl+S，不动。）

- [ ] **Step 7: 跑测试 + 编译**

Run: `go build ./... && go test ./internal/entry/tui/ -v -run TestCocreate`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/entry/tui/
git commit -m "feat(tui): 共创开始意图确认模态（Enter 开写/Esc 返回）+ 启动链路抽取共用

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 11: 文档 + 配置示例 + 全量验证

**Files:**
- Modify: `config.example.jsonc`（budget 段附近）
- Modify: `docs/user-guide.md`（2.2 节 96-106 行；2.3 节 107 行；共创表格 61 行；FAQ 397 行后）

- [ ] **Step 1: config.example.jsonc** budget 配置段附近加：

```jsonc
  // 规划完成后暂停等待你审阅大纲再开写："auto"（TUI 开/headless 关，默认）、"on"、"off"
  "plan_review": "auto",
```

- [ ] **Step 2: user-guide.md 四处**

(a) 2.2 节"创作过程中会发生什么"的节奏列表，规划期（第 1 条）与写作期（第 2 条）之间插入：

```markdown
1.5. **大纲审阅**（TUI 默认开启）：规划完成后系统自动暂停，提示你查看
   `output/{书名}/layered_outline.md`。输入修改意见会调整大纲后再次暂停，
   输入「开始」（或「确认」「开写」「开始写作」）进入正式写作。
   headless 默认跳过此环节；配置 `plan_review: "on"/"off"` 可覆盖两端。
```

（编号按 Markdown 实际渲染调整为插入后重排 1-5。）

(b) 2.3 节开头补一句：

```markdown
> 规划刚完成的"待审阅"暂停态是特例：此时输入修改意见会循环再审，输入「开始」才进入写作（见 2.2）。
```

(c) 2.1 启动表格共创行，`满意后 Ctrl+S 进入正式创作` 改为 `满意后 Ctrl+S 或直接对 AI 说「开始」进入正式创作`。

(d) FAQ 新增一条：

```markdown
**问：我不想每次开书都确认大纲，怎么跳过？**
配置 `plan_review: "off"`。反之 headless 也想审阅则配 `"on"`（管道关闭时自动确认，不会卡死自动化）。
```

- [ ] **Step 3: 全量验证**

Run: `go build ./... && go test ./...`
Expected: 全部 ok（与 main 基线一致，无新增失败）

- [ ] **Step 4: Commit**

```bash
git add config.example.jsonc docs/user-guide.md
git commit -m "docs: 规划审阅与共创「开始」确认使用说明 + plan_review 配置示例

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 12: 端到端手动冒烟（可选但推荐）

- [ ] TUI 冒烟：临时配置便宜模型，`ainovel-cli` 快速开始一句话需求 → 等规划完成 → 确认出现"待审阅"暂停与专属 placeholder → 输入一条修改意见 → 确认大纲调整后再次暂停 → 输入「开始」→ 确认进入写作 → Ctrl+C 退出。
- [ ] 共创冒烟：共创模式聊 2 轮 → 说"可以了，开始写吧" → 确认弹模态 → Esc 返回 → 再说"开始吧" → Enter 确认 → 进入创作。
- [ ] 证据：截图或事件日志片段附在 PR 描述。

---

## 完成后

走 `superpowers:finishing-a-development-branch`：push 分支 + `gh pr create --repo Accelerator-mzq/ainovel-cli --base main`（两个参数都必须显式给——PR#5 的 base 事故教训）。
