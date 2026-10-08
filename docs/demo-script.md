# GoPilot 竞赛演示讲解词（约 2–3 分钟）

## 演示前准备

```bash
docker compose up -d --build
docker compose ps
```

确认三个容器均为 `healthy`，然后打开 <http://localhost:3000>。如使用真实 AI 诊断，请提前在本地 `.env` 中配置有效的 OpenAI-compatible 提供商凭据。

## 现场讲解

### 0:00–0:25｜问题与产品

“Go 服务发生运行时问题时，开发者常常需要在指标、pprof 和大量堆栈之间来回排查。GoPilot 的目标不是让模型猜答案，而是让 Agent 像工程师一样调用真实诊断工具，并且让最终结论可以回溯到具体证据。”

指出页面的三个区域：运行时监控、AI 智能诊断、证据驱动诊断。

### 0:25–0:55｜建立健康基线并注入故障

“现在服务在线，活动泄漏为 0。页面会在无故障时建立可信的运行时基线。”

点击“注入泄漏”。

“我们一次注入 25 个受控 Goroutine。它们会阻塞在命名明确的 `LeakController.waitForever` 中，数量有上限，并且可以安全重置。”

等待趋势图明显上升，指出当前 Goroutine、活动泄漏、相对基线变化。强调“服务在线”只代表健康检查成功，并不等于运行时没有异常。

### 0:55–1:50｜让 Agent 采集证据

点击“开始诊断”。

“GoPilot 使用非流式、受限步数的 Agent 循环。等待期间页面不会伪造工具进度。模型必须调用工具，而且不能绕过 `submit_diagnosis` 直接输出结论。”

结果出现后，逐项查看诊断过程：

1. `get_service_status`：确认服务在线、当前 Goroutine 和活动泄漏数量。
2. `get_runtime_metrics`：验证近期数量增长及时间窗口。
3. `get_goroutine_profile`：解析真实 pprof，并将相同堆栈聚合。
4. `submit_diagnosis`：提交引用上述 observation_id 的结构化诊断。

“这条时间线由服务端调查账本生成，工具调用 ID、参数、状态和 observation_id 都不是前端或模型自行编造的。”

### 1:50–2:25｜查看根因与证据

展开根因和证据卡片。

“最终根因定位到 `LeakController.waitForever`。这里的模型置信度只是模型估计，不是统计概率；真正可验证的是下方的运行时样本、增长幅度、聚合堆栈数量和函数帧。每条 evidence_ref 都必须属于本次调查，否则后端会拒绝诊断。”

### 2:25–2:50｜恢复与总结

点击“重置演示”。

“重置会终止所有故意泄漏的 Goroutine。活动泄漏回到 0，运行时指标逐步恢复，系统在短暂波动后重新建立健康基线。”

“这就是 GoPilot 的完整闭环：故障注入、真实观测、Agent 工具调查、证据约束的根因分析，以及可验证恢复。”

## 现场故障备用方案

- 如果 LLM 提供商暂时不可用：继续展示实时指标、泄漏注入、GoPilot 证据接口和可重置能力，并明确说明这是外部提供商故障，不要把历史结果描述为本次实时调用。
- 可以展示 `docs/screenshots/dashboard-metrics-baseline-fixed-1440x900.png`。必须说明：“这是此前真实验证时记录的截图，用于备用演示，不是当前实时结果。”
- 可以通过 `docker compose logs gopilot` 展示明确的提供商错误；不要暴露 `.env` 或 Authorization 信息。
- 演示结束后始终点击“重置演示”，必要时运行 `docker compose down`。
