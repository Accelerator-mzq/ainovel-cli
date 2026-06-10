package store

import "os"

// SettingsStore 管理用户外部设定文档（user_settings.md）。
// 内容来自启动目录 settings/ 文件夹与共创对话原文，由 novel_context
// 的 Architect 路径注入，作为规划与设定生成的最高优先级参考。
type SettingsStore struct{ io *IO }

// NewSettingsStore 创建 SettingsStore，依赖同一目录的 IO 实例。
func NewSettingsStore(io *IO) *SettingsStore { return &SettingsStore{io: io} }

// SaveUserSettings 全量覆盖写入 user_settings.md（原子写）。
func (s *SettingsStore) SaveUserSettings(content string) error {
	return s.io.WriteMarkdown("user_settings.md", content)
}

// LoadUserSettings 读取设定全文；文件不存在返回空串。
func (s *SettingsStore) LoadUserSettings() (string, error) {
	data, err := s.io.ReadFile("user_settings.md")
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
