# 历史诊断回放数据来源

## 案例身份

- 案例文件：`web/public/replays/goroutine-leak.json`
- Case ID：`goroutine-leak-deepseek-2026-10-08`
- Diagnosis ID：`diag_1791447770985_000002`
- Investigation ID：`inv_1791447764276_000001`
- 真实记录时间：2026-10-08 16:22:50（Asia/Shanghai）
- Provider：DeepSeek
- 模型：`deepseek-flash`
- 协议：OpenAI-compatible Chat Completions
- 文件 SHA-256：`1CEF4378E65C8CF76C33FE15BEEEBDDCA460ACCDF0FFDF06B4FA9E1E802CE7F8`

该文件来自一次真实 Provider 调用，不是 Mock、手写样例或根据预期结果补造的数据。

## 录制方法

录制使用 Docker Compose 中的真实 `demo-service`、GoPilot Evidence Collector 和 Agent Runtime：

1. Reset 演示服务，确认活动泄漏为 0。
2. 等待无故障样本建立可信基线；直接运行时观测为 6 个 Goroutine。
3. 通过真实 `POST /api/leak` 注入 25 个受控 Goroutine。
4. 注入后直接运行时观测为 31 个 Goroutine。
5. 通过 `POST /api/diagnoses` 请求真实 DeepSeek Agent 使用运行时证据诊断。
6. 保存 GoPilot 返回的完整 Diagnosis 对象，不改写 Metrics、pprof、Tool 顺序、时间戳或 observation_id。
7. 校验时间线、Evidence 和 `submit_diagnosis.evidence_refs` 后写入静态回放封套。
8. Reset 当前演示服务，确认活动泄漏归零。

API Key、Authorization Header 和其他凭据均未写入案例文件。

## 真实观测摘要

本次调查包含 6 个服务端时间线步骤和 5 条物化 Evidence：

| 顺序 | Tool | Observation ID | 真实结果摘要 |
| --- | --- | --- | --- |
| 1 | `get_service_status` | `obs_1791447765130_000001` | 当前 30 个 Goroutine，25 个活动受控泄漏 |
| 2 | `get_runtime_metrics` | `obs_1791447765130_000002` | 46 个样本，Goroutine 从 5 增至 30，增量 +25 |
| 3 | `get_goroutine_profile` | `obs_1791447765131_000003` | 25 个堆栈聚合到 `LeakController.waitForever` |
| 4 | `get_goroutine_profile` | `obs_1791447766250_000004` | Agent 再次核验，仍有 25 个相同泄漏堆栈 |
| 5 | `get_runtime_metrics` | `obs_1791447766250_000005` | 30 个样本，Goroutine 从 5 增至 30，增量 +25 |
| 6 | `submit_diagnosis` | — | 引用全部 5 个 observation，提交结构化诊断 |

pprof 中的泄漏状态为 `chan receive`，原始函数帧为：

```text
gopilot/internal/demo.(*LeakController).waitForever
created by gopilot/internal/demo.(*LeakController).Inject
```

最终模型估计置信度为 0.95。根因、受影响组件和建议均保留真实 Provider 返回内容。

## 保存前校验

录制流程在写入文件前完成以下检查：

- 时间线 `sequence` 从 1 开始连续递增。
- 最后一步是成功的 `submit_diagnosis`。
- 每条 Evidence 的 observation_id 都来自一个成功的 Evidence Tool 步骤。
- 时间线 Tool 名称与对应 Evidence 的 source_tool 一致。
- `submit_diagnosis.arguments.evidence_refs` 与物化 Evidence 的 observation_id 集合完全一致。
- pprof Evidence 包含 25 个 `LeakController.waitForever` 堆栈。
- 当前演示服务在录制完成后已 Reset。

前端每次读取案例时会再次执行同样的结构、引用和时间线一致性校验。损坏 JSON、缺失字段、未知 observation_id 或 Tool/Evidence 不一致都会拒绝展示。

## 回放边界

- 回放不会调用 DeepSeek，也不会调用 `POST /api/diagnoses`。
- 回放 Evidence 属于历史 Investigation，不会注入当前 Agent Ledger。
- 历史 Metrics 与 pprof 只显示在历史诊断区域，不参与当前基线或趋势计算。
- 当前 Inject、Reset 或服务重启不会修改静态案例。
- 该案例用于比赛演示和工程审查，不代表实时服务状态，也不是通用调查持久化系统。
