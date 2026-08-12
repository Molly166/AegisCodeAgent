# AegisCodeAgent

[![CI](https://github.com/Molly166/AegisCodeAgent/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/Molly166/AegisCodeAgent/actions/workflows/ci.yml)
[![Aegis Code Review](https://github.com/Molly166/AegisCodeAgent/actions/workflows/aegis-review.yml/badge.svg?branch=master)](https://github.com/Molly166/AegisCodeAgent/actions/workflows/aegis-review.yml)
![Go 1.23+](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

[English](README.md) | [简体中文](README.zh-CN.md)

AegisCodeAgent 是一个使用 Go 开发、以证据为基础的 Pull Request 代码审核 Agent。它将确定性分析、仓库级推理和独立验证组合起来，只把具有证据的问题直接发布到 GitHub。

**当前里程碑：v0.6 · GitHub 原生审核流水线。** 创建或更新 Pull Request 就能自动运行 Aegis，不需要部署独立服务器、Webhook 服务或本地常驻进程。

## 为什么选择 Aegis

- **证据优先：**先使用 `go test`、`go vet`、可选的 `staticcheck`/`gosec`、准确的 Diff 位置和可复现诊断建立事实，再进入模型推理。
- **理解仓库：**通过 Go AST 和 `go/types`，在明确的上下文预算内关联变更符号、调用者、被调用者、接口、实现和测试。
- **验证而非猜测：**每个模型候选都必须通过真实 Diff 和本地证据检查，缺少支持的假设不会进入最终 Findings。
- **GitHub 原生体验：**在 Pull Request 中提供 P0-P3 Annotation、Job Summary、HTML/JSON Artifact、过期任务取消和可配置的合并门禁。
- **默认安全：**从可信 Base Commit 构建 Reviewer；Fork 审核不获取 Secret，仓库代码控制的子进程也不会继承凭证类环境变量。

## 审核流水线

```text
Pull Request
    │
    ▼
Git Diff ──► 确定性分析器 ──► 仓库上下文
                                  │
                                  ▼
                          DeepSeek Reasoning Agent
                                  │
                                  ▼
                              证据 Verifier
                                  │
                                  ▼
                 GitHub Summary + P0-P3 Annotation + HTML Artifact
```

## GitHub Pull Request 自动审核

使用者不需要在本地启动 Aegis。[`.github/workflows/aegis-review.yml`](.github/workflows/aegis-review.yml) 会在 Pull Request 创建、重新打开、转为 Ready 或收到新 Commit 时启动 `Aegis Code Review` Check。

### 触发一次真实审核

```bash
git switch master
git pull --ff-only origin master
git switch -c docs/verify-aegis-trigger

# 修改 README.md 或任意源文件。
git add README.md README.zh-CN.md
git commit -m "docs: clarify automatic review workflow"
git push -u origin docs/verify-aegis-trigger
```

创建从 `docs/verify-aegis-trigger` 到 `master` 的 Pull Request。PR 中应该同时出现 `CI` 与 `Aegis Code Review`。打开 Aegis Check 可以查看 Job Summary 和 Annotation；下载 `aegis-review-report` 可以查看完整的自包含 HTML 报告。此后每次向该分支 Push 都会触发新审核，并取消已经过期的运行。

如需启用完整的 DeepSeek + Verifier 链路，在 **Settings → Secrets and variables → Actions → New repository secret** 中添加：

```text
DEEPSEEK_API_KEY=<你的密钥>
```

没有配置 Secret 时，Aegis 仍然会运行确定性的 `go test` 和 `go vet` 审核。Fork 和 Dependabot Pull Request 不会获得 Secret，并会自动降级为纯静态审核模式。

每次审核会发布：

- 包含 P0/P1/P2/P3 数量和各阶段状态的 GitHub Job Summary；
- 展示在 Pull Request Checks 中的行级 `error`、`warning` 和 `notice` Annotation；
- 名为 `aegis-review-report` 的 Artifact，其中包含 `review.html` 和 `review.json`；
- 当存在 P0/P1，或者必要审核阶段未完整执行时，将 `Aegis Code Review` Check 标记为失败。

| Aegis 优先级 | 内部严重程度 | GitHub Annotation | 默认阻断 |
| --- | --- | --- | :---: |
| P0 | Critical | Error | 是 |
| P1 | High | Error | 是 |
| P2 | Medium | Warning | 否 |
| P3 | Low / Info | Notice | 否 |

如果需要让 P0/P1 真正阻止合并，请在 `master` Branch Ruleset 中把 `Aegis Code Review` 配置为 Required Status Check。GitHub 自身的通知设置会负责失败 Check 的站内与邮件通知；Aegis 不额外运行邮件服务。

安全边界：Fork 与 Dependabot Pull Request 属于不可信输入，始终不会获得仓库 Secret。GitHub 会把同仓库分支视为可以访问 Secret 的可信来源，因此应严格限制仓库写权限，并强制审核 `.github/workflows/` 下的改动。作为纵深防御，Aegis 仍会从 Base Commit 构建评审引擎，并从仓库代码控制的子进程环境中移除凭证类变量。

## 本地 CLI（可选）

运行要求：Go 1.23+ 和 Git。

```bash
go build -o aegis ./cmd/aegis
./aegis review --repo . --base master --head HEAD --output review.html
```

在浏览器中打开 `review.html`。HTML 是默认报告格式，因此可以省略 `--format html`。默认情况下，Aegis 会针对发生变更的 Go Package 运行 `go test` 和 `go vet`。

### 启用 DeepSeek Reasoning Agent

远程模型调用必须由使用者显式启用。复制安全配置示例，只把真实 API Key 写入被 Git 忽略的 `.env`，然后在命令行中指定配置文件：

```bash
cp .aegis.example.json .aegis.json
cp .env.example .env
# 编辑 .env，将占位符替换为真实的 DEEPSEEK_API_KEY。

./aegis review \
  --config .aegis.json \
  --repo . \
  --base master \
  --head HEAD \
  --output review.html
```

默认模型为 `deepseek-v4-flash`；如果更重视审核质量，可以在 `.aegis.json` 中将 `model` 修改为 `deepseek-v4-pro`。配置文件包含模型、官方 Base URL、Thinking Mode、推理强度、Agent 预算和 Verifier 超时设置。文件中只保存环境变量名称 `DEEPSEEK_API_KEY`，不会保存真实密钥。

默认只允许使用 DeepSeek 官方 Endpoint。如果需要把代码和凭证发送到兼容代理，必须同时设置自定义 `--agent-base-url` 和显式的 `--agent-allow-custom-endpoint` 参数。

仓库上下文引擎默认启用，并索引仓库内所有 Package，从而发现跨 Package 调用关系。审核大型 Monorepo 时，可以把范围限制到受影响的 Package：

```bash
./aegis review \
  --repo . \
  --base master \
  --head HEAD \
  --context-scope changed \
  --context-max-symbols 40 \
  --context-max-bytes 49152 \
  --output review.html
```

当 `staticcheck` 和 `gosec` 已安装到 `PATH` 时，可以运行全部支持的分析器：

```bash
./aegis review \
  --repo . \
  --base master \
  --head HEAD \
  --analyzers all \
  --output review.html
```

为了保证证据完整性，当前检出的 Worktree 必须保持干净并与指定 Head Commit 完全一致。`--allow-dirty-analysis` 可用于显式的本地实验，但生成的结果可能无法准确对应 Git Revision 比较。

在 CI 或其他工具中使用 JSON：

```bash
./aegis review --repo . --base master --head HEAD --format json --output review.json
```

全部 Review 参数：

```text
--repo       Git 仓库路径（默认 "."）
--config     Aegis JSON 配置文件的显式路径
--base       基础 Git Revision（默认 "master"）
--head       目标 Git Revision（默认 "HEAD"）
--format     html、markdown 或 json（默认 "html"）
--output     输出文件路径，使用 - 输出到标准输出（默认 "-"）
--context    每个 Diff Hunk 的上下文行数（默认 3）
--timeout    Git 操作最大执行时间（默认 30s）
--analyzers  default、all、none 或逗号分隔的分析器列表
--analysis-scope  Package 分析范围：changed 或 all（默认 "changed"）
--changed-lines-only  仅发布新增行上的静态诊断（默认 true）
--analyzer-timeout  每个分析器的最大执行时间（默认 2m）
--allow-dirty-analysis  显式允许在不匹配或非干净 Worktree 中执行分析
--repo-context  为变更 Go 符号构建仓库上下文（默认 true）
--context-scope  仓库上下文范围：changed 或 all（默认 "all"）
--context-max-symbols  上下文中的最大符号数量（默认 40）
--context-max-bytes  上下文数据量预算（默认 49152）
--context-timeout  仓库索引最大执行时间（默认 1m）
--agent-provider  none 或 deepseek（除非配置文件启用，否则默认 "none"）
--agent-model  模型名称（默认 "deepseek-v4-flash"）
--agent-base-url  Provider API Base URL（默认使用 DeepSeek 官方地址）
--agent-api-key-env  保存凭证的环境变量名称（DeepSeek 要求 DEEPSEEK_API_KEY）
--agent-thinking  启用 Thinking Mode（默认 true）
--agent-reasoning-effort  low、medium、high、xhigh 或 max（默认 "high"）
--agent-timeout  完整推理循环的最大执行时间（默认 3m）
--agent-max-steps  最大模型轮次（默认 6）
--agent-max-candidates  最大候选问题数量（默认 12）
--agent-max-input-bytes  发送给模型的序列化证据预算（默认 98304）
--agent-max-output-tokens  每个模型轮次的最大输出 Token（默认 8192）
--agent-allow-custom-endpoint  显式允许向非官方 Endpoint 发送代码和凭证
--verify-agent-candidates  使用确定性证据门验证 Agent 候选（默认 true）
--verifier-timeout  完整 Verifier 阶段的最大执行时间（默认 3m）
--verifier-analyzer-timeout  每个聚焦验证分析器的最大执行时间（默认 2m）
```

Workflow 在内部通过下面的命令发布结果：

```text
aegis github
  --report review.json
  --html-output review.html
  --summary "$GITHUB_STEP_SUMMARY"
  --fail-on p1
  --fail-on-incomplete=true
  --max-annotations 50
```

`--fail-on` 支持 `p0`、`p1`、`p2`、`p3` 或 `none`。该命令面向 GitHub Actions，普通使用者通常不需要手动调用。

## 系统架构

```text
Git Revisions
    │
    ▼
Diff 收集器 ──► Unified Diff 解析器 ──► 受影响 Package 选择器
                                                │
                         ┌──────────────────────┴──────────────────────┐
                         ▼                                             ▼
                确定性分析流水线                           AST + go/types 上下文
          go test · vet · staticcheck · gosec       调用者 · 被调用者 · 接口 · 测试
                         │                                             │
                         └──────────────────────┬──────────────────────┘
                                                ▼
                                  过滤 · 去重 · 排序 · 预算
                                                │
                                                ▼
                                      有边界的推理循环
                              DeepSeek · 按行读取 · 代码搜索
                                                │
                                                ▼
                                     未验证的候选问题
                                                │
                                                ▼
                                      确定性 Verifier
                              身份 · Diff · 快照 · Test · Vet
                                                │
                         ┌──────────────────────┼──────────────────────┐
                         ▼                      ▼                      ▼
                      已验证                  已拒绝                  证据不足
                         │
                         ▼
                      最终 Findings
                         │
                         ┌──────────────────────┼──────────────────────┐
                         ▼                      ▼                      ▼
                    HTML 档案              JSON 契约              Markdown 报告

后续阶段：量化评测 ──► 生产级加固
```

当前 Package 边界：

```text
cmd/aegis/          CLI 入口与用户可见错误
internal/gitdiff/   Git 执行、Revision 解析、Diff 解析
internal/analyzer/  工具执行、Package 范围、分析器、结果聚合
internal/context/   AST/类型索引、关系图、排序、预算
internal/config/    严格 JSON 配置和最小范围的 dotenv 凭证读取
internal/agent/     Provider 协议、DeepSeek 适配器、Prompt、工具、推理循环、校验
internal/verifier/  本地不变量、聚焦分析器、证据关联、候选裁决
internal/githubreport/ P0-P3 映射、GitHub Summary、Annotation、合并门禁
internal/secureenv/    仓库代码子进程的凭证环境变量隔离
internal/review/    所有审核阶段共享的稳定领域模型
internal/report/    自包含 HTML、JSON 和 Markdown Renderer
```

## 完整 Agent 路线图

1. ✅ **确定性分析层**——接入 `go test`、`go vet`、`staticcheck` 和 `gosec`，统一转换为包含证据的 Findings。
2. ✅ **仓库上下文引擎**——在严格预算内检索变更符号、接口、调用者、实现和测试；优先使用准确的类型解析，失败时使用带置信度标记的 AST 推断。
3. ✅ **Reasoning Loop**——规划受限的只读工具调用，保留 Thinking Tool Turn，生成经过本地校验的候选问题，并通过可扩展接口隔离 DeepSeek Provider。
4. ✅ **Verifier 流水线**——验证候选身份和真实位置，重新运行聚焦 Test/Vet，关联独立诊断，校准置信度，对已有证据去重，并拦截缺少支持的问题。
5. ✅ **GitHub Actions 集成**——在 Pull Request 更新时运行审核，发布 Check Summary 和行级 Annotation，上传 HTML Artifact，支持幂等重跑，并隔离不可信贡献代码与 Secrets。
6. **量化评测**——构建包含 Bug PR 和 Clean PR 的数据集，统计 Precision、Recall、误报率、延迟/成本分位数和 Ablation 实验结果。

## 质量门禁

```bash
go fmt ./...
go vet ./...
go test -race ./...
go build ./cmd/aegis
```

未来任何分析器新增的 Finding 都必须包含代码位置、严重程度、类别、来源、置信度和可复现证据。这一契约保证最终 Agent 是可测量的工程系统，而不是主观的模型输出。
