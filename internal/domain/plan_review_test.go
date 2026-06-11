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
