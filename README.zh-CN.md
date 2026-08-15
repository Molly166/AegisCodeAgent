# AegisCodeAgent

[![CI](https://github.com/Molly166/AegisCodeAgent/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/Molly166/AegisCodeAgent/actions/workflows/ci.yml)
[![Aegis Code Review](https://github.com/Molly166/AegisCodeAgent/actions/workflows/aegis-review.yml/badge.svg?branch=master)](https://github.com/Molly166/AegisCodeAgent/actions/workflows/aegis-review.yml)
![Go 1.23+](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

[English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md)

AegisCodeAgent 是一个使用 Go 开发、自动运行在 GitHub Pull Request 上的代码审核 Agent。它将确定性分析、仓库级上下文、大模型推理和独立验证组合起来，只把具有可靠证据的问题发布到最终审核结果中。

> **当前里程碑：v0.7。** Aegis 已具备 Replay Eval Harness、语义型验证、显式 Needs Review 门禁、PR 变更意图上下文，以及启用四类分析器的 GitHub 流水线。目前主要面向 Go 项目，并首先接入 DeepSeek 作为推理模型。

## 创建 Pull Request 后会发生什么？

使用者不需要启动服务器，也不需要在本地保持进程运行。GitHub Actions 会自动启动 Aegis，使用 PR Base 审核准确的 Head Commit，并将结果发布回 Pull Request。

```text
Pull Request 创建或更新
          │
          ▼
    可信 Workflow 编排
          │
          ▼
 Git Diff 与变更行范围
          │
          ├──────────────► 确定性 Go 分析器
          │                 go test · go vet · staticcheck · gosec
          │
          └──────────────► 仓库上下文引擎
                            PR 意图 · 仓库规则 · AST · 类型 · 调用者 · 测试
                                      │
                                      ▼
                           DeepSeek Reasoning Agent
                                      │
                                      ▼
                                证据 Verifier
                                      │
                                      ▼
                 P0-P3 Annotation · Job Summary · HTML 报告
```

模型不能直接决定代码是否可以合并。静态分析问题本身需要包含可复现证据；模型候选必须通过位置、Diff、源码快照、聚焦诊断或语义证据检查才能晋升。无效假设会被 Rejected；暂时无法证实或证伪的假设会明确进入 `Needs Review`，其中 P0 默认阻断，绝不会再呈现成“没有问题”。

## 项目架构

系统按照输入与输出明确的阶段进行拆分：

| 阶段 | 职责 | 输出 | 实现位置 |
| --- | --- | --- | --- |
| GitHub 编排 | 响应 PR 事件，检出可信 Base 与准确 Head，取消过期任务 | 可复现的审核工作区 | `.github/workflows/aegis-review.yml` |
| Diff 引擎 | 安全解析 Revision，处理三点 Diff、重命名、二进制文件、Hunk 与变更行 | 标准化 Change Set | `internal/gitdiff/` |
| 静态分析 | 并发运行分析器，实施超时，统一诊断格式并去重 | 具有证据的 Findings | `internal/analyzer/` |
| 上下文引擎 | 将 PR 标题、正文、标签、Issue 和仓库规范与 Go 声明、类型关系共同排序 | Repository Context Bundle | `internal/context/` |
| Reasoning Agent | 让 DeepSeek 通过只读工具检查受限证据并生成结构化候选 | 未验证 Candidates | `internal/agent/` |
| Verifier V2 | 校验身份与位置，重跑聚焦检查，执行源码感知的语义规则并关联独立证据 | Verified/Needs Review/Rejected 结论 | `internal/verifier/` |
| 发布层 | 映射 P0-P3、执行合并阈值、渲染 GitHub 输出和完整报告 | Summary、Annotation、HTML/JSON | `internal/githubreport/`、`internal/report/` |
| Eval Harness | Replay 版本化 Bug/Clean 报告，匹配期望并评估 Finding 与门禁行为 | Precision、Recall、F1、P0/P1 Recall、Gate Accuracy、False Block Rate | `internal/eval/`、`eval/cases/` |
| 凭证边界 | 从仓库代码控制的子进程中移除凭证类环境变量 | 安全的子进程环境 | `internal/secureenv/` |

### 一个 Finding 如何进入最终报告

```text
确定性诊断 ───────────────────────────────────────────► 最终 Finding

模型候选
   │
   ▼
Schema + 仓库边界 + 变更行校验
   │
   ▼
聚焦分析器 + 语义证据关联
   │
   ├── Verified ──────────────────────────────────────► 最终 Finding
   ├── Rejected ──────────────────────────────────────► 仅保留审计记录
   └── Needs Review ──────────────────────────────────► 明确展示并交给人工复核；P0 默认阻断
```

最终报告使用稳定的 `ReviewReport` 领域模型。CLI、GitHub Publisher、HTML Renderer 和 JSON 自动化接口共享这一模型，使审核逻辑不会与展示方式耦合。

## 开箱即用：GitHub 自动审核

这是在当前仓库或个人 Fork 中使用 Aegis 的推荐方式。

### 第一步：准备仓库

如果需要，先 Fork 仓库，然后 Clone：

```bash
git clone https://github.com/<你的 GitHub 用户名>/AegisCodeAgent.git
cd AegisCodeAgent
go test ./...
```

确认仓库的 **Settings → Actions → General** 已经允许运行 GitHub Actions。

### 第二步：选择审核模式

确定性审核模式不需要任何 Secret：

```text
go test + go vet + staticcheck + gosec + 语义 Verifier + GitHub 报告
```

如需启用完整的 Reasoning Agent 与 Verifier，在 **Settings → Secrets and variables → Actions** 中创建 Repository Secret：

```text
Name:  DEEPSEEK_API_KEY
Value: <你的 DeepSeek API Key>
```

Fork 和 Dependabot Pull Request 永远不会获得该 Secret，并会自动使用确定性审核模式。

### 第三步：正常提交功能分支

```bash
git switch master
git pull --ff-only origin master
git switch -c feature/my-change

# 修改代码或文档。
git add .
git commit -m "feat: describe the change"
git push -u origin feature/my-change
```

创建从 `feature/my-change` 到 `master` 的 Pull Request。`opened` 事件会启动 Aegis；此后每次 Push 都会产生 `synchronize` 事件、触发新审核并取消已经过期的运行。

纯文档 Pull Request 仍然会触发 Workflow，并生成 Summary 和报告 Artifact。当不存在受支持的源码证据时，Aegis 可以跳过不必要的模型推理，但确定性分析、报告发布和门禁语义仍然可审计。

### 第四步：查看审核结果

进入 Pull Request 后依次查看：

1. **Conversation**：Aegis 持续更新的审核评论，以及完整 HTML 报告入口。
2. **Checks → Aegis Code Review**：执行状态和 Job Summary。
3. **Annotations**：绑定到变更文件及代码行的问题。
4. **Artifacts → aegis-review-report**：完整的 `review.html` 和 `review.json`。
5. 最终 Check 状态：是否通过合并门禁。

| 优先级 | 含义 | GitHub Annotation | 默认阻断 |
| --- | --- | --- | :---: |
| P0 | Critical | Error | 是 |
| P1 | High | Error | 是 |
| P2 | Medium | Warning | 否 |
| P3 | Low / Info | Notice | 否 |

Reasoning Agent 或仓库上下文降级只表示审核覆盖率下降，不会自行升级为 P0/P1。确定性静态分析或 Verifier 未完整执行仍采用 fail-closed；未解决的 P0 假设则由独立的 Needs Review 阈值控制。

如需强制执行审核结果，在 `master` Branch Ruleset 中将 `Aegis Code Review` 设置为 Required Status Check。GitHub 自身的通知设置负责站内和邮件通知，Aegis 不额外运行邮件服务。

> Aegis 当前是 Repository-native Workflow，还不是 GitHub Marketplace Action。它可以在本仓库及其 Fork 中开箱即用；集成到完全无关的仓库目前需要同时引入 Aegis 源码和 Workflow，封装为可复用 Action 属于后续工作。

## 开箱即用：本地 CLI

环境要求：Go 1.23+ 和 Git。

### 不使用 API Key 的确定性审核

```bash
go build -o aegis ./cmd/aegis

./aegis review \
  --repo . \
  --base master \
  --head HEAD \
  --output review.html
```

使用浏览器打开 `review.html`。由于分析器需要读取真实文件系统，当前 Worktree 必须保持干净并与 `HEAD` 一致。

### 完整 DeepSeek + Verifier 审核

```bash
cp .aegis.example.json .aegis.json
cp .env.example .env

# 只把真实 Key 写入已经被 Git 忽略的 .env 文件。
./aegis review \
  --config .aegis.json \
  --repo . \
  --base master \
  --head HEAD \
  --output review.html
```

`.aegis.json` 保存非敏感的 Provider 设置、预算和超时。API Key 只从 `DEEPSEEK_API_KEY` 或被忽略的 `.env` 中读取。除非显式允许自定义 Endpoint，否则只允许连接 DeepSeek 官方地址。

常用命令：

```bash
# 生成机器可读报告
./aegis review --repo . --base master --head HEAD --format json --output review.json

# 安装了 staticcheck 和 gosec 后运行所有适配器
./aegis review --repo . --base master --head HEAD --analyzers all --output review.html

# Replay 仓库内置回归语料并生成自包含质量看板
./aegis eval --corpus ./eval/cases --format html --output eval-report.html

# 查看完整参数
./aegis review --help
./aegis github --help
./aegis eval --help
```

## 安全模型

- Review Binary 从可信的 PR Base Commit 构建，准确的 Head Commit 则在独立目录中作为审核目标。
- Workflow 只申请只读仓库权限，并关闭 Checkout Credential 持久化。
- Fork 与 Dependabot PR 不会获得 `DEEPSEEK_API_KEY` 或可写 Token。
- Git、测试、Vet、Staticcheck 和 Gosec 子进程不会继承凭证类环境变量。
- Agent 工具只读、限制仓库路径、限制行范围并限制输出大小。
- GitHub 会把同仓库分支视为 Secret 的可信来源，因此应限制写权限，并强制审核 `.github/workflows/` 下的改动。

## 开发指南

Package 边界：

```text
cmd/aegis/             CLI 编排和用户可见错误
internal/gitdiff/      Revision 解析与 Unified Diff 处理
internal/analyzer/     分析器适配、调度与诊断标准化
internal/context/      AST/类型索引、关系图、排序与预算
internal/config/       严格 JSON 配置和最小范围 Dotenv 加载
internal/agent/        Provider 协议、Prompt、工具与推理循环
internal/verifier/     候选校验与证据裁决
internal/eval/         Replay 语料、期望匹配、质量指标与 HTML 看板
internal/githubreport/ P0-P3 映射、Summary、Annotation 与合并门禁
internal/secureenv/    子进程凭证隔离
internal/review/       共享领域模型
internal/report/       自包含 HTML、JSON 和 Markdown Renderer
```

创建 Pull Request 前运行质量门禁：

```bash
go fmt ./...
go vet ./...
go test -race ./...
go build ./cmd/aegis
```

任何新的 Finding 来源都必须提供真实位置、严重程度、类别、来源、置信度和可复现证据。新的 Agent Candidate 在验证完成之前必须与最终 Findings 保持分离。

## 当前范围与路线图

已经完成：

- 确定性 Go 分析流水线；
- 仓库上下文引擎；
- 有边界的 DeepSeek Reasoning Loop；
- 支持语义证据和显式 Needs Review 的 Verifier V2；
- Replay Eval Harness 与首批 Bug/Clean 回归语料；
- PR 变更意图和仓库规范上下文；
- GitHub Workflow 全量运行 go test、go vet、staticcheck 和 gosec；
- 自包含 HTML 证据报告；
- GitHub Actions 触发、Annotation、Artifact 和合并门禁。

下一阶段：

- 将种子语料扩充为具有独立标注和统计意义的基准集；
- 增加实时模型对比、重复试验和置信区间；
- 封装可复用 GitHub Action 并提供 Release 分发；
- 接入更多模型 Provider 和生产可观测性。

## License

[MIT](LICENSE)
