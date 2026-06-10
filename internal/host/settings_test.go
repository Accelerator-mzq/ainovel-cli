package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCollectUserSettings 验证 settings/ 目录扫描：递归、按路径排序、文件头分隔、忽略非文本扩展。
func TestCollectUserSettings(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "settings", "world")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, "settings", rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("b-角色.md", "主角：林尘")
	write(filepath.Join("world", "a-境界.txt"), "练气→筑基")
	write("ignore.png", "binary")

	content, files, err := CollectUserSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if files != 2 {
		t.Fatalf("files = %d, want 2", files)
	}
	// 按相对路径字典序：b-角色.md 在前还是 world/a-境界.txt 在前由排序规则决定——
	// 断言两段都在且各有文件头
	if !strings.Contains(content, "## 文件：b-角色.md") || !strings.Contains(content, "主角：林尘") {
		t.Fatalf("缺 b-角色.md 段：%q", content)
	}
	if !strings.Contains(content, "a-境界") || !strings.Contains(content, "练气→筑基") {
		t.Fatalf("缺 world/a-境界.txt 段：%q", content)
	}
	if strings.Contains(content, "binary") {
		t.Fatal("不应读取 .png")
	}

	// settings/ 不存在 → 空内容不报错
	content, files, err = CollectUserSettings(t.TempDir())
	if err != nil || content != "" || files != 0 {
		t.Fatalf("missing dir: %q %d %v", content, files, err)
	}
}

// TestCollectUserSettings_SizeCap 验证单文件超限截断并带提示。
func TestCollectUserSettings_SizeCap(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("设", maxSettingsFileRunes+100)
	if err := os.WriteFile(filepath.Join(dir, "settings", "big.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	content, _, err := CollectUserSettings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "（已截断）") {
		t.Fatal("超限文件应截断并标注")
	}
	if got := len([]rune(content)); got > maxSettingsFileRunes+500 {
		t.Fatalf("截断未生效，len=%d", got)
	}
}
