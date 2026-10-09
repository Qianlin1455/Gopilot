# GoPilot v1.1 评委使用指南

> **推荐路线：不配置 API Key，3 分钟内完成真实故障注入与历史诊断回放。**

## 项目介绍

GoPilot 是一个面向 Go Runtime 的证据驱动诊断演示。它让 Agent 通过真实运行时指标和 pprof 堆栈调查故障，并由服务端校验证据引用，降低模型凭空推断的风险。当前 MVP 聚焦一个可重复、可重置的 Goroutine 泄漏场景，不定位为通用生产级诊断平台。

## 环境要求

- Docker Desktop，或 Docker Engine 与 Docker Compose v2
- Git（使用 `git clone` 时需要）
- 现代浏览器：Chrome、Edge 或 Firefox
- 首次构建时可访问 Docker Hub，以下载 Go、Node、Alpine 和 Nginx 基础镜像
- DeepSeek API Key：**仅实时 AI 诊断需要；首选回放路线不需要**

## 快速启动

仓库实际 GitHub 项目为 `Qianlin1455/Gopilot`。评委首选使用 HTTPS 克隆：

```powershell
git clone https://github.com/Qianlin1455/Gopilot.git
cd Gopilot
docker compose up -d --build
docker compose ps
```

也可以从仓库页面 <https://github.com/Qianlin1455/Gopilot> 点击 **Code → Download ZIP**，解压后在项目根目录打开终端，执行相同的 Docker Compose 命令。

已经配置 GitHub SSH Key 的开发者也可以使用：

```powershell
git clone git@github.com:Qianlin1455/Gopilot.git
```

三个服务显示为 `healthy` 后，打开：<http://localhost:3000>。

## 无 API Key 体验——首选路径

无需注册 DeepSeek，也无需创建 `.env`。历史回放不会访问模型服务。

1. 执行 `docker compose up -d --build`，打开中文 Dashboard。
2. 在“运行时监控”中等待服务在线，并观察当前 Goroutine、可信基线和趋势。
3. 点击“注入 25 个泄漏”。demo-service 会创建 25 个受控 Goroutine，它们阻塞在 `LeakController.waitForever`。
4. 确认当前 Goroutine 与活动泄漏数量上升，趋势图出现对应变化。
5. 在“AI 智能诊断”中选择“查看历史诊断”。
6. 查看此前真实 Agent 调查的 Tool 顺序、参数、执行状态和 `observation_id`。
7. 展开 Evidence 卡片，查看真实 Metrics 样本与 pprof 聚合堆栈。
8. 查看历史根因、受影响组件、模型置信度估计和修复建议。
9. 点击“重置演示”，确认当前活动泄漏回到 0，并等待运行时恢复。

历史回放来自 2026-10-08 的一次真实 DeepSeek 调用，并非当前环境正在进行的 AI 推理。页面上方的 Runtime 是当前 demo-service 的实时数据；回放区域的数据属于过去的独立 Investigation。两者不会混合计算，历史 Observation 也不会写入当前 Agent Ledger。

## 有 API Key 体验——可选路径

1. 从示例配置创建本地 `.env`：

   ```powershell
   Copy-Item .env.example .env
   ```

2. 填写自己的 DeepSeek 配置：

   ```dotenv
   LLM_BASE_URL=https://api.deepseek.com
   LLM_API_KEY=YOUR_OWN_KEY
   LLM_MODEL=deepseek-flash
   LLM_THINKING=disabled
   ```

3. 重新构建并创建容器：

   ```powershell
   docker compose up -d --build --force-recreate
   docker compose ps
   ```

4. 注入泄漏、等待数个运行时样本，然后选择“实时 AI 诊断”并点击“开始诊断”。

Docker Compose 会读取仓库根目录 `.env` 做变量替换，但只把 `LLM_BASE_URL`、`LLM_API_KEY`、`LLM_MODEL`、`LLM_THINKING` 显式传入 GoPilot 容器。`DEMO_SERVICE_URL` 在 Compose 中固定为 `http://demo-service:8081`。API Key 不会进入 Web 前端或 demo-service。

