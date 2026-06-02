package flow

import (
	"testing"

	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

func TestLoadState_ContestFacts(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	s := LoadStateWithContest(st, ContestConfig{Personas: []string{"wuzei", "tudou"}})
	if !s.ContestEnabled {
		t.Fatal("配置两 persona 应 ContestEnabled=true")
	}
	if len(s.Personas) != 2 {
		t.Fatalf("personas = %v", s.Personas)
	}
}

func TestLoadState_NoContestWhenSinglePersona(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	s := LoadStateWithContest(st, ContestConfig{Personas: []string{"wuzei"}})
	if s.ContestEnabled {
		t.Fatal("单 persona 不应启用竞稿")
	}
}

func TestLoadState_NoContestWhenEmpty(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	s := LoadStateWithContest(st, ContestConfig{})
	if s.ContestEnabled {
		t.Fatal("无 persona 不应启用竞稿")
	}
}
