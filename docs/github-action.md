# 在其他仓库安装 Aegis

> **本修订已完成阶段 2 的配置切换，线上验收待完成。** `.github/workflows/aegis-review.yml` 现在包含四 Job、Docker 隔离和 `workflow_call` 入口，原暂存文件已移除。激活基线为已合入的可信 `master` `9fbdb52c75bb7fba33b2b58ac8e0e2ec70c91bac`。本地配置切换不等于 GitHub 已运行、外部仓库接入成功或分支保护已启用；各项证据和待办见 [v1 激活验收清单](v1-acceptance.md)。

本文后续架构对应当前 v1 配置；对外安装应选择**已合入并完成线上验收的固定修订**。Aegis 以 **Reusable GitHub Workflow** 交付：调用方只需一个 YAML 和可选模型 Key；评审器、容器工具链、HTML、评论和合并检查由 Aegis 工作流负责。多 Job 设计让分析阶段与持有评论写权限的发布阶段运行在不同 runner 上。它不是需要自行部署的 Web 服务，也不是 `steps.uses` 形式的 Composite Action。

目前优先支持 Go 单模块仓库。跨文件语义分析、静态分析器与 Verifier 都以 Go 为主要语言；不要将它理解为所有语言都具有相同检测能力。

## 两阶段升级顺序