GoPilot 始终发送 `tool_choice: "required"`。DeepSeek thinking 模式不支持这一设置，因此 DeepSeek 配置需要 `LLM_THINKING=disabled`。不要把真实 Key 写入 `.env.example` 或提交到 Git。

## 操作指南

| 界面区域/操作 | 实际作用 |
| --- | --- |
| 运行时监控 | 每秒读取 demo-service 的真实 Runtime 数据，显示服务在线状态、Goroutine、活动泄漏、可信基线与趋势。 |
| 注入 25 个泄漏 | 调用 `POST /demo/api/leak`，创建数量有界、可由 Reset 终止的演示 Goroutine。 |
| 重置演示 | 调用 `POST /demo/api/leak/reset`，关闭当前泄漏 generation 并等待受控 Goroutine 退出。 |
| 实时 AI 诊断 / 开始诊断 | 调用 `POST /gopilot/api/diagnoses`，由 Agent 使用真实工具调查当前状态；需要模型配置。 |
| 查看历史诊断 | 从静态 `/replays/goroutine-leak.json` 加载已校验的真实历史案例；不调用 DeepSeek。 |
| 诊断过程 | 展示服务端 Investigation Ledger 中的 Tool、参数、状态、Tool Call ID 和 Observation ID。 |
| 诊断证据 | 展示服务端物化的 Metrics、服务状态和 pprof 分组证据；可展开查看结构化数据。 |

`/healthz` 成功只表示服务在线，不表示没有 Goroutine 泄漏。运行时异常需要结合活动泄漏、指标趋势和 pprof 证据判断。

## 如何理解诊断结果

- **根因分析（Root Cause）**：模型依据已返回证据形成的推断，不是原始观测本身。
- **诊断过程**：GoPilot 服务端按执行顺序记录的调查时间线，不由前端伪造。
- **诊断证据（Evidence）**：由成功 Tool Observation 物化出的服务状态、Metrics 或 pprof 数据。
- **`observation_id`**：GoPilot 为每次工具执行生成的标识。模型提交的 `evidence_refs` 必须引用本次 Investigation 中已存在且成功的 Observation。
- **模型置信度**：模型对当前结论的主观估计，范围为 0–1；它不是统计置信区间，也不构成正确性保证。

## 常见问题

### Docker 无法启动

确认 Docker Desktop 或 Docker Engine 正在运行，然后执行：

```powershell
docker compose ps
docker compose logs --no-color
```

### 3000 端口被占用

停止占用该端口的本地进程，或把 `compose.yaml` 中 `127.0.0.1:3000:8080` 临时改为例如 `127.0.0.1:3001:8080`，随后访问 `http://localhost:3001`。

### Docker Hub 镜像拉取失败

恢复 Docker 的网络访问或配置可信的本机镜像加速，然后重新执行 `docker compose up -d --build`。无需修改项目镜像名称。

### 页面显示“Agent 未配置”

这在无 Key 模式下是预期行为。使用“查看历史诊断”即可完整查看真实调查记录。如需实时诊断，请填写 `.env` 中的三个必需 LLM 变量并重新创建 `gopilot` 容器。

### DeepSeek 请求失败

执行 `docker compose logs gopilot` 查看提供商状态码或协议错误。确认模型支持 Tool Calling，并为 DeepSeek 设置 `LLM_THINKING=disabled`。GoPilot 不会在正常日志中输出 API Key。

### 没有 API Key 如何体验

直接使用本指南的“无 API Key 体验”路线。Runtime、故障注入、Reset 和历史回放均不需要模型凭据。

### 页面显示“基线尚未建立”

可信基线只从没有活动泄漏的稳定样本建立。首次启动或 Reset 后请等待数秒；如果服务启动时已经有泄漏，应先 Reset，再等待健康样本。

### 历史诊断和实时诊断有什么区别

实时诊断调查当前 demo-service，会访问配置的模型；历史回放读取随 Dashboard 发布的静态真实记录，不访问模型，也不影响当前 Runtime。回放不等于当前服务已经被诊断。

## 停止与清理

仅停止本项目创建的 Compose 服务：

```powershell
docker compose down
```

该命令不会执行全局容器、镜像或卷清理。

进一步了解实现，请参阅[技术架构文档](architecture.md)和[历史回放来源说明](replay-provenance.md)。
