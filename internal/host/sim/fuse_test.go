package sim

import (
	"context"
	"testing"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
)

// minimalProfile 构造只含 NarrativeVoice 的最小 SimulationProfile，用于融合测试。
func minimalProfile(voice string) *domain.SimulationProfile {
	return &domain.SimulationProfile{
		Version: domain.SimulationProfileVersion,
		Synthesis: domain.SimulationSynthesis{
			Style: domain.SimulationStyle{NarrativeVoice: []string{voice}},
		},
	}
}

// TestFuseSynthesisParsesLLMOutput 验证 FuseSynthesis 正常路径：
// LLM 返回合法 JSON → 解析为 synthesis，且恰好调用 1 次 LLM。
func TestFuseSynthesisParsesLLMOutput(t *testing.T) {
	llm := &scriptedLLM{responses: []string{validSynthesisJSON("fused texture")}}
	syn, err := FuseSynthesis(context.Background(), llm, "fuse prompt", minimalProfile("base voice"), minimalProfile("persona voice"))
	if err != nil {
		t.Fatalf("FuseSynthesis: %v", err)
	}
	if len(syn.Style.ProseTexture) == 0 || syn.Style.ProseTexture[0] != "fused texture" {
		t.Fatalf("融合 synthesis 解析错误: %+v", syn.Style)
	}
	if got := llm.calls.Load(); got != 1 {
		t.Fatalf("LLM 调用 = %d, want 1", got)
	}
}

// TestFuseSynthesisRequiresInputs 验证缺少必填参数时 FuseSynthesis 报错。
func TestFuseSynthesisRequiresInputs(t *testing.T) {
	llm := &scriptedLLM{}
	if _, err := FuseSynthesis(context.Background(), llm, "", minimalProfile("a"), minimalProfile("b")); err == nil {
		t.Error("空 prompt 应报错")
	}
	if _, err := FuseSynthesis(context.Background(), llm, "p", nil, minimalProfile("b")); err == nil {
		t.Error("nil base 应报错（无主画像时调用方直接退化，不应进入融合）")
	}
	if _, err := FuseSynthesis(context.Background(), llm, "p", minimalProfile("a"), nil); err == nil {
		t.Error("nil persona 应报错")
	}
}
