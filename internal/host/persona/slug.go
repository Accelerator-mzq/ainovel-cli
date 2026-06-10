// internal/host/persona/slug.go
package persona

import (
	"fmt"
	"strings"
	"unicode"
)

// slugFor 生成稳定 slug：纯 ASCII 作者名转小写（空格转连字符），
// 含非 ASCII（中文等）则回退 persona{序号}，保证唯一稳定。
func slugFor(author string, index int) string {
	ascii := true
	for _, r := range author {
		if r > unicode.MaxASCII {
			ascii = false
			break
		}
	}
	if !ascii {
		return fmt.Sprintf("persona%d", index+1)
	}
	// 非字母数字一律转连字符，折叠连续连字符并去除首尾，避免污染文件路径
	out := make([]rune, 0, len(author))
	prevHyphen := false
	for _, r := range author {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, unicode.ToLower(r))
			prevHyphen = false
		} else if !prevHyphen {
			out = append(out, '-')
			prevHyphen = true
		}
	}
	slug := strings.Trim(string(out), "-")
	if slug == "" {
		slug = fmt.Sprintf("persona%d", index+1) // 全特殊字符兜底
	}
	return slug
}

// Slugs 把作者名列表转为稳定 slug 列表（与 EnsureFused 一致）。
// host.go 使用此函数推导 agent 命名，必须与融合画像路由完全一致。
func Slugs(authors []string) []string {
	out := make([]string, len(authors))
	for i, a := range authors {
		out[i] = slugFor(a, i)
	}
	return out
}
