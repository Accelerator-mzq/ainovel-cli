package sim

import (
	"os"
	"path/filepath"
	"testing"
)

// 主画像扫描必须排除 personas/ 子树，否则人格语料会污染主画像。
func TestScanSourcesSkipsPersonasSubtree(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("main corpus"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "personas", "乌贼"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "personas", "乌贼", "p.txt"), []byte("persona corpus"), 0o644); err != nil {
		t.Fatal(err)
	}

	sources, err := scanSources(root)
	if err != nil {
		t.Fatalf("scanSources: %v", err)
	}
	if len(sources) != 1 || sources[0].RelativePath != "a.txt" {
		t.Fatalf("主画像扫描应只含根目录语料，got %+v", sources)
	}
}

func TestScanPersonaDirs(t *testing.T) {
	root := t.TempDir()
	// 两个人格目录：一个有语料、一个为空（空目录也要返回，由调用方告警跳过）
	if err := os.MkdirAll(filepath.Join(root, "personas", "乌贼"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "personas", "乌贼", "p.txt"), []byte("persona corpus"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "personas", "空人格"), 0o755); err != nil {
		t.Fatal(err)
	}
	// personas/ 下的散文件（非目录）应被忽略
	if err := os.WriteFile(filepath.Join(root, "personas", "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := scanPersonaDirs(root)
	if err != nil {
		t.Fatalf("scanPersonaDirs: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("应返回 2 个人格目录，got %d", len(dirs))
	}
	// 断言用名字查找避免依赖排序细节
	byAuthor := map[string]personaCorpus{}
	for _, d := range dirs {
		byAuthor[d.Author] = d
	}
	if got := byAuthor["乌贼"]; len(got.Sources) != 1 || got.Sources[0].RelativePath != "p.txt" {
		t.Fatalf("乌贼语料扫描错误: %+v", got)
	}
	if got := byAuthor["空人格"]; len(got.Sources) != 0 {
		t.Fatalf("空目录 Sources 应为空: %+v", got)
	}
}

// personas 是普通文件（非目录）时应跨平台一致返回 nil, nil。
// POSIX 上 os.ReadDir 对文件报 ENOTDIR（非 NotExist），若不在 Stat 层拦截会误报错中止流程。
func TestScanPersonaDirsPersonasIsFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "personas"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirs, err := scanPersonaDirs(root)
	if err != nil {
		t.Fatalf("personas 为文件时不应报错: %v", err)
	}
	if dirs != nil {
		t.Fatalf("应返回 nil，got %+v", dirs)
	}
}

// personas/ 目录不存在时返回 nil, nil（纯主画像场景）。
func TestScanPersonaDirsMissing(t *testing.T) {
	dirs, err := scanPersonaDirs(t.TempDir())
	if err != nil {
		t.Fatalf("缺 personas/ 目录不应报错: %v", err)
	}
	if dirs != nil {
		t.Fatalf("应返回 nil，got %+v", dirs)
	}
}
