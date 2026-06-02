// internal/store/contest.go
package store

import (
	"fmt"
	"os"
)

// ContestStore 管理多人格竞稿的候选稿与裁定文件。
type ContestStore struct{ io *IO }

func NewContestStore(io *IO) *ContestStore { return &ContestStore{io: io} }

// candPath 返回某章节某 persona 候选稿的相对路径。
func candPath(chapter int, persona string) string {
	return fmt.Sprintf("drafts/%02d.cand-%s.md", chapter, persona)
}

// SaveCandidate 保存某 persona 的候选稿。
func (s *ContestStore) SaveCandidate(chapter int, persona, content string) error {
	return s.io.WriteMarkdown(candPath(chapter, persona), content)
}

// LoadCandidate 读取某 persona 的候选稿；不存在返回空串。
func (s *ContestStore) LoadCandidate(chapter int, persona string) (string, error) {
	data, err := s.io.ReadFile(candPath(chapter, persona))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ListCandidates 返回给定 persona 列表中已落盘候选稿的存在性映射。
// 不存在的 persona 映射值为 false，确保返回 map 包含全部请求的 persona。
func (s *ContestStore) ListCandidates(chapter int, personas []string) (map[string]bool, error) {
	present := make(map[string]bool, len(personas))
	for _, p := range personas {
		c, err := s.LoadCandidate(chapter, p)
		if err != nil {
			return nil, err
		}
		present[p] = c != ""
	}
	return present, nil
}
