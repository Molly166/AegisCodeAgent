# AegisCodeAgent

[English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md)

**运行在 GitHub PR 中的 Go 代码评审 Agent。** 结合静态分析、仓库上下文、模型推理和独立证据验证，输出 P0–P3 风险结论、代码行标注与 HTML 证据报告。无需部署独立 App 或常驻服务。

> **当前为 v1 发布候选开发版。** 已实现可复用 Workflow、多 Provider、隔离分析和真实代码评测；不代表已发布版本标签、完成在线模型验收或部署报告网站。[已验证范围与限制](docs/validation.md)。

> **两阶段迁移提示：** 仓库存在 `examples/aegis-review-v1-migration.yml` 时属于阶段 1，实际 PR 工作流保留旧版本，下文的四 Job 沙箱/可复用工作流**尚未启用**。先将实现合入可信 `master`，再删除暂存文件并将实际 `.github/workflows/aegis-review.yml` 切换为 v1，才进入阶段 2。PR #12 已暴露升级及 CI 失败，本轮修复仍待线上验收；配置切换也不等于上线通过。请按[两阶段顺序](docs/github-action.md)执行，不得默认信任目标 Head 来绕过。

## 新人先看：它如何工作？

以下是 v1 工作流完成启用与验收后的行为：你向分支提交代码并创建/更新 PR，GitHub Actions 自动启动 Aegis，审核精确的 PR Head。完成后，在 PR 中更新同一条 Bot 评论，给出结论、问题位置和完整报告链接，并通过独立的 **Aegis merge gate** Check 返回门禁结果。

- **先看结论和 Findings：** 统一 P0–P3，展示代码位置、证据和建议。
- **不把不确定当作没问题：** 未确认假设进入 `Needs Review`；执行不完整、覆盖降级单独展示。
- **门禁可配置：** 默认阻断有证据的 P0/P1 和未确认的 P0 假设；P2/P3 本身不阻断。
- **模型可选：** 支持 DeepSeek 直连、OrcaRouter 预设及显式配置的 OpenAI-compatible 接口。无 Key 时为静态模式，设置 `require-agent: true` 才强制模型评审完成。
- **报告可追溯：** 默认上传 HTML/JSON Artifact；公开 HTTPS 发布为独立、人工确认的可选流程，不会自动公开 PR 代码。

**必须在仓库 Ruleset/分支保护中，将实际显示的 Aegis merge gate 设为必需检查，GitHub 才会按该检查限制合入。** 调用方 Job 可能为检查名添加前缀。

## 项目架构

```text
PR opened / synchronize
          ↓
解析可信评审器版本、精确 Base / Head
          ↓
Diff → 并发静态分析 → 仓库上下文
          │ go test / vet / staticcheck / gosec
          │ AST / 类型 / 调用关系 / 测试 / PR 意图 / 仓库规范
          ↓
Reasoning Agent ⇄ 有预算和路径限制的只读工具
          ↓
Verifier → Verified / Needs Review / Rejected
          ↓
独立 Publisher → HTML + 代码行标注 + PR 评论
          ↓
独立必需 Check → 合并策略
```

静态诊断携带分析器证据。模型只能提出 Candidate，经过身份、Diff 位置、源码快照及独立诊断/语义证据验证后，才能成为最终 Finding。Verifier 不是通用漏洞证明器：不能证实的语义问题仍交由人工核验，模型不直接决定是否合入。

| 模块 | 职责 | 代码位置 |
| --- | --- | --- |
| Diff | 精确版本、三点比较、变更行和重命名 | `internal/gitdiff` |
| Analysis | 并发工具、超时、输出限制和 Docker 执行 | `internal/analyzer` |
| Context | 预算约束的 Go AST/类型/符号关系、测试关联、PR 意图和规范 | `internal/context` |
| Agent | Provider 能力适配、工具循环、结构化候选和请求追踪 | `internal/agent` |
| Verifier | 源码感知证据关联、候选裁决与保守晋升 | `internal/verifier` |
| Publisher | 独立门禁、评论/标注、HTML 渲染 | `internal/githubreport`、`internal/report` |
| Eval | 固定报告回放与真实代码运行两套评测 | `internal/eval`、`internal/liveeval` |
| Delivery | 可复用 PR Workflow、发布包和可选报告托管 | `.github/workflows`、`scripts` |

### 为什么要拆分分析与发布？

Aegis 自身评审从可信 PR Base 编译评审器；其他仓库调用时，从被调用 Aegis Workflow 的实际 SHA 编译。**不会使用目标 PR Head 中的代码编译评审器。**

分析 Job 仅有 GitHub 读取权限；可能加载或执行 PR Go 代码的命令进入无网络 Docker 容器：只读源码、独立资源限制、不挂载模型 Key。公共模块依赖先由无凭据容器预下载。Publisher 在新的 runner 中验证提交身份，并从 JSON 重新生成 HTML、评论及门禁。

容器不等于虚拟机级别隔离。本地默认 `--sandbox host` 仅适用于可信仓库；CI 不会为使检查通过而自动解除隔离。[安全边界](SECURITY.md)。

## 其他仓库如何开箱使用？

阶段 2 完成验收后，只需添加一份 Workflow，无需复制 Aegis 源码或自行维护服务。阶段 1 的旧实际工作流不支持此 `workflow_call` 接入，不可用该阶段的 SHA 替换占位符。在目标仓库创建 `.github/workflows/aegis.yml`：

