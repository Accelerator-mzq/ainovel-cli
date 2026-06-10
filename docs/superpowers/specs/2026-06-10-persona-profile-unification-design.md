# 设计：画像驱动的竞稿人格（替代 StyleBlock 机制）

日期：2026-06-10
状态：已与用户对齐，待实现

## 背景与动机

当前项目存在两套独立的"文风信号"机制：

1. **仿写画像（simulation）**：用户导入目标作品原文语料，经 LLM 逐篇分析 + synthesis 合并，
   产出结构化画像（`meta/simulation_profile.json`），由 `novel_context` 注入所有写作/规划角色。
   语义是**收敛**（全书贴近目标文风）。
2. **竞稿人格（persona StyleBlock）**：仅凭 `writing_contest.personas` 里的作者名，
   调 LLM 凭先验知识生成 ~150 字文风描述，拼进竞稿写手 `writer_<slug>` 的 system prompt
   （`internal/agents/build.go` 的 `personaPrompt` 拼接）。语义是**发散**（候选稿拉开区分度）。

两者叠加时竞稿写手同时携带两个文风信号（`assets/simulation_contest_test.go` 中
`TestPersonaWriterCarriesBothSignals` 验证的"质量软冲突"）。

用户确认的四个动机（全选）：

- **人格不够准**：凭作者名让 LLM 想象文风不可靠，中腰部作者会产生"虚假精确"的幻觉画像；
- **两信号冲突**：画像收敛 + 人格发散互相干扰，想统一信号源；
- **配置太复杂**：两套机制概念重叠，想收敛为一套；
- **候选贴近画像**：竞稿候选稿应贴近主画像文风方向，同时保留多候选选优。

## 已确认的关键决策

| 决策点 | 结论 |
|---|---|
| 人格信号来源 | **必须语料**——每个竞稿人格必须有对应语料生成的画像；作者名降级路径**不做**（轻画像 ≈ 结构化 StyleBlock，准确性无质变，且结构化字段会放大"虚假精确"风险） |
| 运行期信号形态 | **生成期融合，运行期单信号**——装配时把"主画像基底 + 人格画像变奏"LLM 融合为一份人格专属画像并缓存；运行期每个竞稿写手只看自己的融合画像 |
| 语料组织 | **目录约定**——`./simulate/` 根 → 主画像（现状）；`./simulate/personas/<作者名>/` → 该人格画像；配置格式零新增 |
| 注入通道 | **统一走 novel_context**——竞稿写手获得专属 ContextTool 实例，返回各自融合画像作为 `simulation_profile`；现有"## 仿写画像"prompt 指导段零修改直接适配 |

## ① 数据与存储

```
meta/simulation_profile.json        主画像（现状不动）
meta/simulation_personas.json       新增：map[作者名]SimulationProfile，人格画像
meta/contest_fused_profiles.json    新增：map[作者名]{baseStamp, personaStamp, profile, fallback}
contest/personas.json               废弃（不再读写，用户可手动删除）
```

- 人格画像 key 用**作者名而非 slug**：slug 对中文名是 `persona{序号}`（index 相关），
  重排配置会错位；作者名才是稳定身份（见 `internal/host/persona/generator.go:41-44`
  注释记录的历史教训）。
- 融合缓存失效键 = 主画像 `UpdatedAt` + 人格画像 `UpdatedAt`，任一变化该人格自动重融合。
  无主画像时 `baseStamp` 为空串，同样参与失效判断。

## ② 生成与融合流程

**人格画像生成**（重活，只在 `/simulate` 命令中显式触发，有进度 UI）：

- `internal/host/sim/scanner.go` 扩展：`./simulate/` 根文件 → 主画像（现状）；
  `./simulate/personas/<作者名>/` 子目录 → 该作者人格画像。
- 同一条 `/simulate` 命令全量增量扫，按语料指纹（SHA256/Fingerprint）跳过未变更篇目，
  机制复用现有 corpus manifest。
- 分析管线完全复用现有 source report → synthesis 流程，无新分析逻辑。

**融合**（轻活，启动装配时按需执行，缓存命中则零 LLM 调用）：

- 新增融合 prompt：`assets/prompts/simulation-persona-fuse.md`。
  输入主画像与人格画像的 compact synthesis，输出融合 synthesis JSON。
