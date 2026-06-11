package tui

import (
	"testing"

	"github.com/Accelerator-mzq/ainovel-cli/internal/host"
)

// TestCocreateApply_ConfirmPending 验证 start_intent 确认模态的触发条件：
//   - 意图为真且草稿非空（CanStart）→ 弹确认模态
//   - 草稿为空 → 不弹（无法开始创作）
//   - 无意图 → 不弹
func TestCocreateApply_ConfirmPending(t *testing.T) {
	s := newCoCreateState("写一本仙侠")
	s.apply(host.CoCreateReply{Message: "ok", Prompt: "## 主题", Ready: true, StartIntent: true})
	if !s.confirmPending {
		t.Fatal("intent=true 且草稿非空应弹确认")
	}

	s2 := newCoCreateState("写一本仙侠")
	s2.apply(host.CoCreateReply{Message: "ok", Prompt: "", Ready: true, StartIntent: true})
	if s2.confirmPending {
		t.Fatal("草稿为空（CanStart=false）不应弹确认")
	}

	s3 := newCoCreateState("写一本仙侠")
	s3.apply(host.CoCreateReply{Message: "ok", Prompt: "## 主题", Ready: true})
	if s3.confirmPending {
		t.Fatal("无意图不应弹确认")
	}
}
