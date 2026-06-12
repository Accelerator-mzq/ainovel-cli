package sim

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	// fetchTimeout 单条 URL 的整体请求超时
	fetchTimeout = 30 * time.Second
	// fetchMaxBodyBytes 响应体大小上限（20MB），超限该条报错跳过
	fetchMaxBodyBytes = int64(20 << 20)
	// fetchUserAgent 模拟常见浏览器 UA——默认 Go UA 会被很多站点直接 403
	fetchUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
)

// FetchOptions 是 RunFetch 的参数。Client/MaxBodyBytes 为测试注入位，
// 零值即生产默认（30s 超时客户端 / 20MB 上限）。
type FetchOptions struct {
	SourceDir    string   // simulate 根目录（绝对路径，由 Host 解析）
	Author       string   // 作者名，将作为 personas/ 子目录名
	URLs         []string // 待抓取 URL，仅支持 http/https
	Client       *http.Client
	MaxBodyBytes int64
}

// fetchOutcome 是单条 URL 的处理结果。
type fetchOutcome struct {
	fileName string
	chars    int
	warnings []string
	err      error
}

// RunFetch 抓取网络语料落盘 simulate/personas/<作者>/，返回事件通道。
// 用法级错误（参数缺失、作者名非法、URL scheme 不支持）同步返回；
// 网络/提取失败逐条独立处理，单条失败不中断其余。
// 不调 LLM、不动 Store——落盘后由用户检查质量再自行运行 /simulate。
func RunFetch(ctx context.Context, opts FetchOptions) (<-chan Event, error) {
	if strings.TrimSpace(opts.SourceDir) == "" {
		return nil, fmt.Errorf("source dir is required")
	}
	if err := validateAuthorDirName(opts.Author); err != nil {
		return nil, err
	}
	if len(opts.URLs) == 0 {
		return nil, fmt.Errorf("至少需要一条 URL")
	}
	parsed := make([]*url.URL, 0, len(opts.URLs))
	for _, raw := range opts.URLs {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("仅支持 http/https URL：%s", raw)
		}
		parsed = append(parsed, u)
	}

	author := strings.TrimSpace(opts.Author)
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: fetchTimeout}
	}
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = fetchMaxBodyBytes
	}

	events := make(chan Event, 32)
	go func() {
		defer close(events)
		emit := func(stage Stage, current, total int, msg string, err error) {
			ev := Event{Time: time.Now(), Stage: stage, Current: current, Total: total, Message: msg, Err: err}
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		}

		destDir := filepath.Join(opts.SourceDir, personasDirName, author)
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			emit(StageError, 0, 0, "创建语料目录失败", err)
			return
		}

		okCount := 0
		for i, u := range parsed {
			if err := ctx.Err(); err != nil {
				emit(StageError, i, len(parsed), "用户取消语料抓取", err)
				return
			}
			emit(StageFetch, i+1, len(parsed), fmt.Sprintf("抓取 %d/%d：%s", i+1, len(parsed), u.String()), nil)
			out := fetchOne(ctx, client, u, destDir, maxBody)
			if out.err != nil {
				// 注意：单条失败 Err 必须传 nil，原因进 Message。
				// TUI 的 appendEvent 收到非 nil Err 会把整个面板置为失败态，
				// 而部分失败仍应以成功收尾。
				emit(StageFetch, i+1, len(parsed), fmt.Sprintf("✗ %s：%v", u.String(), out.err), nil)
				continue
			}
			okCount++
			msg := fmt.Sprintf("✓ %s（%d 字符）", out.fileName, out.chars)
			if len(out.warnings) > 0 {
				msg += "　⚠ " + strings.Join(out.warnings, "；")
			}
			emit(StageFetch, i+1, len(parsed), msg, nil)
		}

		if okCount == 0 {
			emit(StageError, len(parsed), len(parsed), "全部 URL 抓取失败", fmt.Errorf("no corpus fetched"))
			return
		}
		emit(StageDone, len(parsed), len(parsed), fmt.Sprintf(
			"语料抓取完成：成功 %d/%d，落盘 simulate/%s/%s/；请检查语料质量后运行 /simulate 生成画像",
			okCount, len(parsed), personasDirName, author), nil)
	}()
	return events, nil
}

// fetchOne 处理单条 URL：抓取 → 按 Content-Type 分流解码/提取 → 质检 → 落盘。
func fetchOne(ctx context.Context, client *http.Client, u *url.URL, destDir string, maxBody int64) fetchOutcome {
	var out fetchOutcome
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		out.err = err
		return out
	}
	req.Header.Set("User-Agent", fetchUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		out.err = fmt.Errorf("请求失败: %w", err)
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.err = fmt.Errorf("HTTP %d", resp.StatusCode)
		return out
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		out.err = fmt.Errorf("读取响应失败: %w", err)
		return out
	}
	if int64(len(body)) > maxBody {
		out.err = fmt.Errorf("响应超过 %dKB 上限", maxBody>>10)
		return out
	}

	contentType := resp.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)
	var title, text string
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		title, text, err = extractArticle(body, contentType, u)
	case mediaType == "text/plain" || strings.HasSuffix(strings.ToLower(u.Path), ".txt"):
		text, err = decodePlainText(body)
		if err == nil {
			text = normalizeText(text)
			if text == "" {
				err = fmt.Errorf("txt 内容为空")
			}
			title = strings.TrimSuffix(path.Base(u.Path), path.Ext(u.Path))
		}
	default:
		err = fmt.Errorf("不支持的内容类型 %q（仅支持 HTML 页面与 txt 文本）", mediaType)
	}
	if err != nil {
		out.err = err
		return out
	}

	out.fileName = corpusFileName(title, u.String())
	if err := os.WriteFile(filepath.Join(destDir, out.fileName), []byte(text), 0o644); err != nil {
		out.err = fmt.Errorf("写入语料文件失败: %w", err)
		return out
	}
	out.chars = len([]rune(text))
	out.warnings = qualityWarnings(text)
	return out
}