- 融合规则：**人格画像主导风格层**（narrative_voice / sentence_rhythm / mood / lexicon），
  **主画像主导结构层**（plot_design / hook_design / pacing_density / reader_engagement
  以主画像为基底、人格特征调味）。
- 融合 LLM 调用复用 writer role 模型（与被删除的 generatePersonaStyle 一致，不新增模型配置）。
- 无主画像 → 融合退化为人格画像本身（不调 LLM）。
- 融合 LLM 调用失败 → 兜底直接用人格画像 + `fallback` 标记 + 日志警告，不阻断启动，
  下次启动重试（沿用现有 60s 总超时模式，对齐 `build.go` 现有 EnsurePersonas 超时处理）。

## ③ 装配与注入

`internal/agents/build.go` 竞稿装配段改造：

- 对 `writing_contest.personas` 逐个查人格画像：**任一缺失 → 竞稿整体禁用**
  （回退单 writer 流程），启动事件明确提示缺哪些 + 修复路径
  （"放语料到 ./simulate/personas/<名>/，运行 /simulate，重启生效"）。
  禁用而非拒绝启动：首次配置时用户必须能进 TUI 跑 `/simulate`（鸡生蛋问题）。
- 画像齐全 → 确保融合缓存 → 每个竞稿写手获得**专属 ContextTool 实例**：
  `NewContextTool` 增加 option（如 `WithProfileSource(func() (*domain.SimulationProfile, error))`），
  其 `buildSimulationProfile` 返回该人格的融合画像；其余角色的共享实例照旧返回主画像。
- 竞稿写手 `SystemPrompt = writerPrompt` 原样——`"## 你的写作人格"` 拼接删除。
  `assets/load.go` 里现成的"## 仿写画像"指导段语义完全适配，零 prompt 修改。
- Judge / draft_persona / 候选与润色两阶段 StopGuard / slug 机制（agent 命名、候选稿路径）
  全部不动。

运行期信号图（最终态）：

```
writer_A  → novel_context → 融合画像_A   ← 单一信号
writer_B  → novel_context → 融合画像_B   ← 单一信号
architect / editor / coordinator → 主画像（现状）
judge → 无画像（现状，刻意排除）
```

## ④ 删除项与兼容

| 删除 | 位置 |
|---|---|
| `generatePersonaStyle`（凭作者名生成文风） | `internal/agents/build.go` |
| `personaPrompt` 拼接（`"## 你的写作人格"`） | `internal/agents/build.go` |
| `persona.Generator` 的 StyleBlock 生成与兜底文案 | `internal/host/persona/generator.go` 重构为 Resolver（保留 `slugFor`/`Slugs`） |
| `domain.Persona.StyleBlock` 字段 | `internal/domain/cast.go` |
| `TestPersonaWriterCarriesBothSignals` | 改写为反向断言：竞稿写手 prompt **不含**人格块 |

- 配置格式**零变化**：`writing_contest.personas` 仍是作者名列表。
- `contest/personas.json` 不再读写，旧 StyleBlock 缓存自然失效。
- **上游同步热点标注**：本设计触碰 `internal/agents/build.go`、
  `internal/tools/novel_context*.go`、`internal/host/sim/` 包，
  均为后续 fork 合并上游时的冲突热点，合并时需人工核对。

## ⑤ 错误处理汇总

- 配置了人格但无对应语料目录/画像 → 竞稿禁用 + 列出缺失项与现有目录名（防拼写错位）。
- `personas/<名>/` 目录存在但无有效语料 → `/simulate` 警告跳过，等同缺画像。
- 有人格画像、无主画像 → 正常竞稿，融合退化（"贴近主画像"目标自动放宽）。
- 融合失败 → 人格画像兜底 + fallback 标记，不阻断。

## ⑥ 测试策略

- **单测**：scanner 子目录扫描与增量指纹；融合缓存命中/失效/兜底
  （fuse func 注入，仿现有 `StyleGenFunc` 模式）；缺画像禁用竞稿。
- **注入正确性**：per-persona ContextTool 返回各自融合画像、不串台
  （防"张冠李戴"回归，对应 slug 重排的历史教训）。
- **assets 集成**：竞稿写手 prompt 无人格块；judge 仍无画像指导。
- **e2e**：竞稿全流程真实 LLM 冒烟（沿用现有竞稿 e2e 通道）。
