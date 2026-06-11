package headless

import (
	"bufio"
	"strings"
	"testing"
)

// fakeReviewHandler 记录 HandleReviewInput 收到的文本，按预设值返回。
type fakeReviewHandler struct {
	inputs   []string
	approved bool
	err      error
}

func (f *fakeReviewHandler) HandleReviewInput(text string) (bool, error) {
	f.inputs = append(f.inputs, text)
	return f.approved, f.err
}

// 确认词分支：approved=true 时打印"已确认"。
func TestHandlePlanReviewPromptConfirm(t *testing.T) {
	h := &fakeReviewHandler{approved: true}
	var out strings.Builder
	handlePlanReviewPrompt(h, bufio.NewReader(strings.NewReader("开始\n")), &out)
	if len(h.inputs) != 1 || h.inputs[0] != "开始" {
		t.Fatalf("unexpected inputs: %v", h.inputs)
	}
	if !strings.Contains(out.String(), "已确认") {
		t.Fatalf("missing confirm output: %q", out.String())
	}
}

// 修改意见分支：approved=false 时打印"已注入"。
func TestHandlePlanReviewPromptFeedback(t *testing.T) {
	h := &fakeReviewHandler{approved: false}
	var out strings.Builder
	handlePlanReviewPrompt(h, bufio.NewReader(strings.NewReader("第二卷节奏太快，加一个过渡弧\n")), &out)
	if len(h.inputs) != 1 || h.inputs[0] != "第二卷节奏太快，加一个过渡弧" {
		t.Fatalf("unexpected inputs: %v", h.inputs)
	}
	if !strings.Contains(out.String(), "已注入") {
		t.Fatalf("missing feedback output: %q", out.String())
	}
}

// EOF 分支：stdin 关闭时自动确认，handler 应收到「开始」。
func TestHandlePlanReviewPromptEOF(t *testing.T) {
	h := &fakeReviewHandler{approved: true}
	var out strings.Builder
	handlePlanReviewPrompt(h, bufio.NewReader(strings.NewReader("")), &out)
	if len(h.inputs) != 1 || h.inputs[0] != "开始" {
		t.Fatalf("unexpected inputs: %v", h.inputs)
	}
	if !strings.Contains(out.String(), "自动确认") {
		t.Fatalf("missing auto-confirm output: %q", out.String())
	}
}
