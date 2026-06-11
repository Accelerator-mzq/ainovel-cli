package host

import "testing"

// TestParseCoCreate_StartIntent 验证 start_intent 标签解析：
//   - true 值 → StartIntent=true
//   - 缺标签（旧模型/流式截断）→ StartIntent=false
//   - false 值 → StartIntent=false
func TestParseCoCreate_StartIntent(t *testing.T) {
	raw := "<reply>好的，马上开始</reply>\n<draft>## 主题\n- 测试</draft>\n" +
		"<ready>true</ready>\n<start_intent>true</start_intent>\n<suggestions></suggestions>"
	reply, err := parseCoCreateResponse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reply.StartIntent {
		t.Fatal("start_intent=true 应解析为 true")
	}

	// 缺标签（旧模型/流式截断）→ false
	noTag := "<reply>继续聊</reply>\n<draft>## 主题</draft>\n<ready>false</ready>\n<suggestions></suggestions>"
	reply2, err := parseCoCreateResponse(noTag)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if reply2.StartIntent {
		t.Fatal("缺 start_intent 标签应为 false")
	}

	// false 值
	falseTag := "<reply>再聊聊</reply>\n<draft>## 主题</draft>\n<ready>true</ready>\n" +
		"<start_intent>false</start_intent>\n<suggestions></suggestions>"
	reply3, _ := parseCoCreateResponse(falseTag)
	if reply3.StartIntent {
		t.Fatal("start_intent=false 应为 false")
	}
}
