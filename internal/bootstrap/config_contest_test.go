package bootstrap

import "testing"

func TestWritingContest_Normalize_DedupTrim(t *testing.T) {
	wc := WritingContest{Personas: []string{" 乌贼 ", "土豆", "乌贼", "", "  "}}
	got := wc.Normalize()
	want := []string{"乌贼", "土豆"}
	if len(got.Personas) != len(want) {
		t.Fatalf("personas = %v, want %v", got.Personas, want)
	}
	for i := range want {
		if got.Personas[i] != want[i] {
			t.Fatalf("personas[%d] = %q, want %q", i, got.Personas[i], want[i])
		}
	}
}

func TestWritingContest_Enabled(t *testing.T) {
	if (WritingContest{}).Enabled() {
		t.Fatal("空配置应为未启用")
	}
	if !(WritingContest{Personas: []string{"乌贼", "土豆"}}).Enabled() {
		t.Fatal("两个 persona 应为启用")
	}
	if (WritingContest{Personas: []string{"乌贼"}}).Normalize().Enabled() {
		t.Fatal("单 persona 不应启用竞稿")
	}
}
