package persona

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
	"github.com/Accelerator-mzq/ainovel-cli/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(filepath.Join(t.TempDir(), "novel"))
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	return st
}

func seedPersonaProfile(t *testing.T, st *store.Store, author, updatedAt string) {
	t.Helper()
	m, err := st.Simulation.LoadPersonaProfiles()
	if err != nil {
		t.Fatal(err)
	}
	m[author] = domain.SimulationProfile{
		Version:   domain.SimulationProfileVersion,
		UpdatedAt: updatedAt,
		Synthesis: domain.SimulationSynthesis{
			Style: domain.SimulationStyle{NarrativeVoice: []string{author + " voice"}},
		},
	}
	if err := st.Simulation.SavePersonaProfiles(m); err != nil {
		t.Fatal(err)
	}
}

func seedBaseProfile(t *testing.T, st *store.Store, updatedAt string) {
	t.Helper()
	if err := st.Simulation.Save(domain.SimulationProfile{
		Version:   domain.SimulationProfileVersion,
		UpdatedAt: updatedAt,
		Synthesis: domain.SimulationSynthesis{
			Style: domain.SimulationStyle{NarrativeVoice: []string{"base voice"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

// countingFuse 返回固定 synthesis 并计数；failErr 非 nil 时模拟融合失败。
func countingFuse(calls *int, failErr error) FuseFunc {
	return func(_ context.Context, _, persona *domain.SimulationProfile) (*domain.SimulationSynthesis, error) {
		*calls++
		if failErr != nil {
			return nil, failErr
		}
		return &domain.SimulationSynthesis{
			Style: domain.SimulationStyle{NarrativeVoice: []string{"fused:" + persona.Synthesis.Style.NarrativeVoice[0]}},
		}, nil
	}
}

func TestMissingProfiles(t *testing.T) {
	st := newTestStore(t)
	seedPersonaProfile(t, st, "乌贼", "t1")
	missing, err := MissingProfiles(st, []string{"乌贼", "肘子"})
	if err != nil {
		t.Fatalf("MissingProfiles: %v", err)
	}
	if len(missing) != 1 || missing[0] != "肘子" {
		t.Fatalf("missing = %v, want [肘子]", missing)
	}
	// AvailableProfiles 用于缺失告警时提示拼写错位
	available, err := AvailableProfiles(st)
	if err != nil {
		t.Fatalf("AvailableProfiles: %v", err)
	}
	if len(available) != 1 || available[0] != "乌贼" {
		t.Fatalf("available = %v, want [乌贼]", available)
	}
}

// 有主画像：首跑融合并缓存；重跑 0 次融合；主画像更新后缓存失效重融合。
func TestEnsureFusedCachesAndInvalidates(t *testing.T) {
	st := newTestStore(t)
	seedBaseProfile(t, st, "base-t1")
	seedPersonaProfile(t, st, "乌贼", "p-t1")
	seedPersonaProfile(t, st, "肘子", "p-t1")
	authors := []string{"乌贼", "肘子"}

	calls := 0
	out, err := EnsureFused(context.Background(), st, authors, countingFuse(&calls, nil))
	if err != nil {
		t.Fatalf("EnsureFused: %v", err)
	}
	if calls != 2 || len(out) != 2 {
		t.Fatalf("首跑融合调用 = %d（want 2），结果 %d 个", calls, len(out))
	}
	// slug 必须按 index 与 Slugs() 一致（中文名 → persona{N}），防张冠李戴
	if out[0].Slug != "persona1" || out[1].Slug != "persona2" {
		t.Fatalf("slug 错位: %q / %q", out[0].Slug, out[1].Slug)
	}
	if out[0].Profile.Synthesis.Style.NarrativeVoice[0] != "fused:乌贼 voice" {
		t.Fatalf("融合结果未生效: %+v", out[0].Profile.Synthesis.Style)
	}

	// 缓存命中：0 次融合
	calls = 0
	if _, err := EnsureFused(context.Background(), st, authors, countingFuse(&calls, nil)); err != nil {
		t.Fatalf("二次 EnsureFused: %v", err)
	}
	if calls != 0 {
		t.Fatalf("缓存命中仍融合了 %d 次", calls)
	}

	// 主画像更新 → 全部失效重融合
	seedBaseProfile(t, st, "base-t2")
	calls = 0
	if _, err := EnsureFused(context.Background(), st, authors, countingFuse(&calls, nil)); err != nil {
		t.Fatalf("主画像更新后 EnsureFused: %v", err)
	}
	if calls != 2 {
		t.Fatalf("主画像更新后融合调用 = %d, want 2", calls)
	}

	// 人格画像更新 → 仅该条目失效重融合（对称覆盖 PersonaStamp 分支 + 部分失效语义）
	seedPersonaProfile(t, st, "乌贼", "p-t2")
	calls = 0
	if _, err := EnsureFused(context.Background(), st, authors, countingFuse(&calls, nil)); err != nil {
		t.Fatalf("人格画像更新后 EnsureFused: %v", err)
	}
	if calls != 1 {
		t.Fatalf("人格画像更新后融合调用 = %d, want 1（仅乌贼失效）", calls)
	}
}

// ctx 取消/超时：fuse 立即失败属于"从未真正尝试"，不得把 Fallback 持久化进缓存
// （否则污染缓存与日志）；但 out 必须仍包含全部 authors，保证 build.go 注册的
// writer 数与 host.go SetContest 的 slug 数对齐。
func TestEnsureFusedCtxCanceledSkipsCacheWrite(t *testing.T) {
	st := newTestStore(t)
	seedBaseProfile(t, st, "base-t1")
	seedPersonaProfile(t, st, "乌贼", "p-t1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 模拟启动期超时/取消：fuse 立即返回 ctx.Err()
	out, err := EnsureFused(ctx, st, []string{"乌贼"}, func(ctx context.Context, _, _ *domain.SimulationProfile) (*domain.SimulationSynthesis, error) {
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("ctx 取消应兜底不阻断: %v", err)
	}
	// out 完整：长度 1 且标记 Fallback 兜底
	if len(out) != 1 || !out[0].Fallback {
		t.Fatalf("out 应包含全部 authors 且标记 Fallback: %+v", out)
	}
	// 缓存无写入："未真正尝试"不得持久化为"融合失败"
	cache, cerr := st.Simulation.LoadFusedProfiles()
	if cerr != nil {
		t.Fatalf("LoadFusedProfiles: %v", cerr)
	}
	if len(cache) != 0 {
		t.Fatalf("ctx 取消不应写缓存，得到 %d 条: %+v", len(cache), cache)
	}
}

// 无主画像：退化为人格画像本身，不调融合。
func TestEnsureFusedNoBaseUsesPersonaProfile(t *testing.T) {
	st := newTestStore(t)
	seedPersonaProfile(t, st, "乌贼", "p-t1")

	calls := 0
	out, err := EnsureFused(context.Background(), st, []string{"乌贼"}, countingFuse(&calls, nil))
	if err != nil {
		t.Fatalf("EnsureFused: %v", err)
	}
	if calls != 0 {
		t.Fatalf("无主画像不应调融合，calls = %d", calls)
	}
	if out[0].Profile.Synthesis.Style.NarrativeVoice[0] != "乌贼 voice" {
		t.Fatalf("应退化为人格画像原样: %+v", out[0].Profile.Synthesis.Style)
	}
}

// 融合失败：人格画像兜底 + Fallback 标记；下次启动重试（Fallback 缓存视为无效）。
func TestEnsureFusedFallbackRetries(t *testing.T) {
	st := newTestStore(t)
	seedBaseProfile(t, st, "base-t1")
	seedPersonaProfile(t, st, "乌贼", "p-t1")

	calls := 0
	out, err := EnsureFused(context.Background(), st, []string{"乌贼"}, countingFuse(&calls, fmt.Errorf("llm down")))
	if err != nil {
		t.Fatalf("融合失败不应返回 error（兜底不阻断）: %v", err)
	}
	if !out[0].Fallback {
		t.Fatal("融合失败应标记 Fallback")
	}
	if out[0].Profile.Synthesis.Style.NarrativeVoice[0] != "乌贼 voice" {
		t.Fatalf("兜底应为人格画像原样: %+v", out[0].Profile.Synthesis.Style)
	}

	// 重试：Fallback 缓存无效 → 再次调融合，成功后覆盖
	calls = 0
	out, err = EnsureFused(context.Background(), st, []string{"乌贼"}, countingFuse(&calls, nil))
	if err != nil {
		t.Fatalf("重试 EnsureFused: %v", err)
	}
	if calls != 1 {
		t.Fatalf("Fallback 缓存应触发重试，calls = %d, want 1", calls)
	}
	if out[0].Fallback || out[0].Profile.Synthesis.Style.NarrativeVoice[0] != "fused:乌贼 voice" {
		t.Fatalf("重试成功后应为融合结果: %+v", out[0])
	}
}
