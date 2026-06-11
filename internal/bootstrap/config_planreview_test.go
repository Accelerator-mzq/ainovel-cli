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
