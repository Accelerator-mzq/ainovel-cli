package sim

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
)

type scannedSource struct {
	domain.SimulationSource
	absPath string
	content string
}

// personaCorpus 是 personas/<作者名>/ 子目录扫出的人格语料。
type personaCorpus struct {
	Author  string
	Dir     string
	Sources []scannedSource
}

// personasDirName 是保存各人格语料的子目录名，主画像扫描时跳过此目录。
const personasDirName = "personas"

func scanSources(root string) ([]scannedSource, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("source dir is required")
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("simulate directory not found: %s", root)
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("simulate path is not a directory: %s", root)
	}

	var out []scannedSource
	personasRoot := filepath.Join(root, personasDirName)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			// personas/ 子树是人格语料，由 scanPersonaDirs 单独扫，不混入当前画像
			if path == personasRoot {
				return filepath.SkipDir
			}
			return nil
		}
		if !isSupportedSource(path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		sum := sha256.Sum256(data)
		sha := hex.EncodeToString(sum[:])
		out = append(out, scannedSource{
			SimulationSource: domain.SimulationSource{
				RelativePath: rel,
				SHA256:       sha,
				Fingerprint:  domain.SimulationSourceFingerprint(rel, sha),
				SizeBytes:    info.Size(),
				ModTime:      info.ModTime().Format(time.RFC3339),
			},
			absPath: path,
			content: string(data),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].RelativePath < out[j].RelativePath
	})
	return out, nil
}

// scanPersonaDirs 扫描 root/personas/ 下的作者子目录，返回按作者名排序的人格语料列表。
// personas/ 不存在返回 nil, nil；空子目录也会返回（Sources 为空），由调用方告警跳过。
func scanPersonaDirs(root string) ([]personaCorpus, error) {
	personasRoot := filepath.Join(strings.TrimSpace(root), personasDirName)
	entries, err := os.ReadDir(personasRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []personaCorpus
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(personasRoot, e.Name())
		sources, err := scanSources(dir)
		if err != nil {
			return nil, err
		}
		out = append(out, personaCorpus{Author: e.Name(), Dir: dir, Sources: sources})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Author < out[j].Author })
	return out, nil
}

func isSupportedSource(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".markdown":
		return true
	default:
		return false
	}
}
