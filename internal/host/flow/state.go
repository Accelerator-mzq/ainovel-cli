package flow

import (
	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// LoadState 从 Store 读取 Route 所需的全部事实。
// 这是路由的"IO 边界"：所有读取集中在这里，Route 保持纯。
// 读取失败按保守默认填充（has*=false, boundary=nil），让 Router 倾向重派而非跳过。
func LoadState(store *storepkg.Store) State {
	s := State{
		FoundationMissing: store.FoundationMissing(),
	}
	progress, err := store.Progress.Load()
	if err != nil || progress == nil {
		return s
	}
	s.Progress = progress

	if n := len(progress.CompletedChapters); n > 0 {
		s.LastCompleted = progress.CompletedChapters[n-1]
	}

	// 弧边界仅在分层模式且有已完成章节时才计算
	if progress.Layered && s.LastCompleted > 0 {
		if boundary, berr := store.Outline.CheckArcBoundary(s.LastCompleted); berr == nil && boundary != nil {
			s.ArcBoundary = boundary
			if boundary.IsArcEnd {
				s.HasArcReview = store.World.HasArcReview(s.LastCompleted)
				s.HasArcSummary = store.Summaries.HasArcSummary(boundary.Volume, boundary.Arc)
				if boundary.IsVolumeEnd {
					s.HasVolumeSummary = store.Summaries.HasVolumeSummary(boundary.Volume)
				}
			}
		}
	}

	return s
}

// ContestConfig 是 LoadStateWithContest 需要的竞稿静态配置（来自 bootstrap.Config 解析后的 slug 列表）。
type ContestConfig struct {
	Personas []string // persona slug，顺序即写作顺序；len<2 视为未启用
}

// LoadStateWithContest 在 LoadState 基础上补齐竞稿事实。
// 非竞稿场景（cfg.Personas<2）等价于原 LoadState。
func LoadStateWithContest(store *storepkg.Store, cfg ContestConfig) State {
	s := LoadState(store)
	if len(cfg.Personas) < 2 {
		return s
	}
	s.ContestEnabled = true
	s.Personas = cfg.Personas

	if s.Progress == nil || s.Progress.Phase != domain.PhaseWriting {
		return s
	}
	// 只在"正常续写"语义下编排竞稿：有重写队列/审阅/弧末后处理时不介入。
	next := s.Progress.NextChapter()
	if next <= 0 {
		return s
	}
	s.ContestChapter = next
	if ready, err := store.Contest.ListCandidates(next, cfg.Personas); err == nil {
		s.CandidatesReady = ready
	}
	if v, err := store.Contest.LoadVerdict(next); err == nil && v != nil {
		s.HasVerdict = true
		s.VerdictWinner = v.Winner
		s.IsPromoted = v.Promoted
	}
	return s
}
