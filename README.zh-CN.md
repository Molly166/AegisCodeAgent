# AegisCodeAgent

[English](README.md) | [简体中文](README.zh-CN.md)

AegisCodeAgent 是一个使用 Go 开发、以证据为基础的 Pull Request 代码审核 Agent。它并非简单包装大模型 API，而是按照完整工程系统建设：确定性分析负责建立事实，仓库上下文引擎负责解释影响范围，大模型基于证据进行推理，Verifier 则在发布前过滤缺少依据的问题。

项目目前处于**阶段五：Verifier 验证流水线**。CLI 能够安全解析 Git Revision，运行确定性 Go 分析器，构建受预算约束的仓库上下文，执行有边界的 DeepSeek 推理循环，并通过独立的本地证据门审核每个 Agent 候选问题。只有获得聚焦确定性诊断支持的候选问题，才能进入最终 Findings 并影响审核结论。

## 当前能力

- 使用三点比较语义（`merge-base...head`）对比两个 Git Revision。
- 解析修改、新增、删除、重命名、二进制、带引号以及 Unicode 路径。
- 保留 Diff Hunk 上下文与新旧行号，供下游分析器使用。
- 默认针对受影响的 Go Package 运行 `go test` 和 `go vet`。
- 支持接入 `staticcheck` 和 `gosec` 的机器可读输出。
- 并发运行分析器，并为每个分析器设置独立超时和输出大小限制。
- 将静态诊断过滤到新增代码行，完成去重、严重程度排序和稳定指纹生成。
- 区分 passed、findings、skipped、unavailable、failed 和 timed out 等分析状态。
- 使用 `go list` 发现仓库 Package，并通过 Go AST 索引代码声明。
- 尽可能使用 `go/types` 建立准确的调用边和接口实现关系。
- 完整类型信息不可用时，降级到带置信度标记的语法推断。
- 定位发生变更的函数、方法、类型、接口、变量、常量、测试和文件级变更。
- 对直接调用者、被调用者、接收者类型、相关接口和相关测试进行排序。
- 对符号数量、代码片段、文件大小和上下文总量实施预算，并记录截断统计。
- 通过 Provider 接口和 Chat Completions Tool Call 协议调用 DeepSeek。
- 在工具调用之间保留 Thinking Mode 上下文，同时不保存或发布模型的私有推理过程。
- 仅允许模型通过只读工具检查受限行范围和纯文本代码搜索结果。
- 要求模型输出 JSON，在本地验证全部候选问题，拒绝不位于新增代码行的问题并分配稳定指纹。
- 将 Agent 候选与确定性 Findings 分离，记录推理步数、工具调用、延迟、Token 使用量和警告。
- 从显式 JSON 配置读取非敏感设置，仅从环境变量或被忽略的 `.env` 文件读取 API Key。
- 重新校验候选身份、变更行归属、仓库边界、符号链接和真实源码快照。
- 针对包含合法 Go 候选问题的 Package 重新运行聚焦 `go test` 和 `go vet`。
- 通过位置重叠、类别兼容和共同缺陷信号关联诊断，而不是只比较问题标题。
- 将每个候选判定为 `verified`、`rejected` 或 `inconclusive`；不会把测试通过错误地视为候选一定不成立。
- 对已经被静态分析覆盖的候选建立关联而不重复发布，只晋升获得新证据支持的候选。
- 校准候选置信度，并将晋升问题的严重程度限制在最强确定性证据的等级以内。
- 生成响应式、可打印、无外部依赖的 HTML 审核报告。
- 支持导出兼容性 Markdown 和用于自动化的版本化 JSON。
- 通过 GitHub Actions 运行单元测试和 Git 集成测试。

## 快速开始

运行要求：Go 1.23+ 和 Git。

```bash
go build -o aegis ./cmd/aegis
./aegis review --repo . --base main --head HEAD --output review.html
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
  --base main \
  --head HEAD \
  --output review.html
```

默认模型为 `deepseek-v4-flash`；如果更重视审核质量，可以在 `.aegis.json` 中将 `model` 修改为 `deepseek-v4-pro`。配置文件包含模型、官方 Base URL、Thinking Mode、推理强度、Agent 预算和 Verifier 超时设置。文件中只保存环境变量名称 `DEEPSEEK_API_KEY`，不会保存真实密钥。

默认只允许使用 DeepSeek 官方 Endpoint。如果需要把代码和凭证发送到兼容代理，必须同时设置自定义 `--agent-base-url` 和显式的 `--agent-allow-custom-endpoint` 参数。

仓库上下文引擎默认启用，并索引仓库内所有 Package，从而发现跨 Package 调用关系。审核大型 Monorepo 时，可以把范围限制到受影响的 Package：

```bash
./aegis review \
  --repo . \
  --base main \
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
  --base main \
  --head HEAD \
  --analyzers all \
  --output review.html
```

为了保证证据完整性，当前检出的 Worktree 必须保持干净并与指定 Head Commit 完全一致。`--allow-dirty-analysis` 可用于显式的本地实验，但生成的结果可能无法准确对应 Git Revision 比较。

在 CI 或其他工具中使用 JSON：

```bash
./aegis review --repo . --base main --head HEAD --format json --output review.json
```

全部 Review 参数：

```text
--repo       Git 仓库路径（默认 "."）
--config     Aegis JSON 配置文件的显式路径
--base       基础 Git Revision（默认 "main"）
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

后续阶段：GitHub PR ──► 量化评测 ──► 生产级加固
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
internal/review/    所有审核阶段共享的稳定领域模型
internal/report/    自包含 HTML、JSON 和 Markdown Renderer
```

## 完整 Agent 路线图

1. ✅ **确定性分析层**——接入 `go test`、`go vet`、`staticcheck` 和 `gosec`，统一转换为包含证据的 Findings。
2. ✅ **仓库上下文引擎**——在严格预算内检索变更符号、接口、调用者、实现和测试；优先使用准确的类型解析，失败时使用带置信度标记的 AST 推断。
3. ✅ **Reasoning Loop**——规划受限的只读工具调用，保留 Thinking Tool Turn，生成经过本地校验的候选问题，并通过可扩展接口隔离 DeepSeek Provider。
4. ✅ **Verifier 流水线**——验证候选身份和真实位置，重新运行聚焦 Test/Vet，关联独立诊断，校准置信度，对已有证据去重，并拦截缺少支持的问题。
5. **GitHub Actions 集成**——在 Pull Request 更新时运行审核，发布 Check Summary 和行级 Annotation，上传 HTML Artifact，支持幂等重跑，并隔离不可信贡献代码与 Secrets。
6. **量化评测**——构建包含 Bug PR 和 Clean PR 的数据集，统计 Precision、Recall、误报率、延迟/成本分位数和 Ablation 实验结果。

## 质量门禁

```bash
go fmt ./...
go vet ./...
go test -race ./...
go build ./cmd/aegis
```

未来任何分析器新增的 Finding 都必须包含代码位置、严重程度、类别、来源、置信度和可复现证据。这一契约保证最终 Agent 是可测量的工程系统，而不是主观的模型输出。