2026-09-08 的 [PR #12](https://github.com/Molly166/AegisCodeAgent/pull/12) 同时引入新实现与新工作流，但可信 Base `75fd986` 不包含 `--sandbox` 和新版 Publisher 脚本，导致分析、发布及最终门禁连锁失败。实际模型 Review 步骤没有进入；这不是检测出 P0/P1 的结果。另有独立的 CI lint 与 Docker 集成测试失败，见[验收记录](validation.md)。

为避免通过信任 PR Head 绕过边界，修复采用以下顺序：

1. **阶段 1：`fix/aegis-v1-bootstrap-ci-20260908`。** 保留升级前的实际 Review 工作流，将新版评审器、DockerRunner、发布脚本与 CI 修复先送审；新版工作流暂存于 `examples/aegis-review-v1-migration.yml`，参与契约测试和 lint，但 GitHub 不会从 `examples/` 启动它。完成 CI、代码审核后，由维护者合入 `master`。本阶段仍使用旧单 Job/宿主执行路径，不具备新版独立 Publisher 与 Docker PR 执行边界；也不能把这个阶段的 SHA 用作下面的 `workflow_call` 安装版本。
2. **阶段 2：`feat/v1-workflow-activation-20260916`。** 从已包含前置实现及后续修复的 `master` `9fbdb52` 新建分支，将暂存工作流原样启用到 `.github/workflows/aegis-review.yml` 并移除暂存文件。本修订已完成此配置变更；推送后还需验证四 Job、报告反馈与门禁。不得提前针对旧 Base 启用；仅在同一个 PR 中拆成两个 commit 不能解决此问题。
3. 两阶段都通过审核和线上验收后，才选择固定完整 SHA 供外部仓库复用。不要通过自动改用目标 Head、忽略检查错误或关闭门禁来完成迁移。

上述两阶段替代 PR #12 的一次性升级路径；**不会自动关闭或合并任何 PR**。历史 PR 保留用于故障追踪，是否关闭由维护者决定。阶段 2 的本地配置和测试不代表已推送、已合入或线上检查已通过。报告发布器预先认可的 v1 工作流 SHA256 为 `5c1ca01490ab638d8ffa3c2835a79542ea48d49618ac79c1d746838c825c1cce`，本次原样迁移保持该身份不变。

## 1. 固定一个已审核的 Aegis 版本

先选择已完成阶段 2、合入且通过 CI 与接入验收的 Aegis release commit，并复制完整的 40 位 SHA。下面的 `REPLACE_WITH_RELEASE_COMMIT_SHA` 是占位符，必须替换后使用；本文不声称 `v1` 或 `v1.0.0` 已经发布。

在你的仓库添加 `.github/workflows/aegis.yml`：

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
  aegis:
    uses: Molly166/AegisCodeAgent/.github/workflows/aegis-review.yml@REPLACE_WITH_RELEASE_COMMIT_SHA
    with:
      provider: deepseek
      model: deepseek-v4-flash
      fail-on: p1
      fail-on-needs-review: p0
    secrets:
      provider-api-key: ${{ secrets.DEEPSEEK_API_KEY }}
```

无需自己 checkout、安装 Go、配置缓存或编译 Aegis。工作流通过 GitHub 本次运行的 `referenced_workflows` 元数据解析被调用的 Aegis SHA，再从 `Molly166/AegisCodeAgent` 检出这个 SHA。它不会在外部仓库里寻找 `./cmd/aegis`，也不会把调用方的 `github.sha` 当作评审器版本。一次 caller run 只调用一份 Aegis 工作流；重复引用产生歧义时会拒绝执行。

在 Aegis 自己的仓库里，直接触发 `.github/workflows/aegis-review.yml` 时仍从 PR Base 编译可信评审器。目标代码使用事件里的精确 Head SHA，而不是 GitHub 自动生成的合并提交。

## 2. 配置模型

将 Key 放入仓库 **Settings → Secrets and variables → Actions**。不要提交 `.env`，不要使用 `secrets: inherit`。

OrcaRouter 调用方式：

```yaml
    with:
      provider: orcarouter
      model: deepseek/deepseek-v4-flash
    secrets:
      provider-api-key: ${{ secrets.ORCAROUTER_API_KEY }}
```

通用 OpenAI-compatible 接口：

```yaml
    with:
      provider: openai-compatible
      model: YOUR_TESTED_MODEL_ID
      base-url: https://your-provider.example/v1
      allow-custom-endpoint: true
    secrets:
      provider-api-key: ${{ secrets.AEGIS_API_KEY }}
```

`base-url`、模型与权限策略由仓库维护者在 Workflow 中设置。CI 禁止读取目标仓库的 API 配置和 `.env`。自定义 HTTPS endpoint 需要显式开启 `allow-custom-endpoint`；该设置表示你同意向这个地址发送代码和对应 API Key。模型名称可配置不等于已经验证了它的 Tool Calling/JSON 能力，应先完成 Provider Contract Tests 和 Live Eval。

不接入外部模型时设 `provider: none` 并省略 `secrets`。缺少可用 Key 时工作流也会明确降级为静态评审。Fork PR 和 Dependabot PR 不接收模型 Key。

## 3. 配置合并门禁

第一次运行后，在分支 Ruleset/Branch protection 中把实际显示的 **Aegis merge gate** 检查设为必需项（Reusable Workflow 的检查名可能带调用方 Job 前缀）。仅有一个失败的 Workflow，不代表 GitHub 已经禁止所有人点 Merge；必须配置必需状态检查及相应绕过权限。

| 输入 | 默认 | 含义 |
| --- | --- | --- |
| `fail-on` | `p1` | 阻止 P0/P1；P2/P3 仍展示 |
| `fail-on-needs-review` | `p0` | 未确认的 P0 假设要求人工核验；可设 `p1` 或 `none` |
| `fail-on-incomplete` | `true` | 必需证据阶段不完整时阻断 |
| `require-agent` | `false` | 为 `true` 时要求模型评审完成；静态降级/Fork 也会被阻断 |

可选模型或上下文降级应明确展示，不会单凭 P2/P3 Finding 阻断。Publisher 在新 runner 中重新计算策略；分析脚本不会把所有非零退出都等同为 P0/P1。缺失 JSON、SHA 不匹配、异常退出或必需阶段失败仍会失败。

## 4. 查看结果

- 同仓库 PR：更新同一条 `github-actions[bot]` 评论，提供结论、Findings 和完整报告链接。
- Fork/Dependabot：通常没有 PR 评论写权限，结果保留在 Check、Job Summary 和 Artifact；这是权限降级，不是“没有反馈”。
- 同一 run attempt 的完整报告命名为 `aegis-review-report-<attempt>`，包含 `review.html`、`review.json`、摘要与发布策略；默认保留 14 天。
- 发布前会检查 PR 当前 Base/Head，旧 run 不覆盖新提交的评论。
- HTML 默认是私有访问边界内的 Artifact，需要下载后打开。可选公开 HTTPS 发布见 [report-hosting.md](report-hosting.md)。

## 执行边界和限制

分析 Job 的 GitHub Token 仅有读取权限。只有可信 Go 进程取得模型 Key；所有会加载/执行 PR Go 代码的子命令进入 DockerRunner，使用只读源码、独立 PID 命名空间、无网络、无额外 Linux capabilities、只读根文件系统和受限 CPU/内存。容器不挂载宿主 Home、GitHub 工作流控制文件、Docker socket 或模型凭据。

模块依赖在模型 Key 注入之前，由单独的无凭据容器预下载。它只运行固定的 `go mod download`，使用公共 Go Proxy，禁止自动切换工具链和 Go workspace；之后分析容器只读挂载模块缓存。需要私有依赖凭据、外部服务、网络访问或写源码目录的测试，可能导致分析不完整。当前不会为了让检查变绿而解除隔离；这类仓库需要后续提供专门的可信依赖供应与测试配置。

DockerRunner 不是虚拟机级别隔离，应使用 GitHub 托管临时 runner，不建议直接在含内部凭据的长期自托管 runner 上执行不可信 PR。针对越权读取环境、宿主文件、源码写入的容器集成用例由 CI 验证；单纯“清理环境变量”不构成隔离。

Docker 源码挂载额外要求独立、已提交的干净 Git checkout：拒绝本地修改、未跟踪/忽略文件、隐藏修改的索引标记以及已初始化子模块，避免把本地 `.env` 一起挂进容器。`.git` 元数据在容器内被空只读挂载覆盖，且关闭 VCS 构建标记。请使用一次性 clone，不要将带凭据或开发产物的日常工作区作为不可信 PR 的执行目录。

从旧版本升级时，如果可信 Base 尚不具备 DockerRunner 和新 Publisher，直接启用新版工作流的 PR 会 fail closed。请按本文的两阶段顺序先升级可信实现，再启用新工作流；“失败后直接合入”不是迁移步骤。阶段 2 应在阶段 1 合入后从最新 `master` 新建分支，再将暂存文件移到正式工作流位置；不要提前向旧 Base 创建激活 PR。工作流不会编译 PR Head 的 Publisher 来绕开信任边界。

GitHub 平台依据：[复用工作流](https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows)、[Workflow Run API 与 referenced_workflows](https://docs.github.com/en/rest/actions/workflow-runs#get-a-workflow-run)、[安全使用 GitHub Actions](https://docs.github.com/en/actions/reference/security/secure-use)。

## 构建发布包

维护者确认代码合入 `master`、CI 和在线验收通过后，创建自己的版本标签。随后手动运行 **Build release artifacts** 并输入现有标签。

工作流验证标签是受审 `master` 提交的祖先，执行测试，然后生成 Linux/macOS/Windows × amd64/arm64 的六组文件、`checksums.txt` 与 `build-info.json`。使用固定 Go 版本、`-trimpath`、确定性归档时间和 `-X main.version`。在相同源码和工具链下可以复现产物。

该工作流只有读取权限，只上传 release candidates，不创建标签、发布 GitHub Release 或移动 `v1` 标签。维护者审核并校验 SHA256 后再发布。升级消费者仓库时通过 PR 更新所固定的完整 SHA。
