package headless

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Accelerator-mzq/ainovel-cli/assets"
	"github.com/Accelerator-mzq/ainovel-cli/internal/bootstrap"
	"github.com/Accelerator-mzq/ainovel-cli/internal/diag"
	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
	"github.com/Accelerator-mzq/ainovel-cli/internal/entry/startup"
	"github.com/Accelerator-mzq/ainovel-cli/internal/host"
	"github.com/Accelerator-mzq/ainovel-cli/internal/logger"
	"github.com/Accelerator-mzq/ainovel-cli/internal/store"
)

type Options struct {
	Prompt string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run 以无界面模式运行会话内核，直接消费 Engine 事件与流式输出。
// 未来若新增“续写已有小说”等共享启动方式，不应直接堆到这里，
// 而应先落到 internal/entry/startup，再由 headless 入口调用。
func Run(cfg bootstrap.Config, bundle assets.Bundle, opts Options) error {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	var eng *host.Host
	// reviewNotify 由 guard goroutine 触发，eng 此时已赋值
	reviewNotify := func() {
		runPlanReviewLoop(eng, stdin, stderr)
	}
	created, err := host.New(cfg, bundle,
		host.WithInteractive(false),
		host.WithPlanReviewNotify(reviewNotify))
	if err != nil {
		return err
	}
	eng = created
	eng.AskUser().SetHandler(newTerminalAskUser(stdin, stderr).handle)
	cleanup := logger.SetupFile(eng.Dir(), "headless.log", false)
	defer cleanup()
	defer eng.Close()
	// 运行结束 / 出错返回时落一份脱敏诊断，方便 headless 用户贴 issue。
	// （外部 kill 的挂死不走 defer，仍需在 TUI 里手动 /diag。）
	defer func() { _, _ = diag.Export(store.NewStore(eng.Dir())) }()

	prompt := strings.TrimSpace(opts.Prompt)
	if prompt != "" {
		plan, err := startup.PrepareQuick(startup.Request{
			Mode:        startup.ModeQuick,
			UserPrompt:  prompt,
			OutputDir:   eng.Dir(),
			Interactive: true,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(stderr, "headless 启动: %s\n", eng.Dir())
		if err := eng.StartPrepared(plan.StartPrompt); err != nil {
			return err
		}
	} else {
		items, err := eng.ReplayQueue(0)
		if err != nil {
			return err
		}
		roundHasContent, err := replayQueue(items, stdout, stderr)
		if err != nil {
			return err
		}
		label, err := eng.Resume()
		if err != nil {
			return err
		}
		if label == "" {
			return fmt.Errorf("headless 模式需要 --prompt，或输出目录 %q 下已有可恢复会话", eng.Dir())
		}
		fmt.Fprintf(stderr, "headless 恢复: %s (%s)\n", eng.Dir(), label)
		return consume(eng, stdout, stderr, roundHasContent)
	}

	return consume(eng, stdout, stderr, false)
}

func consume(eng *host.Host, stdout, stderr io.Writer, roundHasContent bool) error {
	for {
		select {
		case ev, ok := <-eng.Events():
			if !ok {
				return nil
			}
			writeEvent(stderr, ev)
		case delta, ok := <-eng.Stream():
			if !ok {
				continue
			}
			if delta == host.StreamClearSentinel {
				if roundHasContent {
					if _, err := io.WriteString(stdout, "\n\n"); err != nil {
						return err
					}
					roundHasContent = false
				}
				continue
			}
			if delta == "" {
				continue
			}
			if _, err := io.WriteString(stdout, delta); err != nil {
				return err
			}
			roundHasContent = true
		case _, ok := <-eng.Done():
			if !ok {
				return nil
			}
			return drainPending(eng, stdout, stderr, roundHasContent)
		}
	}
}

func drainPending(eng *host.Host, stdout, stderr io.Writer, roundHasContent bool) error {
	for {
		select {
		case ev, ok := <-eng.Events():
			if ok {
				writeEvent(stderr, ev)
			}
		case delta, ok := <-eng.Stream():
			if !ok {
				continue
			}
			if delta == host.StreamClearSentinel {
				if roundHasContent {
					if _, err := io.WriteString(stdout, "\n\n"); err != nil {
						return err
					}
					roundHasContent = false
				}
				continue
			}
			if delta != "" {
				if _, err := io.WriteString(stdout, delta); err != nil {
					return err
				}
				roundHasContent = true
			}
		default:
			if roundHasContent {
				if _, err := io.WriteString(stdout, "\n"); err != nil {
					return err
				}
			}
			return nil
		}
	}
}

func writeEvent(w io.Writer, ev host.Event) {
	if w == nil || strings.TrimSpace(ev.Summary) == "" {
		return
	}
	ts := ev.Time.Format("15:04:05")
	if ts == "00:00:00" {
		ts = "--:--:--"
	}
	fmt.Fprintf(w, "[%s] [%s] %s\n", ts, ev.Category, ev.Summary)
}

// runPlanReviewLoop 在 plan_review=on 的 headless 运行中处理规划审阅：
// 打印提示，读一行 stdin 喂 HandleReviewInput。修改意见注入后引擎恢复运行，
// 下次拦截 notify 会再次触发本函数（guard.ResetPrompt 在 HandleReviewInput
// 修改分支里完成）。EOF（无人值守管道）自动确认，避免卡死自动化。
// 已知限制：与 ask_user 共享 stdin——审阅暂停期间引擎不运行，无并发争用。
func runPlanReviewLoop(eng *host.Host, stdin io.Reader, out io.Writer) {
	fmt.Fprintln(out, "\n[规划审阅] 大纲已生成（layered_outline.md）。输入修改意见，或输入「开始」进入写作：")
	r := bufio.NewReader(stdin)
	for {
		line, err := r.ReadString('\n')
		text := strings.TrimSpace(line)
		if text != "" {
			approved, herr := eng.HandleReviewInput(text)
			if herr != nil {
				fmt.Fprintf(out, "[规划审阅] 处理失败: %v\n", herr)
			}
			if approved {
				fmt.Fprintln(out, "[规划审阅] 已确认，进入写作")
			} else {
				fmt.Fprintln(out, "[规划审阅] 修改意见已注入，调整完成后将再次暂停审阅")
			}
			return
		}
		if err != nil {
			fmt.Fprintln(out, "[规划审阅] stdin 关闭，自动确认进入写作")
			_, _ = eng.HandleReviewInput("开始")
			return
		}
	}
}

func replayQueue(items []domain.RuntimeQueueItem, stdout, stderr io.Writer) (bool, error) {
	var roundHasContent bool
	for _, item := range items {
		switch item.Kind {
		case domain.RuntimeQueueUIEvent:
			writeEvent(stderr, host.Event{
				Time:     item.Time,
				Category: item.Category,
				Summary:  item.Summary,
			})
		case domain.RuntimeQueueStreamClear:
			if roundHasContent {
				if _, err := io.WriteString(stdout, "\n\n"); err != nil {
					return roundHasContent, err
				}
				roundHasContent = false
			}
		case domain.RuntimeQueueStreamDelta:
			text := host.ReplayDeltaText(item)
			if text == "" {
				continue
			}
			if _, err := io.WriteString(stdout, text); err != nil {
				return roundHasContent, err
			}
			roundHasContent = true
		}
	}
	return roundHasContent, nil
}
