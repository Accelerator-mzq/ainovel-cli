package host

import (
	"testing"
	"time"
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
	select {
	case <-aborted:
	case <-time.After(2 * time.Second):
		t.Fatal("abort 未触发")
	}
	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("notify 未触发")
	}
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

func TestPlanReviewGuard_NotPending(t *testing.T) {
	// 回调可能在 go 起的非测试 goroutine 里执行，不能 t.Fatal（FailNow 仅限测试 goroutine）
	g := newPlanReviewGuard(func() bool { return false },
		func(Event) { t.Error("不应发事件") }, func() { t.Error("不应 abort") }, nil)
	if !g.Allow() {
		t.Fatal("非 pending 应放行")
	}
}
