package sim

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// drainFetch 启动 RunFetch 并收完所有事件。
func drainFetch(t *testing.T, opts FetchOptions) []Event {
	t.Helper()
	ch, err := RunFetch(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunFetch: %v", err)
	}
	var events []Event
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) == 0 {
		t.Fatal("没有收到任何事件")
	}
	return events
}

func lastEvent(events []Event) Event { return events[len(events)-1] }

// fixtureHTML 生成一个 readability 可稳定提取的中文文章页。
func fixtureHTML(title string) string {
	para := strings.Repeat("<p>他在镇上住了三十年，每天清晨沿着河堤走到渡口，看船来船往，这个习惯从未改变过，连下雪天也不例外。</p>", 30)
	return "<html><head><title>" + title + "</title></head><body><article>" + para + "</article></body></html>"
}

func TestRunFetchHTMLUTF8(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fixtureHTML("渡口三十年")))
	}))
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{SourceDir: root, Author: "测试作者", URLs: []string{srv.URL + "/article/1"}})

	if last := lastEvent(events); last.Stage != StageDone {
		t.Fatalf("last stage = %s, want %s（events: %+v）", last.Stage, StageDone, events)
	}
	dir := filepath.Join(root, "personas", "测试作者")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("应落盘 1 个语料文件: %v, %v", entries, err)
	}
	if !strings.Contains(entries[0].Name(), "渡口三十年") || !strings.HasSuffix(entries[0].Name(), ".txt") {
		t.Fatalf("文件名应含标题且为 .txt: %q", entries[0].Name())
	}
	data, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if !strings.Contains(string(data), "他在镇上住了三十年") {
		t.Fatal("落盘内容应含正文")
	}
}

func TestRunFetchHTMLGBK(t *testing.T) {
	gbkBody, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(fixtureHTML("雪夜渡口")))
	if err != nil {
		t.Fatalf("构造 GBK 页面失败: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=gbk")
		_, _ = w.Write(gbkBody)
	}))
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{SourceDir: root, Author: "测试作者", URLs: []string{srv.URL}})
	if last := lastEvent(events); last.Stage != StageDone {
		t.Fatalf("last stage = %s, want done", last.Stage)
	}
	dir := filepath.Join(root, "personas", "测试作者")
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("应落盘 1 个文件，got %d", len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if !strings.Contains(string(data), "他在镇上住了三十年") {
		t.Fatal("GBK 页面应正确解码出中文正文")
	}
}

func TestRunFetchTxtDirectLink(t *testing.T) {
	novel := strings.Repeat("江水东去，他守着这条渡船过了半生，岸边的柳树绿了又黄。\n", 40)
	gbkTxt, _ := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(novel))
	mux := http.NewServeMux()
	mux.HandleFunc("/utf8.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(novel))
	})
	mux.HandleFunc("/gbk.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream") // 故意不给 text/plain，靠 .txt 后缀分流
		_, _ = w.Write(gbkTxt)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{
		SourceDir: root, Author: "测试作者",
		URLs: []string{srv.URL + "/utf8.txt", srv.URL + "/gbk.txt"},
	})
	if last := lastEvent(events); last.Stage != StageDone {
		t.Fatalf("last stage = %s, want done（%+v）", last.Stage, events)
	}
	dir := filepath.Join(root, "personas", "测试作者")
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("应落盘 2 个文件，got %d", len(entries))
	}
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if !strings.Contains(string(data), "江水东去") {
			t.Fatalf("%s 内容解码错误", e.Name())
		}
	}
}

func TestRunFetchPartialFailureStillDone(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fixtureHTML("正常页")))
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{
		SourceDir: root, Author: "测试作者",
		URLs: []string{srv.URL + "/ok", srv.URL + "/missing", srv.URL + "/pdf"},
	})
	last := lastEvent(events)
	if last.Stage != StageDone {
		t.Fatalf("部分失败仍应 done 收尾，got %s", last.Stage)
	}
	if !strings.Contains(last.Message, "1/3") {
		t.Fatalf("摘要应含成功比例 1/3：%q", last.Message)
	}
	// 失败明细事件 Err 必须为 nil（否则 TUI 把整个面板置为失败态）
	for _, ev := range events {
		if ev.Stage == StageFetch && ev.Err != nil {
			t.Fatalf("逐条失败应只进 Message 不设 Err：%+v", ev)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(root, "personas", "测试作者"))
	if len(entries) != 1 {
		t.Fatalf("只应落盘成功的 1 个文件，got %d", len(entries))
	}
}

func TestRunFetchAllFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{SourceDir: root, Author: "测试作者", URLs: []string{srv.URL}})
	if last := lastEvent(events); last.Stage != StageError {
		t.Fatalf("全部失败应以 error 收尾，got %s", last.Stage)
	}
}

func TestRunFetchOversizeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("a", 4096)))
	}))
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{
		SourceDir: root, Author: "测试作者",
		URLs:         []string{srv.URL + "/big.txt"},
		MaxBodyBytes: 1024, // 测试注入小上限
	})
	if last := lastEvent(events); last.Stage != StageError {
		t.Fatalf("超限应整批失败（唯一 URL），got %s", last.Stage)
	}
}

func TestRunFetchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{
		SourceDir: root, Author: "测试作者",
		URLs:   []string{srv.URL},
		Client: &http.Client{Timeout: 20 * time.Millisecond},
	})
	if last := lastEvent(events); last.Stage != StageError {
		t.Fatalf("超时应失败，got %s", last.Stage)
	}
}

func TestRunFetchRefetchOverwritesSameFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fixtureHTML("同一篇")))
	}))
	defer srv.Close()

	root := t.TempDir()
	opts := FetchOptions{SourceDir: root, Author: "测试作者", URLs: []string{srv.URL + "/same"}}
	drainFetch(t, opts)
	drainFetch(t, opts) // 重抓同一 URL
	entries, _ := os.ReadDir(filepath.Join(root, "personas", "测试作者"))
	if len(entries) != 1 {
		t.Fatalf("同 URL 重抓应覆盖同名文件，got %d 个文件", len(entries))
	}
}

func TestRunFetchFollowsRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fixtureHTML("重定向后的页面")))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	events := drainFetch(t, FetchOptions{SourceDir: root, Author: "测试作者", URLs: []string{srv.URL + "/old"}})
	if last := lastEvent(events); last.Stage != StageDone {
		t.Fatalf("重定向应被跟随并成功，got %s", last.Stage)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "personas", "测试作者"))
	if len(entries) != 1 {
		t.Fatalf("应落盘 1 个文件，got %d", len(entries))
	}
}

func TestRunFetchCancelDuringLastURL(t *testing.T) {
	// 复现场景：取消发生在最后一条 URL 的 fetchOne 执行中——
	// 错误走 per-URL 软失败分支（Err=nil），必须靠循环后的 ctx 终判
	// 以 StageError"用户取消"收尾，而不是误报 done 或"全部 URL 抓取失败"。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel() // 请求进行中模拟用户取消
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fixtureHTML("取消页")))
	}))
	defer srv.Close()

	root := t.TempDir()
	ch, err := RunFetch(ctx, FetchOptions{SourceDir: root, Author: "测试作者", URLs: []string{srv.URL}})
	if err != nil {
		t.Fatalf("RunFetch: %v", err)
	}
	var events []Event
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) == 0 {
		t.Fatal("至少应收到取消前的首条进度事件")
	}
	// emit 在 ctx 已取消后 select 两分支均就绪、随机二选一，StageError 事件
	// 可能被丢弃（包内既有模式，不改 emit）。稳定不变式是"绝不能以 done 收尾"；
	// 若 StageError 事件送达，则进一步校验文案含"取消"。
	last := lastEvent(events)
	if last.Stage == StageDone {
		t.Fatalf("取消后不应以 done 收尾：%+v", events)
	}
	if last.Stage == StageError && !strings.Contains(last.Message, "取消") {
		t.Fatalf("取消收尾事件文案应含\"取消\"：%q", last.Message)
	}
}

func TestRunFetchRejectsBadInput(t *testing.T) {
	root := t.TempDir()
	cases := []FetchOptions{
		{SourceDir: root, Author: "../逃逸", URLs: []string{"https://example.com"}},  // 路径穿越
		{SourceDir: root, Author: "正常", URLs: []string{"ftp://example.com/a.txt"}}, // 非 http(s)
		{SourceDir: root, Author: "正常", URLs: nil},                                 // 无 URL
		{SourceDir: "", Author: "正常", URLs: []string{"https://example.com"}},       // 无根目录
	}
	for i, opts := range cases {
		if _, err := RunFetch(context.Background(), opts); err == nil {
			t.Errorf("case %d 应同步返回错误", i)
		}
	}
}
