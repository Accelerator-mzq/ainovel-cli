package store

import "testing"

// TestUserSettings_SaveLoad 验证设定全文的落盘与读取。
func TestUserSettings_SaveLoad(t *testing.T) {
	s := NewStore(t.TempDir())
	// 未保存时返回空串不报错
	if got, err := s.Settings.LoadUserSettings(); err != nil || got != "" {
		t.Fatalf("empty load = %q, %v", got, err)
	}
	content := "## 文件：世界观.md\n\n修炼境界：练气 → 筑基\n"
	if err := s.Settings.SaveUserSettings(content); err != nil {
		t.Fatal(err)
	}
	got, err := s.Settings.LoadUserSettings()
	if err != nil || got != content {
		t.Fatalf("load = %q, %v", got, err)
	}
	// 覆盖写
	if err := s.Settings.SaveUserSettings("v2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Settings.LoadUserSettings(); got != "v2" {
		t.Fatalf("overwrite load = %q", got)
	}
}
