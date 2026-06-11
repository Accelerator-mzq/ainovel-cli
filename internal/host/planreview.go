package host

import (
	"strings"
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

// 规划审阅确认词：TrimSpace 后精确匹配，避免"不要开始"之类误判。
var planReviewConfirmWords = map[string]bool{
	"开始": true, "确认": true, "开写": true, "开始写作": true,
}

// IsPlanReviewConfirm 报告文本是否为规划审阅确认词。
func IsPlanReviewConfirm(text string) bool {
	return planReviewConfirmWords[strings.TrimSpace(text)]
}
