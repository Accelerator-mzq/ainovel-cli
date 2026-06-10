package assets

import (
	"strings"
	"testing"
)

// 本文件验证「仿写画像 simulation」与「多人格竞稿」两功能同时开启时的集成正确性。
// StyleBlock 旧机制已删除，竞稿 writer 与普通 writer 共用同一 system prompt，
// 文风信号唯一来源是 novel_context 注入的融合画像。

// simulationGuidance 的稳定文本锚点（见 load.go const simulationGuidance）。
const simGuidanceAnchor = "## 仿写画像"

// TestSimulationGuidanceInjectedPerRole 确认 simulation 指导按预期注入各角色，
// 且竞稿裁判 Judge 被刻意排除（合并决策：withSimulationGuidance 的 role 参数无 judge）。
func TestSimulationGuidanceInjectedPerRole(t *testing.T) {
	b := Load("default")

	// 写作/规划角色应被注入画像指导。
	inject := map[string]string{
		"Coordinator":    b.Prompts.Coordinator,
		"ArchitectShort": b.Prompts.ArchitectShort,
		"ArchitectLong":  b.Prompts.ArchitectLong,
		"Writer":         b.Prompts.Writer,
		"Editor":         b.Prompts.Editor,
	}
	for role, p := range inject {
		if !strings.Contains(p, simGuidanceAnchor) {
			t.Errorf("角色 %s 的 prompt 应含仿写画像指导，实际未注入", role)
		}
	}

	// Judge（竞稿裁判）刻意未注入画像指导——评判候选稿，不直接产出文风。
	if strings.Contains(b.Prompts.Judge, simGuidanceAnchor) {
		t.Error("Judge 不应被注入仿写画像指导（合并时刻意排除），但实际含有")
	}
}

// TestContestWriterSingleStyleSignal 验证 StyleBlock 机制删除后的最终态：
// 竞稿写手与普通 writer 共用同一 system prompt（含"## 仿写画像"指导段，
// 运行期文风信号唯一来源是 novel_context 注入的融合画像），不再有
// "## 你的写作人格" prompt 拼接。融合 prompt 资产必须可加载。
func TestContestWriterSingleStyleSignal(t *testing.T) {
	b := Load("default")

	if !strings.Contains(b.Prompts.Writer, simGuidanceAnchor) {
		t.Error("writer prompt 应含仿写画像指导段（竞稿写手复用同一 prompt）")
	}
	if strings.Contains(b.Prompts.Writer, "## 你的写作人格") {
		t.Error("StyleBlock 人格块机制已删除，writer prompt 不应再含人格块标记")
	}
	if strings.TrimSpace(b.Prompts.SimulationPersonaFuse) == "" {
		t.Error("simulation-persona-fuse prompt 应非空")
	}
}
