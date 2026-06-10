// internal/host/persona/slug_test.go
package persona

import (
	"reflect"
	"strings"
	"testing"
)

func TestSlugFor(t *testing.T) {
	cases := []struct {
		author string
		index  int
		want   string
	}{
		{"Brandon Sanderson", 0, "brandon-sanderson"}, // ASCII：小写 + 连字符
		{"乌贼", 0, "persona1"},                         // 中文：index 回退
		{"乌贼", 2, "persona3"},                         // index 相关
		{"a..b", 0, "a-b"},                            // 特殊字符折叠
		{"!!!", 1, "persona2"},                        // 全特殊字符兜底
	}
	for _, c := range cases {
		if got := slugFor(c.author, c.index); got != c.want {
			t.Errorf("slugFor(%q, %d) = %q, want %q", c.author, c.index, got, c.want)
		}
	}
}

func TestSlugsMatchesIndexOrder(t *testing.T) {
	got := Slugs([]string{"乌贼", "肘子"})
	want := []string{"persona1", "persona2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Slugs = %v, want %v", got, want)
	}
}

func TestSlugs_ChineseAndASCII(t *testing.T) {
	// 中文作者名 → personaN 序号；ASCII → 小写
	got := Slugs([]string{"乌贼", "Brandon Sanderson"})
	if got[0] != "persona1" {
		t.Fatalf("中文 slug = %q, want persona1", got[0])
	}
	if got[1] != "brandon-sanderson" {
		t.Fatalf("ASCII slug = %q, want brandon-sanderson", got[1])
	}
}

func TestSlugs_FiltersPathUnsafeChars(t *testing.T) {
	// 路径不安全字符（点、斜杠、反斜杠）必须被折叠为连字符或兜底 slug
	got := Slugs([]string{"J.R.R. Tolkien", "a/b\\c"})
	for _, s := range got {
		for _, bad := range []string{".", "/", "\\"} {
			if strings.Contains(s, bad) {
				t.Fatalf("slug %q 含路径不安全字符 %q", s, bad)
			}
		}
	}
}
