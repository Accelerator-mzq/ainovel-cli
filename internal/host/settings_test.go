package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	storepkg "github.com/Accelerator-mzq/ainovel-cli/internal/store"
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

// TestMergeSettingsPreservingCocreate 验证重启同步时共创原文段不被冲掉（C1 回归）。
func TestMergeSettingsPreservingCocreate(t *testing.T) {
	existing := "## 文件：旧设定.md\n\n旧内容\n\n" + cocreateSectionHeader + "\n\n### 用户输入 1\n\n设定A原文"
	merged := mergeSettingsPreservingCocreate("## 文件：新设定.md\n\n新内容", existing)
	if !strings.Contains(merged, "新内容") {
		t.Fatalf("缺新收集内容：%q", merged)
	}
	if !strings.Contains(merged, "设定A原文") || !strings.Contains(merged, cocreateSectionHeader) {
		t.Fatalf("共创原文段被冲掉：%q", merged)
	}
	if strings.Contains(merged, "旧内容") {
		t.Fatalf("旧 settings 段应被新收集内容替换：%q", merged)
	}
	// 无共创段时原样返回新内容
	if got := mergeSettingsPreservingCocreate("新内容", "只有旧设定"); got != "新内容" {
		t.Fatalf("无共创段应原样返回：%q", got)
	}
}

// TestAppendCoCreateTranscript_CapAndDedup 验证共创原文体量上限与重复 Ctrl+S 幂等替换。
func TestAppendCoCreateTranscript_CapAndDedup(t *testing.T) {
	h := &Host{store: storepkg.NewStore(t.TempDir())}

	// 超长 transcript 截断并标注
	long := strings.Repeat("创", maxSettingsFileRunes+100)
	h.AppendCoCreateTranscript(long)
	got, err := h.store.Settings.LoadUserSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "（共创原文超长已截断）") {
		t.Fatal("超长共创原文应截断并标注")
	}
	if n := len([]rune(got)); n > maxSettingsFileRunes+200 {
		t.Fatalf("截断未生效，len=%d", n)
	}

	// 重复调用幂等替换：新内容覆盖旧共创段，不无限追加
	h.AppendCoCreateTranscript("第二次原文")
	got, _ = h.store.Settings.LoadUserSettings()
	if !strings.Contains(got, "第二次原文") || strings.Contains(got, "创创创") {
		t.Fatalf("重复 Ctrl+S 应替换旧共创段：%q", got)
	}
	if strings.Count(got, cocreateSectionHeader) != 1 {
		t.Fatalf("共创段标记头应只出现一次：%q", got)
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
