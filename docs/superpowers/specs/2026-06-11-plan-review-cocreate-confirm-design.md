# 设计：规划审阅门禁 + 共创开始确认

日期：2026-06-11
状态：已与用户对齐，待实现

## 背景与动机

两个独立但相邻的交互缺口：

1. **规划期 → 写作期没有审稿停顿点**。引擎规划完成（premise → characters →
   world_rules → layered_outline → compass）后自动开写第 1 章。用户想先审阅
   `layered_outline.md`、确认或修改后再开写，目前只能掐时机手动 Esc 暂停——
   时机难掌握，且第 1 章可能已派发。
2. **共创模式收口只有 Ctrl+S 一个入口**。共创协议已有 `<ready>` 标记
   （AI 认为信息足够），但 TUI 只把它渲染成灰色 placeholder 提示
   （`internal/entry/tui/cocreate.go:55`），用户在对话里说"开始吧"没有任何响应，
   必须知道并记得按 Ctrl+S。

两功能共同的设计哲学：**关键转换点交给用户显式确认，但确认的时机由系统主动创造，
不靠用户掐点或记快捷键**。

## 已确认的关键决策

| 决策点 | 结论 |
|---|---|
| A 审阅时机 | **仅首次开书规划完成**。后续弧/卷展开不暂停（可随时干预），避免挂机长跑被反复打断 |
| A 确认交互 | **文本语义区分 + 修改后再审循环**：确认词精确匹配 → 开写；其他文本 → 干预改大纲 → 改完再次暂停待审，循环直到明确确认 |
| A 默认行为 | **TUI 默认开，headless 默认跳过**；配置 `plan_review: "auto"/"on"/"off"` 可覆盖两端 |
| B 触发语义 | **双档**：`<ready>`（信息足够）仅升级高亮提示条；新增 `<start_intent>`（用户本轮明确要求开始）才弹确认模态 |
| B 确认交互 | 模态 `[Enter] 确认开写 / [Esc] 返回继续共创`；模态打开期间按键只归模态；Ctrl+S 原路径保留 |
| 交付形态 | 一个分支一个 PR，A/B 各一组独立提交（均可单独 revert），TDD 全程 |

---

## A. 规划审阅门禁（plan review gate）

### A1. 配置（`internal/bootstrap/config.go` + `config.example.jsonc`）

顶层新增字段：

```jsonc
// 规划完成后暂停等待用户审阅大纲："auto"（TUI 开/headless 关，默认）、"on"、"off"
"plan_review": "auto"
```

- 解析为 `PlanReview string`，空值视为 `"auto"`，非法值报配置错误
- 生效判定：`EffectivePlanReview(interactive bool) bool`
  - `"auto"` → `interactive`。**该布尔由入口层装配 Host 时显式传入**（TUI 入口传 true、
    headless 入口传 false），不复用 `startup.Request.Interactive`（headless 的
    PrepareQuick 也置 true，语义不同）
  - `"on"` → true；`"off"` → false

### A2. 状态落盘（`internal/domain/runtime.go` + `internal/store/progress.go`）

`Progress` 新增字段：

```go
PlanReviewed bool `json:"plan_reviewed,omitempty"` // 用户已确认大纲，规划审阅门禁放行
```

判定为 domain 纯函数（与死亡判定 `IsDeadValue` 同风格，表驱动可测）：

```go
// PlanReviewPending 规划已完成但用户尚未确认大纲，且写作尚未实际开始。
func PlanReviewPending(p *Progress) bool {
    return p != nil && p.Phase == PhaseWriting && !p.PlanReviewed &&
        p.CurrentChapter == 0 && p.InProgressChapter == 0 && len(p.CompletedChapters) == 0
}
```

- **旧书兼容**：已有任何章节进度 → 永不 pending，行为零变化
- **崩溃恢复免费获得**：pending 由 progress.json 派生。规划完成后崩溃/Ctrl+C，
  重启 TUI 自动回到待审阅暂停态，不会绕过审阅
- `Store.Progress` 新增 `MarkPlanReviewed()`（读改写落盘，原子写入沿用现有 IO）

### A3. 门禁（新文件 `internal/host/planreview.go`，仿 `budget.go` guard 模式）

```go
type planReviewGuard struct {
    mu       sync.Mutex
    pending  func() bool   // 闭包读 store.Progress + domain.PlanReviewPending
    emit     func(Event)
    abort    func()        // h.Abort()
    notify   func()        // entry 层回调（TUI 刷新 / headless 起输入循环）
    prompted bool          // 同一 pending 期只提示一次
}

func (g *planReviewGuard) Allow() bool
```

- `Allow()` 返回 `!pending()`；首次拦截（prompted 翻转）时：
  `emit(SYSTEM "规划完成·大纲待审阅，已暂停派发" info)` + `abort()` + `notify()`
- `notify` 主要供 headless 起 stdin 输入循环；TUI 不依赖它（靠 Snapshot 轮询
  `PlanReviewPending` 切换展示），TUI 装配时可传 nil
- 用户确认后 pending 变 false，`prompted` 复位（支持理论上的多次进入）
- **门禁组合**：`Dispatcher.SetGate` 是单槽（`dispatcher.go:35` 装配期一次），新增

```go
func composeGates(gates ...func() bool) func() bool // 全部放行才放行，nil 跳过
```

  host.go 装配期把 budget guard 与 plan-review guard 组合后 `SetGate` 一次，
  保持"装配期只调一次、运行期只读"的现有并发约定。

### A4. 审阅输入处理（`internal/host`）

