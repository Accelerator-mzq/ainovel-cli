package store

import (
	"path/filepath"
	"testing"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
)

// 人格画像与融合缓存的存取往返：写入后读回一致；文件不存在返回空 map 而非错误。
func TestPersonaProfilesRoundTrip(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "novel"))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}

	// 不存在 → 空 map
	got, err := st.Simulation.LoadPersonaProfiles()
	if err != nil {
		t.Fatalf("LoadPersonaProfiles: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("初始应为空 map，got %d 项", len(got))
	}

	in := map[string]domain.SimulationProfile{
		"乌贼": {
			Version:   domain.SimulationProfileVersion,
			UpdatedAt: "2026-06-10T00:00:00Z",
			Synthesis: domain.SimulationSynthesis{
				Style: domain.SimulationStyle{NarrativeVoice: []string{"阴郁第三人称"}},
			},
		},
	}
	if err := st.Simulation.SavePersonaProfiles(in); err != nil {
		t.Fatalf("SavePersonaProfiles: %v", err)
	}
	got, err = st.Simulation.LoadPersonaProfiles()
	if err != nil {
		t.Fatalf("LoadPersonaProfiles after save: %v", err)
	}
	if got["乌贼"].UpdatedAt != "2026-06-10T00:00:00Z" {
		t.Fatalf("读回 UpdatedAt 不一致: %+v", got["乌贼"])
	}
	if got["乌贼"].Synthesis.Style.NarrativeVoice[0] != "阴郁第三人称" {
		t.Fatalf("读回 synthesis 不一致: %+v", got["乌贼"])
	}
}

func TestFusedProfilesRoundTrip(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "novel"))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}

	got, err := st.Simulation.LoadFusedProfiles()
	if err != nil {
		t.Fatalf("LoadFusedProfiles: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("初始应为空 map，got %d 项", len(got))
	}

	in := map[string]domain.FusedPersonaProfile{
		"乌贼": {
			BaseStamp:    "base-1",
			PersonaStamp: "p-1",
			Fallback:     true,
			Profile:      domain.SimulationProfile{Version: domain.SimulationProfileVersion},
		},
	}
	if err := st.Simulation.SaveFusedProfiles(in); err != nil {
		t.Fatalf("SaveFusedProfiles: %v", err)
	}
	got, err = st.Simulation.LoadFusedProfiles()
	if err != nil {
		t.Fatalf("LoadFusedProfiles after save: %v", err)
	}
	entry := got["乌贼"]
	if entry.BaseStamp != "base-1" || entry.PersonaStamp != "p-1" || !entry.Fallback {
		t.Fatalf("读回融合缓存不一致: %+v", entry)
	}
}
