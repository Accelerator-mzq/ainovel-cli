package startup

import (
	"strings"
	"testing"

	"github.com/Accelerator-mzq/ainovel-cli/internal/host"
)

// TestUserTranscript 验证 UserTranscript 只提取 user 消息并附轮次头。
func TestUserTranscript(t *testing.T) {
	s := &CoCreateSession{
		history: []host.CoCreateMessage{
			{Role: "user", Content: "我想写一个修仙小说"},
			{Role: "assistant", Content: "好的，请告诉我更多细节"},
			{Role: "user", Content: "主角叫林尘，练气期开始"},
			{Role: "assistant", Content: "了解，我来整理草稿"},
		},
	}

	got := s.UserTranscript()

	// 应包含两条用户输入
	if !strings.Contains(got, "### 用户输入 1") {
		t.Errorf("缺少用户输入 1 标头：%q", got)
	}
	if !strings.Contains(got, "我想写一个修仙小说") {
		t.Errorf("缺少第一条用户消息内容：%q", got)
	}
	if !strings.Contains(got, "### 用户输入 2") {
		t.Errorf("缺少用户输入 2 标头：%q", got)
	}
	if !strings.Contains(got, "主角叫林尘") {
		t.Errorf("缺少第二条用户消息内容：%q", got)
	}
	// 不应包含 assistant 内容
	if strings.Contains(got, "好的，请告诉我更多细节") {
		t.Errorf("不应包含 assistant 消息：%q", got)
	}
	if strings.Contains(got, "了解，我来整理草稿") {
		t.Errorf("不应包含 assistant 消息：%q", got)
	}
}

// TestUserTranscript_NilSession 验证空 session 安全返回空串。
func TestUserTranscript_NilSession(t *testing.T) {
	var s *CoCreateSession
	if got := s.UserTranscript(); got != "" {
		t.Errorf("nil session 应返回空串，got %q", got)
	}
}

func TestCoCreateSession_StartIntentLifecycle(t *testing.T) {
	s := NewCoCreateSession("写一本仙侠")
	s.ApplyReply(host.CoCreateReply{
		Message: "好的", Prompt: "## 主题", Ready: true, StartIntent: true, Raw: "raw",
	})
	if !s.StartIntent() {
		t.Fatal("ApplyReply 应透传 StartIntent")
	}
	s.AppendUser("再改一下主角名字")
	if s.StartIntent() {
		t.Fatal("用户再次发言后 StartIntent 应作废（同 suggestions）")
	}
	s.ApplyReply(host.CoCreateReply{Message: "好", Prompt: "## 主题", Ready: true, Raw: "raw"})
	if s.StartIntent() {
		t.Fatal("下一轮无意图应覆盖为 false")
	}
}