```go
// HandleReviewInput 处理待审阅状态下的用户输入。
// 确认词 → 落盘 PlanReviewed + Resume，返回 true；
// 其他文本 → 走 Steer 干预管道（改大纲），返回 false。
func (h *Host) HandleReviewInput(text string) (approved bool)
```

- 确认词集合精确匹配（TrimSpace 后全等）：`开始`、`确认`、`开写`、`开始写作`
- **修改后再审循环零状态机**：Steer 的干预被 Coordinator 处理（可能改
  layered_outline），处理完引擎再次尝试派发写作 → `Allow()` 依然拦截 →
  再次暂停 + 提示。循环天然成立，直到确认词出现
- `UISnapshot` 新增 `PlanReviewPending bool`（Snapshot 时由 progress 派生）

### A5. TUI（`internal/entry/tui`）

- `snapshot.PlanReviewPending` 时：
  - 状态栏显示 `⏸ 规划完成·待审阅`
  - placeholder：`已生成 output/{书名}/layered_outline.md · 输入修改意见，或输入「开始」进入写作`
  - 输入提交路由改走 `HandleReviewInput`（替代普通的暂停恢复注入）
- 其余暂停（Esc/预算）展示不变

### A6. headless（`internal/entry/headless`）

- `auto`（默认）→ 门禁不启用，现有自动化行为零变化
- `on` → guard 的 `notify` 回调触发后：stderr 打印审阅提示（含 layered_outline.md
  路径与确认词说明），从 stdin 逐行读取喂 `HandleReviewInput`，直到 approved
  （复用 `terminalAskUser` 同款 bufio 读取，注意与 ask_user 的 stdin 互斥）

### A7. 错误处理

- `MarkPlanReviewed` 落盘失败：记日志 + 本次仍放行（Resume）。fail-safe 方向是
  "重启后多问一次"，绝不卡死创作
- 门禁启用但 progress 读取失败：视为不 pending（放行），与现有 store 读失败的
  宽松处理一致

---

## B. 共创开始意图确认

### B1. 协议（`internal/host/cocreate.go`）

- 系统提示词四标签扩五标签：`<reply>/<draft>/<ready>/<suggestions>` + `<start_intent>`
- prompt 语义约束（写进 `coCreateSystemPrompt`）：
  - 仅当用户**本轮明确要求立即开始创作**（如「开始吧」「可以了，开写」）置 `true`
  - 用户仅表达满意但未要求开始 → `false`
  - 否定语境（「先别开始」「还不要开始」）→ 必须 `false`
  - `start_intent=true` 时 `ready` 必须同为 `true`
- `splitCoCreateMarkers` 增加 `start_intent` 解析，容错规则与 `<ready>` 同款
  （无开有闭 typo 修复、缺标签视为 false）
- `CoCreateReply` 新增 `StartIntent bool`；`startup.CoCreateSession` 每轮覆盖透传
  （与 suggestions 同生命周期：用户一发言即作废）

### B2. TUI（`internal/entry/tui/cocreate.go` + `model*.go`）

- 触发条件：`reply.StartIntent && state.canStart()`（草稿非空）→ 弹确认模态
  （渲染仿现有 `renderAskUserModal` 系列）：

```
┌──────────────────────────────┐
│ 检测到开始意图                │
│ 即将以右侧草稿进入正式创作    │
│ [Enter] 确认开写              │
│ [Esc]   返回继续共创          │
└──────────────────────────────┘
```

- 模态打开期间按键只归模态：Esc 关闭模态（**不**触发共创退出）；Enter 走启动链路
- **启动链路单一来源**：从现有 Ctrl+S 处理分支抽出共享方法
  `startCreationFromCoCreate()`（`BuildPlan` → `AppendCoCreateTranscript` →
  `StartPrepared`），Ctrl+S 与模态 Enter 共用，不二写
- Esc 关闭后**本轮不再弹**；下一轮回复再带 start_intent 再弹
- `StartIntent=true` 但草稿为空 → 不弹模态，正常显示回复
- `ready=true` 的提示条从灰色 placeholder 升级为高亮：
  `✨ 信息已足够 · Ctrl+S 或对 AI 说「开始」即可进入创作`
- Ctrl+S 原路径保留不动

---

## 测试策略

| 层 | 用例 |
|---|---|
| domain | `PlanReviewPending` 表驱动：新书规划完成/已确认/有章节进度/Phase 非 writing/nil |
| host | guard `Allow` 拦截与首次副作用（emit+abort+notify 恰好一次）；`HandleReviewInput` 确认词/修改意见两分支；`composeGates` 组合语义 |
| bootstrap | `plan_review` 三态解析 + 非法值报错 + `EffectivePlanReview` 四象限 |
| store | `MarkPlanReviewed` 落盘读回 |
| cocreate | `<start_intent>` 解析：存在/缺失/typo 容错/与 ready 联动 |
| startup | session `StartIntent` 透传与覆盖时机 |
| tui | 模态触发条件（intent+canStart）、Esc 一次性 dismiss、Enter 与 Ctrl+S 链路等价（UserNotes/StartPrompt 一致）；审阅态 placeholder 与输入路由 |

## 文档更新

- `docs/user-guide.md` 2.2（创作流程加"规划完成·待审阅"环节）、2.3（确认词说明）、
  共创开局表格（说「开始」即可）、FAQ（如何跳过审阅 → `plan_review: "off"`）
- `config.example.jsonc` 注释 `plan_review` 三态

## 提交序列（计划阶段细化）

- A：domain 判定 + store 落盘 → config 三态 → host guard + composeGates + HandleReviewInput → TUI 审阅态 → headless on 支持 → docs
- B：协议解析 + session 透传 → TUI 模态 + 链路抽取 → docs
