# 已知问题（Known Issues）

记录已知的兼容性限制与待办，供后续维护参考。

---

## 1. reasoning（thinking）模型在多轮 tool-calling 下不可用

**状态**：✅ 已修复（2026-06-10 确认，随依赖升级 agentcore v1.6.12 + litellm v1.6.16 修复）
**发现于**：2026-06-03，多人格竞稿功能（PR #1）真实 LLM 端到端测试

### 修复说明（2026-06-10）

- `agentcore@v1.6.12 llm/litellm.go:489-499`：多轮回放时把上一轮 assistant 的 thinking 内容
  显式转发为 `reasoning_content`（注释点名 DeepSeek/GLM/Qwen/Mimo 等 reasoning-aware provider）。
- `litellm@v1.6.16 providers/deepseek.go`：`ReasoningField: "reasoning_content"` +
  `ThinkingMapper`（thinking enabled/disabled 请求映射）。
- thinking 模型现可用作 coordinator/architect/writer/editor。注意取舍：thinking token 按输出
  计费且多轮长循环时延明显增加，写作场景默认关闭仍是合理选择（DeepSeek V4 在 provider 的
  `extra_body` 配 `{"thinking":{"type":"disabled"}}`；V4 默认 enabled）。

以下为历史记录，保留备查。

### 现象

当所配模型路由到 **reasoning（thinking）类模型**（如 DeepSeek-R1 / V3-thinking）时，多轮工具循环报错：

```
HTTP 400 [litellm:openai:validation] bad request:
Error from provider (DeepSeek): The `reasoning_content` in the thinking mode must be passed back to the API.
```

### 根因

DeepSeek 等 reasoning 模型要求：多轮对话中，上一轮 assistant 消息的 `reasoning_content` 字段必须在下一轮请求里**回传**。当前 litellm / agentcore 的多轮工具循环在组装后续请求时**未携带** `reasoning_content`，被 provider 拒绝。

关键区分：

- **单轮调用成功**（如 persona 文风生成）——无需回传历史 reasoning
- **多轮工具循环失败**（architect / coordinator / writer / editor 等长循环）——第二轮起就要回传上一轮 `reasoning_content`

这是**框架层**（litellm/agentcore 的消息组装）的限制，与具体业务功能无关。任何能正常多轮 tool-calling 的非 reasoning 模型都能完整跑通（实测 `gpt-5.4-mini`、以及路由到非 thinking 后端的 `kimi-k2.6` 均正常）。

### 影响

无法使用以 thinking 模式运行的 reasoning 模型作为 coordinator / architect / writer / editor。

### 修复方向

在 agentcore/litellm 组装多轮请求时，把上一轮 assistant 消息的 `reasoning_content` 一并带入下一轮（针对支持该字段的 provider）。需先确认 litellm 的 message 结构是否保留了该字段。

### 临时规避

在 provider / 网关侧把模型路由到**非 thinking 后端**即可正常使用。
