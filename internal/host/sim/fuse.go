package sim

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
	"github.com/voocel/agentcore"
)

// FuseSynthesis 把"主画像基底 + 人格画像变奏"融合为一份 synthesis，
// 供竞稿写手作为唯一文风信号。融合规则在 systemPrompt
// （assets/prompts/simulation-persona-fuse.md）中声明：人格主导风格层、主画像主导结构层。
// 无主画像时调用方应直接用人格画像退化，不要调本函数。
func FuseSynthesis(ctx context.Context, llm LLMChat, systemPrompt string, base, persona *domain.SimulationProfile) (*domain.SimulationSynthesis, error) {
	// 校验必填参数
	if strings.TrimSpace(systemPrompt) == "" {
		return nil, fmt.Errorf("fuse prompt is required")
	}
	if base == nil || persona == nil {
		return nil, fmt.Errorf("fuse requires both base and persona profiles")
	}

	// 调用 LLM 完成融合
	resp, err := llm.Generate(ctx, []agentcore.Message{
		agentcore.SystemMsg(systemPrompt),
		agentcore.UserMsg(buildFuseUserPrompt(base, persona)),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("llm fuse profile: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("llm fuse profile: nil response")
	}

	// 解析 LLM 返回的 synthesis JSON
	var synthesis domain.SimulationSynthesis
	if err := parseJSONPayload(resp.Message.TextContent(), &synthesis); err != nil {
		return nil, fmt.Errorf("parse fused synthesis: %w", err)
	}
	return &synthesis, nil
}

// buildFuseUserPrompt 构建融合请求的 user 消息：
// 将 base 与 persona 的 compact 画像序列化为 JSON，交给 LLM 按 system 规则融合。
func buildFuseUserPrompt(base, persona *domain.SimulationProfile) string {
	payload := map[string]any{
		"base_profile":    domain.CompactSimulationProfile(base),
		"persona_profile": domain.CompactSimulationProfile(persona),
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return "Fuse the persona profile onto the base profile. Return only the requested JSON object.\n\n" + string(data)
}
