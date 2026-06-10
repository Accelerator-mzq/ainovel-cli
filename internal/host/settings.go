package host

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// settings 收集体量上限：单文件 / 全部合计（rune 计）。
// 超限截断并标注——这是落盘存档的体量保护（防止超大文件撑爆 user_settings.md），
// 不是 Architect 上下文预算；注入预算由 novel_context 注入端字节兜底负责。
const (
	maxSettingsFileRunes  = 30000
	maxSettingsTotalRunes = 60000
)

// cocreateSectionHeader 共创原文段的标记头，New 同步与 Append 共用。
const cocreateSectionHeader = "# 共创对话用户原文（备查）"

// extractCocreateSection 从已落盘内容中切出共创原文段；无则返回空串。
func extractCocreateSection(existing string) string {
	if idx := strings.Index(existing, cocreateSectionHeader); idx >= 0 {
		return strings.TrimSpace(existing[idx:])
	}
	return ""
}

// mergeSettingsPreservingCocreate 把新收集的 settings/ 内容与已落盘内容合并：
// 保留已有的共创原文段。重启续写时 CollectUserSettings 只含 settings/ 文件内容，
// 直接覆盖会把 Ctrl+S 落盘的共创对话原文永久冲掉（Resume 路径不会重放 Append）。
func mergeSettingsPreservingCocreate(collected, existing string) string {
	if section := extractCocreateSection(existing); section != "" {
		return collected + "\n\n" + section
	}
	return collected
}

// settingsExts 允许的设定文件扩展名（与 /simulate 的语料口径一致）。
var settingsExts = map[string]bool{".md": true, ".txt": true, ".markdown": true}

// CollectUserSettings 递归读取 baseDir/settings/ 下的文本文件，
// 按相对路径字典序拼接为带文件头的 Markdown 全文。
// 目录不存在返回空串；单文件与合计超限时截断并标注。
func CollectUserSettings(baseDir string) (content string, files int, err error) {
	root := filepath.Join(baseDir, "settings")
	info, statErr := os.Stat(root)
	if statErr != nil || !info.IsDir() {
		return "", 0, nil // 没有 settings 目录是常态，不是错误
	}

	var paths []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if settingsExts[strings.ToLower(filepath.Ext(path))] {
			paths = append(paths, path)
		}
		return nil
	})
	if walkErr != nil {
		return "", 0, fmt.Errorf("扫描 settings 目录失败: %w", walkErr)
	}
	sort.Strings(paths)

	var b strings.Builder
	total := 0
	for _, p := range paths {
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return "", 0, fmt.Errorf("读取设定文件 %s 失败: %w", p, readErr)
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		if runes := []rune(text); len(runes) > maxSettingsFileRunes {
			text = string(runes[:maxSettingsFileRunes]) + "\n\n（已截断）"
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = filepath.Base(p)
		}
		section := fmt.Sprintf("## 文件：%s\n\n%s\n\n", filepath.ToSlash(rel), text)
		if total+len([]rune(section)) > maxSettingsTotalRunes {
			b.WriteString("\n（更多设定文件因总量超限未纳入，请精简 settings/ 内容）\n")
			break
		}
		b.WriteString(section)
		total += len([]rune(section))
		files++
	}
	return strings.TrimSpace(b.String()), files, nil
}

// AppendCoCreateTranscript 把共创对话用户原文追加进 user_settings.md。
// 在已有设定（settings/ 目录内容）之后以独立章节追加；无已有内容则单独成文。
func (h *Host) AppendCoCreateTranscript(transcript string) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return
	}
	// 体量上限：与单文件上限同口径（30000 rune），防止超长对话旁路撑爆存档。
	if runes := []rune(transcript); len(runes) > maxSettingsFileRunes {
		transcript = string(runes[:maxSettingsFileRunes]) + "\n\n（共创原文超长已截断）"
	}
	existing, err := h.store.Settings.LoadUserSettings()
	if err != nil {
		slog.Warn("读取已有用户设定失败，跳过共创原文保全", "module", "boot", "err", err)
		return
	}
	section := cocreateSectionHeader + "\n\n" + transcript
	merged := section
	if strings.TrimSpace(existing) != "" {
		// 去重：重复 Ctrl+S（重开同名书）时替换旧的共创段而不是无限追加
		if idx := strings.Index(existing, cocreateSectionHeader); idx >= 0 {
			existing = strings.TrimSpace(existing[:idx])
		}
		if existing != "" {
			merged = existing + "\n\n" + section
		}
	}
	if err := h.store.Settings.SaveUserSettings(merged); err != nil {
		slog.Warn("共创原文落盘失败", "module", "boot", "err", err)
	}
}
