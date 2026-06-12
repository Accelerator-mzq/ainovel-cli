//go:build e2e

package sim

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRunFetchRealURL 对真实公开静态页冒烟（维基文库公版文本）。
// 仅手动运行：go test -tags e2e -run TestRunFetchRealURL ./internal/host/sim/ -v
func TestRunFetchRealURL(t *testing.T) {
	root := t.TempDir()
	ch, err := RunFetch(context.Background(), FetchOptions{
		SourceDir: root,
		Author:    "冒烟测试",
		URLs:      []string{"https://zh.wikisource.org/wiki/%E5%AD%94%E4%B9%99%E5%B7%B1"},
	})
	if err != nil {
		t.Fatalf("RunFetch: %v", err)
	}
	var events []Event
	for ev := range ch {
		events = append(events, ev)
		t.Logf("[%s %d/%d] %s err=%v", ev.Stage, ev.Current, ev.Total, ev.Message, ev.Err)
	}
	last := events[len(events)-1]
	if last.Stage != StageDone {
		t.Fatalf("last stage = %s, want done", last.Stage)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "personas", "冒烟测试"))
	if len(entries) != 1 {
		t.Fatalf("应落盘 1 个文件，got %d", len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(root, "personas", "冒烟测试", entries[0].Name()))
	t.Logf("落盘 %s（%d bytes），前 200 字节：%s", entries[0].Name(), len(data), data[:min(200, len(data))])
}
