// internal/store/contest_test.go
package store

import "testing"

// 注：store 包内已有 NewStore(dir) *Store（单返回值，无 error），见 cast_test.go:11。

func TestContest_CandidateRoundTrip(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Contest.SaveCandidate(3, "wuzei", "乌贼的第三章"); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	got, err := st.Contest.LoadCandidate(3, "wuzei")
	if err != nil {
		t.Fatalf("LoadCandidate: %v", err)
	}
	if got != "乌贼的第三章" {
		t.Fatalf("LoadCandidate = %q", got)
	}
}

func TestContest_ListCandidates(t *testing.T) {
	st := NewStore(t.TempDir())
	_ = st.Contest.SaveCandidate(5, "wuzei", "a")
	_ = st.Contest.SaveCandidate(5, "tudou", "b")
	got, err := st.Contest.ListCandidates(5, []string{"wuzei", "tudou", "maibao"})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("presence map size = %d, want 3", len(got))
	}
	if !got["wuzei"] || !got["tudou"] || got["maibao"] {
		t.Fatalf("presence map wrong: %v", got)
	}
}
