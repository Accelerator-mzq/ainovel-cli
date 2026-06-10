package store

import (
	"os"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
)

type SimulationStore struct{ io *IO }

func NewSimulationStore(io *IO) *SimulationStore { return &SimulationStore{io: io} }

func (s *SimulationStore) Load() (*domain.SimulationProfile, error) {
	var profile domain.SimulationProfile
	if err := s.io.ReadJSON("meta/simulation_profile.json", &profile); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidateSimulationProfile(&profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (s *SimulationStore) Save(profile domain.SimulationProfile) error {
	if profile.Version == "" {
		profile.Version = domain.SimulationProfileVersion
	}
	if err := domain.ValidateSimulationProfile(&profile); err != nil {
		return err
	}
	return s.io.WriteJSON("meta/simulation_profile.json", profile)
}

// LoadPersonaProfiles 读取竞稿人格画像集合（key=作者名）；文件不存在返回空 map。
// key 用作者名而非 slug：中文名 slug 是 index 相关的 persona{N}，重排配置会错位。
func (s *SimulationStore) LoadPersonaProfiles() (map[string]domain.SimulationProfile, error) {
	m := make(map[string]domain.SimulationProfile)
	if err := s.io.ReadJSON("meta/simulation_personas.json", &m); err != nil {
		if os.IsNotExist(err) {
			return map[string]domain.SimulationProfile{}, nil
		}
		return nil, err
	}
	return m, nil
}

// SavePersonaProfiles 全量写回人格画像集合。
func (s *SimulationStore) SavePersonaProfiles(m map[string]domain.SimulationProfile) error {
	return s.io.WriteJSON("meta/simulation_personas.json", m)
}

// LoadFusedProfiles 读取竞稿融合画像缓存（key=作者名）；文件不存在返回空 map。
func (s *SimulationStore) LoadFusedProfiles() (map[string]domain.FusedPersonaProfile, error) {
	m := make(map[string]domain.FusedPersonaProfile)
	if err := s.io.ReadJSON("meta/contest_fused_profiles.json", &m); err != nil {
		if os.IsNotExist(err) {
			return map[string]domain.FusedPersonaProfile{}, nil
		}
		return nil, err
	}
	return m, nil
}

// SaveFusedProfiles 全量写回融合画像缓存。
func (s *SimulationStore) SaveFusedProfiles(m map[string]domain.FusedPersonaProfile) error {
	return s.io.WriteJSON("meta/contest_fused_profiles.json", m)
}
