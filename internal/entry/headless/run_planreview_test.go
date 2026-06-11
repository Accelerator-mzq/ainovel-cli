package headless

import (
	"bufio"
	"errors"
	"strings"
	"testing"
)

// reviewResult 是 fakeReviewHandler 单次调用的预设返回值。
type reviewResult struct {
	approved bool
	err      error
}

// fakeReviewHandler 记录 HandleReviewInput 收到的文本，按预设脚本依次返回；
// 脚本耗尽后重复返回最后一项。
type fakeReviewHandler struct {
	inputs  []string
	results []reviewResult
}

func (f *fakeReviewHandler) HandleReviewInput(text string) (bool, error) {
	f.inputs = append(f.inputs, text)
	r := f.results[0]
	if len(f.results) > 1 {
		f.results = f.results[1:]
	}
	return r.approved, r.err
}

// 确认词分支：approved=true 时打印"已确认"。
func TestHandlePlanReviewPromptConfirm(t *testing.T) {
	h := &fakeReviewHandler{results: []reviewResult{{approved: true}}}
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
	h := &fakeReviewHandler{results: []reviewResult{{approved: false}}}
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
	h := &fakeReviewHandler{results: []reviewResult{{approved: true}}}
	var out strings.Builder
	handlePlanReviewPrompt(h, bufio.NewReader(strings.NewReader("")), &out)
	if len(h.inputs) != 1 || h.inputs[0] != "开始" {
		t.Fatalf("unexpected inputs: %v", h.inputs)
	}
	if !strings.Contains(out.String(), "自动确认") {
		t.Fatalf("missing auto-confirm output: %q", out.String())
	}
}

// 报错重试分支：HandleReviewInput 报错时不返回（返回即无人读 stdin，
// 报错路径不再有 Done 信号触发重提示，进程悬挂），留在循环里读下一行重试。
func TestHandlePlanReviewPromptRetryOnError(t *testing.T) {
	h := &fakeReviewHandler{results: []reviewResult{
		{approved: false, err: errors.New("inject: boom")},
		{approved: true},
	}}
	var out strings.Builder
	handlePlanReviewPrompt(h, bufio.NewReader(strings.NewReader("加快节奏\n开始\n")), &out)
	if len(h.inputs) != 2 || h.inputs[0] != "加快节奏" || h.inputs[1] != "开始" {
		t.Fatalf("unexpected inputs: %v", h.inputs)
	}
	if !strings.Contains(out.String(), "处理失败") {
		t.Fatalf("missing failure output: %q", out.String())
	}
	if !strings.Contains(out.String(), "已确认") {
		t.Fatalf("missing confirm output after retry: %q", out.String())
	}
}

// Done 信号重触发判定真值表：审阅待决且无人读 stdin 且引擎未跑 → 才重触发。
func TestShouldReprompt(t *testing.T) {
	cases := []struct {
		name    string
		active  bool
		pending bool
		state   string
		want    bool
	}{
		{"待决+无人读+已停 → 重触发", false, true, "idle", true},
		{"待决+无人读+暂停 → 重触发", false, true, "paused", true},
		{"已有读者 → 不触发", true, true, "paused", false},
		{"引擎在跑 → 不触发", false, true, "running", false},
		{"非待决 → 不触发", false, false, "idle", false},
	}
	for _, c := range cases {
		if got := shouldReprompt(c.active, c.pending, c.state); got != c.want {
			t.Errorf("%s: shouldReprompt(%v, %v, %q) = %v, want %v",
				c.name, c.active, c.pending, c.state, got, c.want)
		}
	}
}
