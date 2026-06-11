package host

import "testing"

// TestParseCoCreate_StartIntent 验证 start_intent 标签解析：
//   - true 值 → StartIntent=true
//   - 缺标签（旧模型/流式截断）→ StartIntent=false
//   - false 值 → StartIntent=false
//   - 流式截断（有开无闭 / 值中截断）容错
//   - typo 开标签（无开有闭）容错
//   - ready 无闭标签时靠 <start_intent> 开标签断尾（兜底表回归锁定）
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

	// 流式截断：有开无闭（串尾截断）→ extractTagContent 取到末尾，"true" 仍解析为 true
	truncated := "<reply>开始吧</reply>\n<draft>## 主题</draft>\n<ready>true</ready>\n<start_intent>true"
	reply4, err := parseCoCreateResponse(truncated)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reply4.StartIntent {
		t.Fatal("有开无闭（串尾截断）start_intent=true 应解析为 true")
	}

	// 值中截断：<start_intent>tr（无闭）→ "tr" 不是 true → false
	midTrunc := "<reply>开始吧</reply>\n<draft>## 主题</draft>\n<ready>true</ready>\n<start_intent>tr"
	reply5, err := parseCoCreateResponse(midTrunc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if reply5.StartIntent {
		t.Fatal("值中截断（<start_intent>tr）应为 false")
	}

	// typo 无开有闭：<tart_intent>true</start_intent> → 兜底抠出的内容含标签残留，
	// 不等于 "true" → false（不误触发开始）
	typoTag := "<reply>r</reply>\n<draft>d</draft>\n<ready>true</ready>\n" +
		"<tart_intent>true</start_intent>\n<suggestions></suggestions>"
	reply6, err := parseCoCreateResponse(typoTag)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if reply6.StartIntent {
		t.Fatal("typo 开标签（<tart_intent>）start_intent 应为 false")
	}

	// 回归锁定：ready 无闭标签时靠已知开标签表断尾——<start_intent> 必须在
	// extractTagContent 的兜底表里，否则 ready 内容会被 start_intent 段污染
	// （将来误删兜底表中的 start_intent 会让本用例失败）。
	readyTrunc := "<reply>r</reply>\n<draft>d</draft>\n<ready>true\n" +
		"<start_intent>false</start_intent>\n<suggestions></suggestions>"
	reply7, err := parseCoCreateResponse(readyTrunc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reply7.Ready {
		t.Fatal("ready 无闭标签时应被 <start_intent> 开标签断尾，Ready 应为 true")
	}
	if reply7.StartIntent {
		t.Fatal("start_intent=false 应为 false")
	}
}
