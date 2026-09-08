# v1 发布候选验收记录

记录日期：2026-09-08。首轮候选为 `ee4cfb520c894bc7dc32fdd75043552d94857e61`，基于 `master` 提交 `75fd9868f468dea955fe490391761f058327068e`。下文区分首轮本地测试、已观察到的 GitHub 失败与修复待验收项，不代表已经发布 Release 或线上全链路成功。

## 当前迁移状态与线上失败

- 存在 `examples/aegis-review-v1-migration.yml`：**阶段 1**，实际 Review 工作流保留旧 `master` 版本；新版隔离与可复用工作流尚未在线启用，代码和 CI 需要验收。
- 暂存文件已移除，且实际 `.github/workflows/aegis-review.yml` 已是 v1 四 Job：**阶段 2**，工作流配置已切换；仍须以该提交的真实运行结果确认上线成功。不能只根据文件缺失断言完成。
- 顺序为 `fix/aegis-v1-bootstrap-ci-20260908` 经审核合入 `master`，再启用 `fix/aegis-v1-workflow-activation-20260908`。第二阶段的可信 Base 必须已包含新版 CLI、容器与 Publisher 脚本；不能默认信任 PR Head 或忽略失败来迁移。详见[两阶段升级说明](github-action.md)。

[PR #12](https://github.com/Molly166/AegisCodeAgent/pull/12) 的首轮运行在 2026-09-08 出现 **2 个 Workflow、7 个 Job，其中 5 个失败、2 个成功**，并非重复发起 7 次模型 Review：

| 线上检查 | 观察结果与原因 |
| --- | --- |
| [Aegis Code Review](https://github.com/Molly166/AegisCodeAgent/actions/runs/34208979019) | 可信版本解析成功；旧 Base 缺少 `--sandbox` 与新发布脚本，分析构建检查、Publisher 和最终门禁连锁失败。`Review exact PR Head` 被跳过，没有进入模型评审；HTML/评论也未完成交付。 |
| [CI / verify](https://github.com/Molly166/AegisCodeAgent/actions/runs/34208979112/job/102005172239) | `publish-report-pages.yml:118` 与 `release.yml:60` 的单引号 `printf` 输出 Markdown 反引号，触发 ShellCheck `SC2016: Expressions don't expand in single quotes`。后续该 Job 的 Go 测试与扫描尚未执行。首轮本地未安装 ShellCheck，不能将当时 Actionlint 通过等同于 Ubuntu 上的完整检查。 |
| [CI / Docker isolation integration](https://github.com/Molly166/AegisCodeAgent/actions/runs/34208979112/job/102005172489) | 镜像构建成功，但 `docker_integration_test.go:60` 报告 `fork/exec /tmp/go-build…/isolated.test: permission denied`：临时挂载的默认 `noexec` 阻止 Go 测试二进制执行。修复采用专用 `/aegis-tmp` 可执行临时挂载及 `GOTMPDIR`，普通 `/tmp` 保持 `noexec`；真实 Linux 重跑仍待验收。 |
| [CI / Offline executable pipeline health](https://github.com/Molly166/AegisCodeAgent/actions/runs/34208979112/job/102005172508) | 离线实代码执行健康检查成功；未调用模型，不能据此推断生产 Review 或模型准确率通过。 |

本轮通过两阶段迁移和 CI/容器修复处理上述问题，**修复后的 GitHub 结果待验收**。两阶段替代 PR #12 的一次性升级路径；不自动关闭或合并原 PR，保留其历史供追踪。阶段 1 经审核合入后，阶段 2 必须从届时最新的 `master` 新建分支启用，不提前对旧 Base 发起激活 PR。

## 本轮修复的本地回归

2026-09-08，Go 1.26.6 / macOS arm64，重新执行并通过：

- `go test -race ./...`：14 个包通过；本次未重新统计覆盖率。
- Node 交付、迁移与脚本检查：26/26 通过；覆盖旧 Base、缺少 Publisher、阻断 HTML、过期提交和伪造评论标记。
- Actionlint 1.7.12 + ShellCheck 0.11.0：全部实际 Workflow、暂存 v1 Workflow 和 `scripts/*.sh` 通过；缺少 ShellCheck 会明确失败。
- gofmt、`go vet`、Staticcheck 0.7.0、Gosec 2.28.0 高严重度扫描、`git diff --check`：通过。
- Golden Replay：50/50 通过。实代码离线样本：12/12 执行完整，2/8 缺陷检出、6/12 标签判定符合、0 次模型 API 调用；这不是模型准确率验收。
- Docker 参数和边界单测通过；真实 Docker 集成测试在本机明确跳过，不能等同于 Linux 容器验收通过。

本轮还补充了可信源码协议预检和不依赖 Base 脚本的失败反馈；执行失败时，HTML 与摘要均明确置顶阻断说明，不再仅修改退出码。

## 已实现范围

- DeepSeek、OrcaRouter 和通用 OpenAI-compatible Provider；显式模型能力、超时/重试预算、凭据隔离及请求元数据。
- v1 可复用 GitHub Workflow 的实现与待启用配置：可信评审器、精确 Base/Head、Docker 分析、独立 Publisher 和最终门禁；在线启用受上述迁移阶段约束。
- P0–P3 统一展示；未确认假设不再消失，P2/P3 不会仅因自身等级阻断；模型不能降低独立确定性证据的风险等级。
- 50-case Golden Replay 和独立的 12-case 实代码 Eval；重复运行、Verifier 消融、带分母的指标和真实证据保留。
- 自包含 HTML、PR 评论、行标注、手动确认的公开 HTTPS 托管工作流，以及六平台发布候选构建。
- 四语言 README、Provider/接入/托管/安全与贡献说明。

v1 最低构建版本为 **Go 1.24**，使用 `os.Root` 限制模型工具实际打开文件时的仓库边界。以下最终测试使用 **Go 1.26.6 / macOS arm64**，与 CI 固定的 Go 版本一致。

## 首轮候选已实际执行的本地检查

下面保留首次候选的观测值。修复代码和工作流之后必须重新执行，不能将这组历史结果写成修复版本已通过。

| 检查 | 本地结果 |
| --- | --- |
| `go test -race ./...` | 全部 14 个包通过；语句覆盖率约 **76.2%**，不是缺陷检出率 |
| `go vet ./...` | 通过 |
| Staticcheck `v0.7.0` | 通过 |
| Gosec `v2.28.0 -severity high` | 通过：未报告高严重度诊断 |
| Gosec 全量扫描 | 仍有 **11 Medium、2 Low** 诊断，已保留；没有声称零安全告警 |
| Node 发布/反馈安全测试 | **15/15** 通过 |
| Actionlint `v1.7.12`、Bash 语法、gofmt、diff whitespace | 首轮本地通过；当时没有 ShellCheck，后续 Ubuntu CI 的完整 lint 失败 |
| 50-case Golden Replay | **50/50** 通过；独立临时目录重生成后与仓库语料一致 |
| 六平台交叉编译 | Linux/macOS/Windows × amd64/arm64 通过；本机运行验证 `-X main.version` 生效 |

Gosec 剩余项主要涉及受配置控制的子进程/文件路径、公开 Golden 语料目录权限和清理错误处理。它们没有被全局排除，也不能仅因低于默认门禁就视为不存在。对个别已审查的误报（例如环境变量**名称**被识别为硬编码 Key）仅使用带原因的局部标注。

## 实代码离线基线：不要冒充模型准确率

配置：`provider=none`、`analyzers=default`（go test/go vet）、Verifier 开启、每个样本运行一次。12 个合成 Git PR：8 Bug、4 Clean。

| 指标 | 观测结果 |
| --- | --- |
| 执行完整 | 12/12 |
| 检出已标注缺陷 | 2/8，Recall 25% |
| 命中的已发布 Finding | 2/2；样本极小，不能外推准确率 |
| 门禁符合标签 | 6/12 |
| 干净样本误阻断 | 0/4 |
| API 调用 / Token | 未启用模型 / 0 |

这些漏检被保留，没有通过修改标签或使用固定报告替代实际执行来隐藏。该基线并非四分析器全开的结果，也不包含 DeepSeek/OrcaRouter 实际调用。Golden 回放的 100% 不能与这里的检出率混用。

本地生成的证据位于被 Git 忽略的 `artifacts/local-v1-validation/`：`golden.json/html`、`live.json/html`、`coverage.out`、`gosec.json`。Live JSON 记录 Corpus/Binary SHA256、实际 Go 版本、精确 Base/Head 和完整评审结果；本机路径不属于可移植发布包。

## 尚未完成的外部验收

1. **真实模型：** 尚未消费用户 API 配额。需要用明确可访问的固定模型，在相同语料、预算、分析器和门禁配置下进行多次 DeepSeek/OrcaRouter 实测与 Verifier 消融，保留超时/限流/漏检结果。
2. **真实 Docker：** 首轮本机没有 Docker；容器参数与边界单测通过，但 PR #12 的 Linux 真实容器集成测试失败。修复需要在 Linux CI 重跑并保留结果，尚不能声称容器隔离验收通过。
3. **GitHub 在线链路：** PR #12 已运行但升级失败。两阶段修复后仍需验证外部仓库复用、同仓库 PR、Fork 无 Key 降级、P0/P1 阻断、P2/P3 放行、P0 Needs Review、失败报告保留和旧 Head 不覆盖新评论。
4. **首次升级：** 先将实现通过阶段 1 合入可信 Base，再由阶段 2 启用工作流；仍须审核并验收各阶段。不得自动使用不可信 Head 评审器绕过，也不能把阶段 1 旧宿主执行链路称为新版沙箱已生效。
5. **公开报告与发布包：** 尚未部署 Pages、创建标签或发布 Release。Linux GNU tar/zip 归档流程还需线上运行。公开报告必须确认无敏感内容，且会替换当前 Pages 站点。

因此，当前定位是**实现已提交过首轮候选、存在已知线上升级与 CI 失败、正在修复并等待验收的 v1 发布候选**，不是“已证明生产准确率的最终商用版”。完成上述验收后再发布不可变版本，并让消费者通过 PR 固定该版本 SHA。

## 复现命令

```sh
GOTOOLCHAIN=go1.26.6 go test -race ./...
GOTOOLCHAIN=go1.26.6 go vet ./...
GOTOOLCHAIN=go1.26.6 go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
GOTOOLCHAIN=go1.26.6 go run github.com/securego/gosec/v2/cmd/gosec@v2.28.0 -quiet -severity high ./...
GOTOOLCHAIN=go1.26.6 go build -trimpath -o /tmp/aegis ./cmd/aegis
/tmp/aegis eval --corpus eval/cases --format html --output /tmp/aegis-golden.html
/tmp/aegis eval-live --corpus eval/live --agent-provider none \
  --analyzers default --format html --output /tmp/aegis-live.html
```

若已安装的 Go 不在 PATH 或与发布二进制版本不同，为 `eval-live` 加上 `--go-binary /absolute/path/to/matching/go`。该工具只接受匹配的本地编译器，不隐式下载或切换版本。
