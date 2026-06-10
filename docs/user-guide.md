# ainovel-cli 使用手册

面向**用小说引擎写书的用户**的完整说明书：从安装配置、写完一本书的全流程，到命令参考、配置项全量说明、规则定制、产物结构和常见问题排障。

> 想了解内部架构与设计理念，请看 [README](../README.md) 与 [docs/architecture.md](architecture.md)；本手册只讲"怎么用"。

## 目录

1. [安装与首次配置](#1-安装与首次配置)
2. [写一本书：完整流程](#2-写一本书完整流程)
3. [TUI 命令参考](#3-tui-命令参考)
4. [配置参考](#4-配置参考)
5. [写作规则定制（rules.md）](#5-写作规则定制rulesmd)
6. [输出目录与文件含义](#6-输出目录与文件含义)
7. [写作过程中的机械告警](#7-写作过程中的机械告警)
8. [FAQ 与排障](#8-faq-与排障)

---

## 1. 安装与首次配置

### 安装

```bash
# 方式一：go install
go install github.com/Accelerator-mzq/ainovel-cli/cmd/ainovel-cli@latest

# 方式二：源码运行
git clone https://github.com/Accelerator-mzq/ainovel-cli.git
cd ainovel-cli && go run ./cmd/ainovel-cli
```

### 首次引导

直接运行 `ainovel-cli`，首次会自动进入引导流程：**选择 Provider → 输入 API Key → Base URL → 模型名**，完成后生成全局配置 `~/.ainovel/config.json`（同时生成带注释的 `config.example.jsonc` 供参考）。

- 删除 `~/.ainovel/config.json` 后再运行会重新进入引导。
- 不想走引导也可以手动创建配置文件，字段见[第 4 节](#4-配置参考)。

### 配置文件查找顺序（后者覆盖前者）

1. `~/.ainovel/config.json` — 全局配置
2. `./ainovel.json` — 项目级覆盖（可选，放在启动目录）
3. `--config path/to/config.json` — 命令行指定

覆盖规则：标量字段（`provider` / `model` / `style`）后者直接覆盖；`providers` / `roles` 按 key 合并、同名项内部按字段覆盖；`writing_contest` / `budget` 在 overlay 配置了内容时整体覆盖。**不支持用空字符串清空上层已有值**，要清空请直接改更高优先级的文件。

---

## 2. 写一本书：完整流程

### 2.1 启动

在你想存放这本书的目录里运行 `ainovel-cli`。**每本小说绑定启动目录**：产物落在 `{cwd}/output/{书名}/`，换目录启动 = 写另一本，`cd` 回去再启动 = 自动从断点恢复。

进入 TUI 后有两种开局方式：

| 方式 | 适合场景 | 操作 |
|---|---|---|
| **快速开始** | 想法明确 | 一句话需求直接进入创作（如"写一部克苏鲁风格的都市悬疑"） |
| **共创规划** | 想法模糊 | 与 AI 多轮对话澄清需求，右侧实时同步整理出的创作指令草稿；AI 每轮给 1-3 条建议，按数字键一键填入；满意后 `Ctrl+S` 进入正式创作 |

两种方式最终收敛为同一份创作指令，进入同一套引擎。

### 2.2 创作过程中会发生什么

启动后系统全自动运行，大致节奏：

1. **规划期**：Architect 生成故事前提 → 角色档案 → 世界规则 → 大纲。长篇模式只规划前 2 卷骨架 + 第 1 弧详细章节（卷弧滚动规划，写到了再展开）。
2. **写作期**：Writer 逐章完成 构思 → 写正文 → 一致性自查 → 提交。每章提交后系统自动维护摘要、时间线、伏笔台账、角色状态。
3. **弧末/卷末**：Editor 做七维评审（设定一致性/角色行为/节奏/叙事连贯/伏笔/钩子/审美），评分不过线的章节进入重写或打磨队列；随后生成弧/卷摘要，Architect 展开下一弧或创建下一卷。
4. **完结**：规划的章节全部写完、活跃伏笔归零、指南针长线收束后自动收尾。

TUI 上可观察：当前阶段（规划/写作/完成）、活跃流程（写作/评审/重写/打磨/干预处理）、上下文健康度（绿 <70% / 黄 70-85% / 红 >85%）、token 用量与成本。

### 2.3 中途干预（不需要暂停）

创作启动后，底部输入框就是干预入口，随时输入然后回车：

```
❯ 把感情线提前到第4章，增加男女主的对手戏
```

系统会记录指令（崩溃恢复也不丢）、注入正在运行的 Coordinator，由它评估影响范围——可能修改设定、重写已有章节、或只调整后续大纲。

| 干预示例 | 系统可能的响应 |
|---|---|
| "主角改成女性" | 修改角色设定，评估已写章节是否需要重写 |
| "加入一个反派角色" | 更新角色档案和世界规则，在后续章节引入 |
| "节奏太慢了，加快推进" | 调整后续章节的大纲密度 |

如果系统已停机（暂停/完结后），输入框文本会作为"继续指令"注入并自动恢复运行。

### 2.4 中断与恢复

崩溃、断网、`Ctrl+C` 都不要紧：**同一目录再次运行即自动恢复**，精确到步骤级（"第 7 章草稿已落盘 → 继续一致性检查"这种粒度）。文件写入是原子操作（temp + fsync + rename），断电也不会损坏已有数据。

| 中断时机 | 恢复行为 |
|---|---|
| 规划阶段 | 检查已保存设定，补全缺失项 |
| 某章写到一半（草稿未提交） | 读已有草稿从该章续写 |
| 评审进行中 | 重新触发 Editor 评审 |
| 重写/打磨队列未清空 | 继续处理队列 |
| 弧/卷展开中断 | 自动触发 Architect 展开 |
| 用户干预未完成 | 重新注入上次的干预指令 |
| 竞稿中途（候选/裁定写了一半） | 按磁盘上的候选稿/裁定文件继续，幂等 |

**重新开始**：删除 `output/{书名}/` 目录即可（不可恢复，删前可先 `/export` 留份成稿）。

### 2.5 拿到成稿

写作中途或完结后，`/export` 随时导出当前已完成章节（只读操作，不影响写作）。详见[第 3 节](#3-tui-命令参考)。

---

## 3. TUI 命令参考

| 命令 | 语法 | 何时可用 | 作用 |
|---|---|---|---|
| `/help` | `/help` | 随时 | 查看命令列表 |
| `/model` | `/model [role]` | 随时 | 切换默认模型或某角色（coordinator/architect/writer/editor）的模型 |
| `/diag` | `/diag` | 随时 | 诊断当前小说健康度，输出发现+证据+建议 |
| `/export` | `/export [path] [from=N] [to=M] [--overwrite]` | 随时 | 导出已完成章节为 TXT/EPUB |
| `/import` | `/import <path> [from=N] [regex=...]` | 仅空闲 | 反推导入一本已有小说并自动接力续写 |
| `/simulate` | `/simulate` | 仅空闲 | 读取 `./simulate/` 语料生成/增量更新仿写画像 |
| `/importsim` | `/importsim <profile.json>` | 仅空闲 | 导入已有仿写画像，按语料指纹合并 |

"仅空闲"指创作运行中不可执行，需等暂停或本轮任务结束。

### /model — 切换模型

`/model` 打开模型面板切默认模型；`/model writer` 只切 Writer 的模型。可选模型列表来自配置里 `providers.<name>.models`（未配置则回退为配置文件中出现过的该 provider 模型）。切换即时生效，后续调用按新模型计费与计窗口。

### /diag — 健康诊断

对 `output/` 产物做四个维度的体检：

- **流程**：改写循环卡顿、未消费的转向指令、章节跳号
- **质量**：评审维度持续低分、契约履约率、改写率、字数异常
- **规划**：伏笔停滞、伏笔逾期（设置过 deadline 的）、指南针过时、大纲耗尽、摘要缺失
- **上下文**：角色消失、**死亡角色出场**、时间线缺口、关系停滞

每条发现含问题描述、数据证据、改进建议。同时写出**已脱敏**的 `meta/diag-export.md`（无小说正文，只有行为骨架）——遇到死循环/卡住类问题，把它贴到 GitHub issue 即可。

### /export — 导出成稿

格式由输出路径后缀决定（`.txt` / `.epub`）：

```text
/export                            # 默认 TXT，写到 {novelDir}/{书名}.txt
/export ~/光斑.epub                 # EPUB 3（Apple Books / 微信读书 / Kindle 转换器可读）
/export from=10 to=30 --overwrite  # 指定章节区间 + 覆盖已存在文件
```

- TXT：`《书名》` → 卷分隔 → 章节正文。premise（创作蓝图）和弧分隔不进导出；重复的章内标题会被剥掉。
- EPUB：含封面页、目录、按章拆分；重导出同一本书阅读器识别为更新版本。
- 范围内未完成的章节会跳过并显示在结果里，不算错误；目标文件已存在时需 `--overwrite`。

### /import — 导入旧书续写

```text
/import ~/我的小说.txt              # 从头导入并反推设定
/import ~/我的小说.txt from=50      # 从第 50 章接着导入（跳过反推）
/import ~/我的小说.txt regex=^第.+話$  # 自定义章节标题正则（至少含一个捕获组）
```

按章切分 → LLM 反推前提/角色/世界观/分层大纲/指南针 → 原文作为第一卷落盘 → **自动接力续写**。默认正则识别 `第一章`/`第3回`/`Chapter 1`/`序章`/`番外` 等常见格式；提示"未识别到任何章节"就用 `regex=` 传自定义正则。

> 导入是确定性回放，适合"续写同一本书"。只想借鉴设定做新书，请正常起新书并在需求里描述。

### /simulate 与 /importsim — 仿写画像

把参考文章（`.txt`/`.md`/`.markdown`）放进启动目录的 `simulate/` 文件夹，输入 `/simulate`。系统分析语料生成画像（叙述语调、句式节奏、情节模式、钩子设计、节奏密度等），写入 `output/{书名}/meta/simulation_profile.json`，并注入所有 Agent 的上下文——**只借鉴结构、节奏和手法，不复制原文表达**。

- 重跑 `/simulate` 按文件指纹增量：没变化不调 LLM；有新文章在原画像上继续合成。
- `/importsim ./profile.json` 导入之前生成的画像（仅接受本功能产出的 `simulation_profile.v1` 格式），重复来源自动跳过。只导入可信来源的文件。

---

## 4. 配置参考

完整带注释示例见仓库根的 [`config.example.jsonc`](../config.example.jsonc)。顶层字段总览：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `provider` | string | 是 | 默认 provider（`providers` 里的 key） |
| `model` | string | 是 | 默认模型名 |
| `providers` | object | 是 | Provider 凭证库，见 4.1 |
| `roles` | object | 否 | 按角色指定模型，见 4.2 |
| `style` | string | 否 | 写作风格：`default` / `suspense` / `fantasy` / `romance` |
| `context_window` | int | 否 | 覆盖上下文压缩窗口（默认按模型自动解析；大窗口模型可钉小提前触发压缩） |
| `writing_contest` | object | 否 | 多人格竞稿，见 4.3 |
| `budget` | object | 否 | 全书成本预算，见 4.4 |

### 4.1 providers

```jsonc
"providers": {
  "openrouter": {
    "api_key": "sk-or-v1-xxx",
    "base_url": "https://openrouter.ai/api/v1",
    "models": ["google/gemini-2.5-flash", "google/gemini-2.5-pro"]  // /model 面板可选列表
  },
  "my-proxy": {
    "type": "openai",                       // 自定义代理需声明 API 协议类型
    "base_url": "https://proxy.example.com/v1"
    // 自定义代理 api_key 可选
  },
  "ollama": {
    "base_url": "http://localhost:11434/v1" // 本地模型，无需 api_key
  }
}
```

- 支持：`openrouter` / `anthropic` / `gemini` / `openai` / `deepseek` / `qwen` / `glm` / `grok` / `ollama` / `bedrock` 及任意自定义代理。
- `api_key`：托管接口必填；`ollama`、`bedrock`、显式声明了 `type` 的自定义代理可省略。
- `extra_body`：透传自定义请求参数（如 `temperature` / `top_p`）。

### 4.2 roles — 按角色分配模型

```jsonc
"roles": {
  "writer":    { "provider": "anthropic",  "model": "claude-sonnet-4" },
  "architect": { "provider": "openrouter", "model": "google/gemini-2.5-pro" }
}
```

可配置角色：`coordinator` / `architect` / `writer` / `editor`，未配置的用默认模型。每个角色可加 `fallbacks` 备用模型列表（自动 failover）。

### 4.3 writing_contest — 多人格竞稿

```jsonc
"writing_contest": {
  "personas": ["乌贼", "卖报小郎君", "土豆"],  // ≥2 个作者名才启用
  "concurrency": true,                        // 候选并发生成（缺省串行）
  "mode": "synopsis"                          // 两段式（缺省 full 写全章）
}
```

- **工作流程**：每章由各 persona 写一份候选稿 → Judge 评分选优并给修改意见 → 中选者润色提交。文风由启动时 LLM 按作者名生成并缓存（`personas.json`），全书稳定。
- **`concurrency: true`**：候选并发生成（受限流 4 并发），judge/润色仍串行。某 persona 连续失败 3 次自动弃权；全部弃权降级单 Writer。
- **`mode: "synopsis"`（两段式）**：候选阶段只写「500-800 字梗概 + 300 字开头试写」，Judge 在梗概层选优，中选 persona 再写全章。全章 token 只花一份，另加梗概+评审少量开销；适合 persona 多、想控成本的场景。缺省/`full` 为候选写全章。
- **成本提示**：full 模式写作开销约为 persona 数量的倍数。
- **`judge` 字段**：预留，当前复用 editor 模型，配置不生效。
- 不配置或 personas <2 时完全退回单 Writer，零额外成本。

### 4.4 budget — 全书成本预算

```jsonc
"budget": {
  "max_cost_usd": 20.0,   // 美元上限；<=0 或不配 = 不启用
  "warn_ratio": 0.8       // 告警线比例，默认 0.8
}
```

- 成本口径：`meta/usage.json` 的累计值（按模型价格表精确计算，含缓存读写差价；自建代理不返 usage 时计不到——见 FAQ）。
- 累计成本达上限的 80%（可调）时 TUI 发一次**告警**；达到上限后**停止派发新章并暂停运行**（正在写的章节让它自然写完，不强杀）。
- **暂停语义边界**：暂停 = 停止派发新章；超限后你手动输入"继续"仍会触发一轮 coordinator 响应成本，该轮结束后门禁再次拦截。
- **恢复**：调高 `max_cost_usd` 后重启即可，进度无损续写。

---

## 5. 写作规则定制（rules.md）

三层规则就近覆盖、叠加生效：

1. `./rules.md`（启动目录，本书规则）— 最高优先级
2. `~/.ainovel/rules/*.md`（全局，任意 `.md` 按文件名字典序合并）— 跨书复用
3. 内置基线（出厂默认：去 AI 味机械黑名单 + 语义判据）— 兜底

### 大白话规则（零格式）

直接用 Markdown 写偏好，editor 按语义审阅：

```markdown
# 风格
- 严禁破折号代替逗号
- 多用身体感知，少写分析报告式表达

# 角色
- 主角"林尘"：性格冷静克制，不要写成傲娇或圣母

# 设定
- 修炼境界：练气 → 筑基 → 金丹
```

### 机械规则（可选 front matter）

想要确定性的硬检查，在文件顶部加 YAML front matter：

```yaml
---
genre: xianxia
chapter_words: 3000-6000      # 字数范围：偏差 <20% 警告，≥20% 错误
forbidden_chars: ["——"]       # 禁用字符
forbidden_phrases: ["某种程度上"]  # 禁用短语
fatigue_words:                # 疲劳词出现次数上限
  不禁: 1
  仿佛: 2
---
```

机械规则在每章提交时确定性检查，违规作为事实返回（不阻断提交），由 editor 评审时决定是否打回。完整字段说明见 [`rules.md.example`](../rules.md.example)。

---

## 6. 输出目录与文件含义

所有产物在 `output/{书名}/` 下（以下为长篇+竞稿全开时的完整形态，未启用的功能对应文件不会出现）：

```
output/{书名}/
├── chapters/                # ★ 终稿正文（01.md, 02.md, ...）
├── drafts/                  # 草稿区
│   ├── NN.plan.json         #   章构思（目标/冲突/钩子）
│   ├── NN.draft.md          #   章草稿（提交后晋升为 chapters/NN.md）
│   ├── NN.cand-<slug>.md    #   竞稿候选稿（每 persona 一份）
│   ├── NN.verdict.json      #   竞稿裁定（中选者/评分/修改意见）
│   └── NN.contest.json      #   竞稿失败计数与弃权记录
├── summaries/               # 章摘要（NN.json）、弧摘要（arc-vXXaYY.json）、卷摘要（vol-vXX.json）
├── reviews/                 # Editor 评审报告（NN.json）
├── premise.md               # 故事前提（创作蓝图，不进导出）
├── outline.json / .md       # 扁平章节大纲（已展开部分）
├── layered_outline.json / .md  # 分层大纲（长篇模式：卷→弧→章）
├── characters.json / .md    # 角色档案（含别名）
├── world_rules.json / .md   # 世界观规则
├── timeline.json / .md      # 时间线
├── foreshadow_ledger.json / .md  # 伏笔台账（埋设/推进/回收/建议回收章）
├── relationship_state.json / .md # 人物关系
├── personas.json            # 竞稿人格文风缓存
└── meta/
    ├── progress.json        # ★ 进度主状态（当前章/已完成/流程）
    ├── checkpoints.jsonl    # ★ Step 级断点（恢复依据，仅追加）
    ├── compass.json         # 终局方向指南针（长篇）
    ├── state_changes.json   # 角色状态变化流水（境界/位置/生死等）
    ├── style_rules.json     # 弧边界提炼的风格规则
    ├── snapshots/           # 弧末角色快照
    ├── cast_ledger.json     # 配角名册
    ├── usage.json           # token/成本累计（budget 门禁的依据）
    ├── run.json             # 运行元信息（模型、干预历史）
    ├── simulation_profile.json  # 仿写画像（用过 /simulate 才有）
    ├── diag-export.md       # /diag 生成的脱敏报告（提 issue 用）
    └── sessions/            # 各 agent 对话日志（jsonl，排障用）
```

带 ★ 的三个是核心：备份这本书至少要 `chapters/` + `meta/progress.json` + `meta/checkpoints.jsonl`（实际建议整目录备份）。`.md` 后缀的是同名 `.json` 的人类可读版。

---

## 7. 写作过程中的机械告警

每章提交时系统做确定性检查，结果作为**事实**返回给 Agent 并体现在日志/`/diag` 里——**不会阻断写作**，由 editor 评审和诊断规则决定后续动作。你可能看到这些字段：

| 字段 | 含义 | 该不该担心 |
|---|---|---|
| `rule_violations` | 违反了 rules.md 的机械规则（字数/禁词/疲劳词） | editor 会按严重度处理；频繁出现可在干预里强调 |
| `foreshadow_overdue` | 伏笔超过了"建议回收章"（deadline）仍未回收 | Writer/Architect 会被引导回收或顺延；`/diag` 有对应规则 |
| `foreshadow_unknown_ids` | Agent 推进/回收了台账中不存在的伏笔 ID | 通常是拼写漂移，系统会引导下章修正 |
| `character_violations` | 已记录死亡的角色在后续章节出场 | 闪回/回忆属误报（editor 会豁免）；真错会进重写流程 |

这些机械层故意做得**保守**（宁可漏报不误报），语义层的最终判断在 editor 七维评审。

---

## 8. FAQ 与排障

**Q: reasoning / thinking 类模型（DeepSeek-R1 等）能用吗？**
不能用于 coordinator/architect/writer/editor。多轮工具循环要求回传 `reasoning_content`，当前框架层（litellm/agentcore）未携带，第二轮起会报 HTTP 400。规避：在 provider/网关侧把模型路由到非 thinking 后端。详见 [known-issues.md](known-issues.md)。

**Q: 怎么暂停？怎么继续？**
运行中按 `Esc` 暂停（正在执行的任务自然结束）；`Ctrl+C` 连按两次退出程序。暂停/停机后在输入框输入任意指令即注入并恢复，或直接重启程序自动续写。

**Q: 上下文条变红了要紧吗？**
不要紧，>85% 只是提示即将触发压缩。压缩是自动分级的（清理旧工具结果 → 截断 → 用库存摘要替换 → LLM 摘要兜底），压缩后会自动注入恢复包防"失忆"。若日志反复出现压缩失败告警，换个上下文更大的模型或在配置里调小 `context_window` 让压缩更早触发。

**Q: 成本面板一直是 0？**
上游（常见于自建 OpenAI 兼容代理）没按协议返回 usage 数据。TUI 会提示 `missing usage`。这不影响写作，但 **budget 门禁会因此失效**（计不到成本），用自建代理时请勿依赖预算功能。

**Q: 预算超限暂停了，怎么恢复？**
编辑配置调高 `budget.max_cost_usd`，重启程序即可断点续写。注意超限后手动"继续"每次仍会消耗一轮响应成本。

**Q: 竞稿模式某个 persona 总失败怎么办？**
连续失败 3 次自动弃权、候选数减一继续，全弃权降级单 Writer，无需干预。想省成本可改 `mode: "synopsis"` 或减少 personas。

**Q: /import 提示"未识别到任何章节"？**
你的文件标题格式不在默认正则内（如 `001`、`（一）`）。用 `regex=` 传自定义正则，至少含一个捕获组提取标题：`/import ~/book.txt regex=^（(.+)）$`。

**Q: 写到一半感觉跑偏了/卡住了怎么办？**
先 `/diag` 看体检报告（卡循环、伏笔停滞、评审低分都会被点名）。跑偏用干预指令纠正（"后续章节回到主线 X"）；疑似死循环把 `meta/diag-export.md` 贴到 GitHub issue。

**Q: 怎么重写某一章？**
在干预框里直接说（"重写第 12 章，把打斗场面写得更克制"），Coordinator 会把该章入重写队列处理。

**Q: 换模型会影响已写的内容吗？**
不会。`/model` 切换只影响后续调用；上下文窗口、计费自动按新模型调整。

**Q: 想从零重来？**
删除 `output/{书名}/` 整个目录（先 `/export` 备份成稿）。配置文件不用动。

**Q: Windows 下有什么注意的？**
正常支持。路径用反斜杠或正斜杠都可以；中文内容的字数统计按 rune 计，与平台无关。