```yaml
name: Aegis review
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review]
permissions:
  actions: read
  contents: read
  pull-requests: write
jobs:
  review:
    uses: Molly166/AegisCodeAgent/.github/workflows/aegis-review.yml@REPLACE_WITH_RELEASE_COMMIT_SHA
    with:
      provider: deepseek
      model: deepseek-v4-flash
      fail-on: p1
      fail-on-needs-review: p0
      require-agent: false
    secrets:
      provider-api-key: ${{ secrets.DEEPSEEK_API_KEY }}
```

`REPLACE_WITH_RELEASE_COMMIT_SHA` 必须替换为**已经审核、发布且包含该 Workflow 的完整提交 SHA**。它不是可以直接运行的版本名；本文不声称 `v1` 标签已经发布。

在 **Settings → Secrets and variables → Actions** 添加模型 Key。使用 OrcaRouter 时，改为 `provider: orcarouter`、选择经过测试的模型 ID（预设示例 `deepseek/deepseek-v4-flash`），并引用 `secrets.ORCAROUTER_API_KEY`。模型可用性以实际服务为准。不使用模型时设置 `provider: none`，省略 secrets。

Fork/Dependabot PR 不取得模型凭据；无评论写权限时，仍通过 Checks、Summary 和 Artifact 查看结果。设置 `require-agent: true` 会有意阻断这种静态降级。上下文/可选模型的降级不会凭空变成 P0/P1，但必需证据缺失仍默认失败。

完整参数、权限、首次升级迁移和依赖限制见 [GitHub 接入说明](docs/github-action.md)。旧 Base 尚不支持新 Sandbox/Publisher 时，首次迁移 PR 会 fail closed，需要维护者审核迁移，不能通过编译 Head 的评审器来绕过信任边界。

## 本地开发与调试

需要 Go 1.24+、Git；v1 使用 `os.Root` 限制仓库文件读取边界。CI 将评审器与分析器固定为同一 Go 工具链。Docker 模式额外要求 Linux Docker 和可信分析镜像。

若 Shell 通过 `GOTOOLCHAIN` 固定了旧版 Go，请为构建和测试显式选择兼容版本（CI 为 `go1.26.6`）。精简的发布二进制运行 `eval-live` 时，可通过 `--go-binary /绝对路径/go` 指定匹配的本地工具链；评测器不会自动下载编译器。

```sh
git clone https://github.com/Molly166/AegisCodeAgent.git
cd AegisCodeAgent
go test ./...
go build -trimpath -o /tmp/aegis ./cmd/aegis

# 在可信、干净且检出于目标 Head 的工作区执行。
/tmp/aegis review --repo . --base origin/master --head HEAD \
  --agent-provider none --analyzers default \
  --format html --output /tmp/aegis-review.html
```

`default` 为 `go test/go vet`；`all` 还需要 `staticcheck/gosec`，CI 镜像已包含。CLI 是底层执行引擎，不是需要用户常驻启动的独立产品；`review` 生成证据，`github` 独立计算发布策略。

`.aegis.example.json` 是不含密钥的配置示例，通过 `--config` 显式加载。API Key 放环境变量，本地也可使用忽略提交的 `.env`；CI 禁止读取目标 PR 的配置和 `.env`。模型能力、超时/重试预算、自定义 HTTPS 地址授权和请求追踪见 [Provider 文档](docs/providers.md)。

## Eval：回归通过 ≠ 真实检出率

| 套件 | 输入 | 验证什么 |
| --- | --- | --- |
| 50 个 Golden 回放用例 | 27 Bug、15 Clean、5 Needs Review、3 韧性场景的固定报告 | 匹配器、门禁和结果契约是否回归；**不是**实时模型准确率 |
| 12 个真实代码用例 | 8 Bug、4 Clean 的合成 Git Base/Head | 实际运行评审二进制，保留漏检、误拦截、不完整、耗时和使用量 |

```sh
# 固定报告与门禁回归，默认严格模式。
/tmp/aegis eval --corpus eval/cases --format html --output /tmp/aegis-golden.html

# 真正执行代码，无模型费用；如实呈现静态分析漏检。
/tmp/aegis eval-live --corpus eval/live --agent-provider none \
  --analyzers default --repeats 1 --format html --output /tmp/aegis-live.html
```

真实评测记录语料/二进制哈希、工具链、精确提交、Provider/Model、原始报告及带分母的指标；不会拿 Golden Report 代替真实执行。当前匹配采用保守的词项规则，不是独立语义裁判；12 个合成样本不足以证明生产效果。

开启真实模型的 `eval-live` 必须显式指定模型，会消耗你的 API 配额。应固定语料、模型、预算和分析器，使用重复运行及 Verifier 消融比较，方法见 [Eval 说明](eval/README.md)。Token 缺失表示未知，不伪造美元成本。

## 报告、发布与贡献

- [报告托管](docs/report-hosting.md)：默认有访问边界的 Artifact；公开 Pages 必须人工确认，不能用于保密源码。
- [GitHub 接入与发布包](docs/github-action.md)：六平台构建、SHA256 与完整 SHA 固定；构建产物不等于已经发布 Release。
- [贡献指南](CONTRIBUTING.md)：测试、语料变更、安全负向用例。
- [安全说明](SECURITY.md)：凭据、提示注入、依赖和容器限制。
- [验收记录](docs/validation.md)：已实际运行的验证，以及仍需外部环境完成的验收。

本版本聚焦 Go 单模块仓库。其他语言/配置文件可以提供推理上下文，但没有同等静态和语义验证覆盖。私有依赖供应、通用语义证明、自动修复和生产级准确率承诺不属于本发布候选的已验收能力。

## 许可

[MIT](LICENSE)。
