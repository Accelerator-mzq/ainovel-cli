package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
	"github.com/Accelerator-mzq/ainovel-cli/internal/rules"
	"github.com/Accelerator-mzq/ainovel-cli/internal/store"
)

func TestContextToolInjectsCompactSimulationProfile(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	profile := domain.SimulationProfile{
		Version: domain.SimulationProfileVersion,
		Corpus: domain.SimulationCorpusManifest{
			Sources: []domain.SimulationSource{{
				RelativePath: "a.txt",
				SHA256:       "sha-a",
				Fingerprint:  domain.SimulationSourceFingerprint("a.txt", "sha-a"),
			}},
		},
		SourceReports: []domain.SimulationSourceReport{{
			RelativePath: "a.txt",
			SHA256:       "sha-a",
			Fingerprint:  domain.SimulationSourceFingerprint("a.txt", "sha-a"),
			Summary:      "full report should not be injected",
		}},
		Synthesis: domain.SimulationSynthesis{
			Style: domain.SimulationStyle{
				NarrativeVoice: []string{"close third"},
			},
			RoleGuidance: domain.SimulationRoleGuidance{
				Coordinator: []string{"keep tasks aligned"},
				Architect:   []string{"escalate costs"},
				Writer:      []string{"borrow technique only"},
				Editor:      []string{"check non-copying"},
			},
		},
	}
	if err := st.Simulation.Save(profile); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Start", CoreEvent: "Begin"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 1); err != nil {
		t.Fatal(err)
	}

	tool := NewContextTool(st, References{}, "default", rules.LoadOptions{})
	architectRaw, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("architect Execute: %v", err)
	}
	var architect map[string]any
	if err := json.Unmarshal(architectRaw, &architect); err != nil {
		t.Fatal(err)
	}
	assertCompactSimulationProfile(t, architect, "planning_memory")

	chapterRaw, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("chapter Execute: %v", err)
	}
	var chapter map[string]any
	if err := json.Unmarshal(chapterRaw, &chapter); err != nil {
		t.Fatal(err)
	}
	assertCompactSimulationProfile(t, chapter, "working_memory")
}

func assertCompactSimulationProfile(t *testing.T, payload map[string]any, section string) {
	t.Helper()
	if got := payload["simulation_profile"]; got != true {
		t.Fatalf("expected top-level simulation_profile marker, got %#v", got)
	}
	sectionMap, ok := payload[section].(map[string]any)
	if !ok {
		t.Fatalf("expected %s", section)
	}
	compact, ok := sectionMap["simulation_profile"].(map[string]any)
	if !ok {
		t.Fatalf("expected simulation_profile under %s", section)
	}
	if _, exists := compact["source_reports"]; exists {
		t.Fatal("compact simulation_profile must not include source_reports")
	}
	if got := compact["source_count"]; got != float64(1) {
		t.Fatalf("source_count = %v, want 1", got)
	}
}

// 竞稿写手的专属 ContextTool 必须返回注入的融合画像，且不影响共享实例（防串台）。
func TestContextToolWithProfileSourceOverrides(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	// store 里放主画像
	if err := st.Simulation.Save(domain.SimulationProfile{
		Version: domain.SimulationProfileVersion,
		Synthesis: domain.SimulationSynthesis{
			Style: domain.SimulationStyle{NarrativeVoice: []string{"base voice"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Start", CoreEvent: "Begin"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 1); err != nil {
		t.Fatal(err)
	}

	fused := &domain.SimulationProfile{
		Version: domain.SimulationProfileVersion,
		Synthesis: domain.SimulationSynthesis{
			Style: domain.SimulationStyle{NarrativeVoice: []string{"fused persona voice"}},
		},
	}
	shared := NewContextTool(st, References{}, "default", rules.LoadOptions{})
	personaTool := shared.WithProfileSource(func() (*domain.SimulationProfile, error) { return fused, nil })

	voiceOf := func(tool *ContextTool) string {
		t.Helper()
		raw, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		section := payload["working_memory"].(map[string]any)
		compact := section["simulation_profile"].(map[string]any)
		style := compact["style"].(map[string]any)
		voices := style["narrative_voice"].([]any)
		return voices[0].(string)
	}

	if got := voiceOf(personaTool); got != "fused persona voice" {
		t.Fatalf("persona 实例应返回融合画像, got %q", got)
	}
	if got := voiceOf(shared); got != "base voice" {
		t.Fatalf("共享实例应仍返回主画像（不串台）, got %q", got)
	}
}
