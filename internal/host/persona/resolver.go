// internal/host/persona/resolver.go
package persona

import (
	"context"
	"fmt"
	"sort"

	"github.com/Accelerator-mzq/ainovel-cli/internal/domain"
	"github.com/Accelerator-mzq/ainovel-cli/internal/store"
)

// FuseFunc 把"主画像基底 + 人格画像变奏"融合为一份 synthesis。
// 注入以便测试与解耦具体 LLM（实际实现为 sim.FuseSynthesis 的闭包包装）。
type FuseFunc func(ctx context.Context, base, persona *domain.SimulationProfile) (*domain.SimulationSynthesis, error)

// Resolved 是竞稿写手装配所需的完整人格信息：身份（Author/Slug）+ 运行期唯一文风信号（Profile）。
type Resolved struct {
	Author   string
	Slug     string
	Profile  *domain.SimulationProfile
	Fallback bool // true 表示融合失败、Profile 为人格画像原样兜底
}

// MissingProfiles 返回 authors 中没有人格画像的作者名列表。
// build.go（subagent 注册）与 host.go（Dispatcher 路由）共用此谓词，
// 保证"缺画像 → 竞稿禁用"在两个装配入口上的判定一致。
func MissingProfiles(st *store.Store, authors []string) ([]string, error) {
	stored, err := st.Simulation.LoadPersonaProfiles()
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, a := range authors {
		if _, ok := stored[a]; !ok {
			missing = append(missing, a)
		}
	}
	return missing, nil
}

// AvailableProfiles 返回已生成人格画像的作者名列表（排序稳定输出），
// 供缺画像告警时一并展示——配置作者名与 ./simulate/personas/ 目录名拼写错位时，
// 用户对照两个列表即可定位。
func AvailableProfiles(st *store.Store) ([]string, error) {
	stored, err := st.Simulation.LoadPersonaProfiles()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(stored))
	for name := range stored {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// EnsureFused 返回 authors 对应的融合画像列表：缓存命中（主画像与人格画像
// UpdatedAt 均未变且非 Fallback）直接复用；否则调 fuse 重融合并写回缓存。
// 失败兜底人格画像原样 + Fallback 标记，不阻断（下次启动重试）。
// 调用方应先用 MissingProfiles 确认画像齐全。
func EnsureFused(ctx context.Context, st *store.Store, authors []string, fuse FuseFunc) ([]Resolved, error) {
	// 主画像读失败按"无主画像"退化处理：读错误 ≠ 永久损坏，退化融合优于阻断竞稿。
	base, _ := st.Simulation.Load()
	stored, err := st.Simulation.LoadPersonaProfiles()
	if err != nil {
		return nil, fmt.Errorf("load persona profiles: %w", err)
	}
	// 缓存损坏时静默重建（重新融合比阻断流程更合适，对齐旧 personas.json 策略）
	cache, _ := st.Simulation.LoadFusedProfiles()
	if cache == nil {
		cache = map[string]domain.FusedPersonaProfile{}
	}

	baseStamp := ""
	if base != nil {
		baseStamp = base.UpdatedAt
	}
	out := make([]Resolved, 0, len(authors))
	dirty := false

	for i, author := range authors {
		pp, ok := stored[author]
		if !ok {
			// 防御：调用方已查 MissingProfiles，此处命中说明启动期文件被外部改动
			return out, fmt.Errorf("persona profile missing for %q", author)
		}
		// slug 必须按当前 index 重算，与 Slugs() 完全一致（中文名 → persona{N}），
		// 否则重排配置会让 build.go 注册与 host.go 路由张冠李戴。
		slug := slugFor(author, i)

		if c, hit := cache[author]; hit && !c.Fallback && c.BaseStamp == baseStamp && c.PersonaStamp == pp.UpdatedAt {
			prof := c.Profile
			out = append(out, Resolved{Author: author, Slug: slug, Profile: &prof})
			continue
		}

		var resolved Resolved
		if base == nil {
			// 无主画像：融合退化为人格画像本身，不调 LLM（"贴近主画像"目标自动放宽）
			prof := pp
			resolved = Resolved{Author: author, Slug: slug, Profile: &prof}
		} else if syn, ferr := fuse(ctx, base, &pp); ferr != nil || syn == nil {
			prof := pp
			resolved = Resolved{Author: author, Slug: slug, Profile: &prof, Fallback: true}
			// ctx 取消/超时：剩余 author 的 fuse 全部立即失败属于"从未真正尝试"，
			// 不写 Fallback 进缓存（保留旧条目，避免把取消误持久化为融合失败），
			// 但 out 仍补全兜底结果——build.go 注册的 writer 数必须与
			// host.go SetContest 的 slug 数对齐，下次启动自动重试。
			if ctx.Err() != nil {
				out = append(out, resolved)
				continue
			}
		} else {
			fused := domain.SimulationProfile{
				Version:   domain.SimulationProfileVersion,
				CreatedAt: pp.CreatedAt,
				UpdatedAt: pp.UpdatedAt,
				Corpus:    pp.Corpus, // 沿用人格语料清单，compact 注入时 source_files 显示真实来源
				Synthesis: *syn,
			}
			resolved = Resolved{Author: author, Slug: slug, Profile: &fused}
		}
		out = append(out, resolved)
		cache[author] = domain.FusedPersonaProfile{
			BaseStamp:    baseStamp,
			PersonaStamp: pp.UpdatedAt,
			Fallback:     resolved.Fallback,
			Profile:      *resolved.Profile,
		}
		dirty = true
	}

	if dirty {
		if err := st.Simulation.SaveFusedProfiles(cache); err != nil {
			return out, fmt.Errorf("cache fused profiles: %w", err)
		}
	}
	return out, nil
}
