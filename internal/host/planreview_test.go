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
