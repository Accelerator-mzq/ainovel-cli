package headless

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/Accelerator-mzq/ainovel-cli/assets"
	"github.com/Accelerator-mzq/ainovel-cli/internal/bootstrap"
	"github.com/Accelerator-mzq/ainovel-cli/internal/diag"
	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
	"github.com/Accelerator-mzq/ainovel-cli/internal/entry/startup"
	"github.com/Accelerator-mzq/ainovel-cli/internal/host"
	"github.com/Accelerator-mzq/ainovel-cli/internal/logger"
	"github.com/Accelerator-mzq/ainovel-cli/internal/store"
	"github.com/Accelerator-mzq/ainovel-cli/internal/utils"
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

	// 唯一共享的 stdin reader：bufio.Reader 的 fill() 会把管道里已有的字节一次性
	// 读进私有缓冲，若每个消费方各建一只 reader，先读的那只会把后续行"超前读"进
	// 自己的缓冲再随函数返回丢弃（批量管道输入"意见\n开始\n"时第二轮只剩 EOF）。
	// 审阅提示与 ask_user 必须共用这一只 reader。
	stdinReader := bufio.NewReader(stdin)

	var eng *host.Host
	// reviewActive：审阅暂停期间为 true。notify 同步置位先于读 stdin，
	// consume 据此区分"审阅暂停的 Done"与"真正停机的 Done"（论证见 consume）。
	var reviewActive atomic.Bool
	// reviewNotify 由 guard goroutine 触发（eng 此时已赋值），consume 的重触发
	// 路径也会调它。CAS 防双读者：两条路径可能并发到达（abort 的 Done 先于
	// guard 的 notify goroutine 跑起来的窗口），共享 bufio.Reader 不允许两个
	// goroutine 同时 ReadString（数据竞争+互吞）；输掉 CAS 的一方直接返回。
	reviewNotify := func() {
		if !reviewActive.CompareAndSwap(false, true) {
			return // 已有一个审阅循环在读 stdin
		}
		defer reviewActive.Store(false)
		handlePlanReviewPrompt(eng, stdinReader, stderr)
	}
	created, err := host.New(cfg, bundle,
		host.WithInteractive(false),
		host.WithPlanReviewNotify(reviewNotify))
	if err != nil {
		return err
	}
	eng = created
	// newTerminalAskUser 内部 bufio.NewReader 对已是 *bufio.Reader 的输入原样复用
	//（NewReaderSize 的同型短路），不会二次包裹，与审阅路径共享同一只缓冲。
	eng.AskUser().SetHandler(newTerminalAskUser(stdinReader, stderr).handle)
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
		return consume(eng, stdout, stderr, roundHasContent, &reviewActive, reviewNotify)
	}

	return consume(eng, stdout, stderr, false, &reviewActive, reviewNotify)
}

// shouldReprompt 判定 Done 信号到达时是否需要重触发审阅提示：
// 审阅待决（pending）但既没人读 stdin（!reviewActive）、引擎也没在跑
// （runtimeState != "running"）。三个条件缺一不可：active 时已有读者在等输入；
// running 时引擎稍后还会停机或被门禁再次拦截；非 pending 则无审阅可言。
func shouldReprompt(reviewActive, pending bool, runtimeState string) bool {
	return !reviewActive && pending && runtimeState != "running"
}

func consume(eng *host.Host, stdout, stderr io.Writer, roundHasContent bool, reviewActive *atomic.Bool, reprompt func()) error {
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
			// done 是带缓冲 channel：每次 run 停机由 waitDone 非阻塞发一次信号，
			// 仅 Close 时关闭（ok=false 走上面分支）。所以这里 continue 之后
			// 下一轮 select 仍监听同一通道，能收到后续 run 的停机信号。
			//
			// 规划审阅门禁拦截时会 Abort 当前 run，waitDone 照常发 Done——
			// 若据此直接退出，审阅还阻塞在 stdin 上进程就没了（TUI 对同一信号
			// 是重挂 listenDone 活下去，headless 需要等价处理）。
			// 竞态论证——"Done 到达时既不 active、又不 pending、又不 running"
			// 只可能是真正的停机：
			//   1. 审阅期间 reviewActive 必为 true（notify 同步置位先于读 stdin）；
			//   2. notify goroutine 尚未跑起来的窗口期，PlanReviewPending 已为 true
			//      （guard 拦截的前提就是 pending）；
			//   3. 确认路径 HandleReviewInput 返回前 Resume 已把状态置为 running，
			//      而 reviewActive 在其后才清零，修改意见路径同理（Continue 置 running）；
			//      abort 先于 notify 串行落地（planreview.go）保证读到输入时已是
			//      Paused，确认/干预路径必走 Resume/Inject，不会留下无人重启的暂停。
			//
			// 悬挂兜底：修改意见轮若以纯文本回复/子代理报错收尾，dispatcher 不再
			// 触发派发 → 门禁不再拦截 → 不再 notify，但 waitDone 照发 Done。此时
			// 审阅待决（pending）却没人读 stdin（!active）——裸 continue 会让用户
			// 后续输入永远无人消费，进程永久挂起。重触发审阅提示再 continue
			//（reprompt 即 reviewNotify，内部 CAS 防与 guard notify 形成双读者）。
			active := reviewActive.Load()
			snap := eng.Snapshot()
			if shouldReprompt(active, snap.PlanReviewPending, snap.RuntimeState) {
				go reprompt()
				continue
			}
			if active || snap.PlanReviewPending || snap.RuntimeState == "running" {
				continue
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

// reviewInputHandler 收窄审阅函数对 Host 的依赖，便于测试注入 fake。
type reviewInputHandler interface {
	HandleReviewInput(string) (bool, error)
}

// handlePlanReviewPrompt 在 plan_review=on 的 headless 运行中处理一次规划审阅输入：
// 打印提示，读一行 stdin 喂 HandleReviewInput。修改意见注入后引擎恢复运行，
// 下次拦截 notify 会再次触发本函数（guard.ResetPrompt 在 HandleReviewInput
// 修改分支里完成）。EOF（无人值守管道）自动确认，避免卡死自动化。
// in 必须是与 ask_user 共享的唯一 bufio.Reader：防止各自缓冲超前读互吞字节；
// 审阅暂停期间引擎不运行，故与 ask_user 无并发争用。
func handlePlanReviewPrompt(eng reviewInputHandler, in *bufio.Reader, out io.Writer) {
	fmt.Fprintln(out, "\n[规划审阅] 大纲已生成（layered_outline.md）。输入修改意见，或输入「开始」进入写作：")
	for {
		line, err := in.ReadString('\n')
		text := utils.CleanInputLine(line)
		if text != "" {
			approved, herr := eng.HandleReviewInput(text)
			if herr != nil {
				// 处理失败（Continue/Inject/Resume 报错）时不能返回：报错分支没有
				// 启动新 run，此后不会再有 Done 信号触发 consume 的重提示兜底，
				// 返回即无人读 stdin，用户后续输入永远无人消费。留在循环里重试；
				// 确认放行在 HandleReviewInput 内幂等，重输「开始」会重试 Resume。
				fmt.Fprintf(out, "[规划审阅] 处理失败: %v，请重新输入：\n", herr)
				continue
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
			if _, herr := eng.HandleReviewInput("开始"); herr != nil {
				fmt.Fprintf(out, "[规划审阅] 自动确认失败: %v\n", herr)
			}
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
