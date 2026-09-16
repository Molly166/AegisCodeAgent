# v1 发布候选验收记录

## 当前修订：阶段 2 配置已激活，线上验收待完成（2026-09-16）

- 激活分支：`feat/v1-workflow-activation-20260916`，基于可信 `master` `9fbdb52c75bb7fba33b2b58ac8e0e2ec70c91bac`（PR #17 合入后）。
- 已将预审的四 Job 工作流原样切换为正式 `.github/workflows/aegis-review.yml`，移除暂存入口，保持报告发布器认可的 v1 SHA256 不变。
- 只读查询已确认上述基线的 [GitHub CI 35076687212](https://github.com/Molly166/AegisCodeAgent/actions/runs/35076687212) 三个 Job 成功：`verify`、`Docker isolation integration`、`Offline executable pipeline health (not model accuracy)`。这是**激活之前的基线 CI**，不能替代激活分支自己的运行。
- 本次本地检查、门禁矩阵、推送后验收步骤与证据记录见 [v1 激活验收清单](v1-acceptance.md)。本修订未声明新版 PR Review 已上线、已完成外部仓库复用验收、已发布 Release 或已测得真实模型准确率。

## 历史记录边界

以下保留 2026-09-08 的原始阶段 1 验收与失败记录，其中“当前”“本轮”“尚未激活”等表述属于当时的快照，不代表 2026-09-16 的配置状态。模型实测及其他外部验收仍需独立证据，不能由配置激活推断通过。

历史记录日期：2026-09-08。首轮候选为 `ee4cfb520c894bc7dc32fdd75043552d94857e61`，基于 `master` 提交 `75fd9868f468dea955fe490391761f058327068e`。阶段 1 修复已通过 [PR #13](https://github.com/Molly166/AegisCodeAgent/pull/13) 合入，当时可信 `master` 为 `24507c8`；该轮 Go Test 误报修复分支为 `fix/go-test-skip-false-positive-20260908`。这些记录不代表已经发布 Release 或新版 Review 全链路已上线。

## 历史迁移状态与线上验收（2026-09-08）

- **阶段 1 已合入：** `fix/aegis-v1-bootstrap-ci-20260908` 的实现通过 PR #13 进入可信 Base，CI 的 3 个 Job 均已成功，包括真实 Linux Docker 集成测试。
- **阶段 2 尚未激活：** `examples/aegis-review-v1-migration.yml` 仍为待启用配置，实际 `.github/workflows/aegis-review.yml` 保留旧链路。不能把 CI 中容器测试成功表述为线上 PR Review 已使用新版四 Job 沙箱链路。
- 先验收并合入当前 Go Test 误报修复，再从届时最新 `master` 新建阶段 2 激活分支。可信 Base 必须已包含新版 CLI、容器、Publisher 和解析修复；不能默认信任 PR Head 或忽略失败来迁移。详见[两阶段升级说明](github-action.md)。

### 阶段 1 的后续运行：基础 CI 已通过，Review 暴露跳过用例误报

| 线上检查 | 已确认结果 |
| --- | --- |
| [CI：34212218038](https://github.com/Molly166/AegisCodeAgent/actions/runs/34212218038) | 3 个 Job 全部成功：`verify`、真实 `Docker isolation integration`、离线实代码执行健康检查。此前 ShellCheck 与容器临时目录执行权限问题已通过这次线上运行验证。 |
| [Aegis Review：34212218080](https://github.com/Molly166/AegisCodeAgent/actions/runs/34212218080/job/102015619903) | 旧可信 Base 的 Go Test 解析器把 `t.Skip` 的源码定位输出误判为 P1，导致最终门禁失败。报告中的 3 个 P2 不构成默认阻断；可选 Reasoning Agent 的 partial 也不是本次阻断原因。 |

当前修复按 `Package/Test` 的终态识别 Go Test 事件：`pass` / `skip` 不产生失败 Finding，`fail` / `build-fail` 聚合为 P1；截断、取消、畸形和空输出保持 incomplete，源码定位采用保守策略。新增事件协议回归与真实 Go 测试，防止再把普通日志或跳过提示当成失败证据。Docker 跳过提示改为 `fmt.Println` + `t.SkipNow`，仅兼容仍在运行的旧 Base 解析器；跳过条件和真实 Linux Docker CI Job 均未移除或放宽。

上述线上成功属于阶段 1 的已运行版本；**当前误报修复分支仍需在推送后验证自己的 GitHub 检查**，不能沿用前一版本的 CI 结论。

### 首轮失败记录（保留历史）

[PR #12](https://github.com/Molly166/AegisCodeAgent/pull/12) 的首轮运行在 2026-09-08 出现 **2 个 Workflow、7 个 Job，其中 5 个失败、2 个成功**，并非重复发起 7 次模型 Review：

| 线上检查 | 观察结果与原因 |
| --- | --- |
| [Aegis Code Review](https://github.com/Molly166/AegisCodeAgent/actions/runs/34208979019) | 可信版本解析成功；旧 Base 缺少 `--sandbox` 与新发布脚本，分析构建检查、Publisher 和最终门禁连锁失败。`Review exact PR Head` 被跳过，没有进入模型评审；HTML/评论也未完成交付。 |
| [CI / verify](https://github.com/Molly166/AegisCodeAgent/actions/runs/34208979112/job/102005172239) | `publish-report-pages.yml:118` 与 `release.yml:60` 的单引号 `printf` 输出 Markdown 反引号，触发 ShellCheck `SC2016: Expressions don't expand in single quotes`。后续该 Job 的 Go 测试与扫描尚未执行。首轮本地未安装 ShellCheck，不能将当时 Actionlint 通过等同于 Ubuntu 上的完整检查。 |
| [CI / Docker isolation integration](https://github.com/Molly166/AegisCodeAgent/actions/runs/34208979112/job/102005172489) | 镜像构建成功，但 `docker_integration_test.go:60` 报告 `fork/exec /tmp/go-build…/isolated.test: permission denied`：临时挂载的默认 `noexec` 阻止 Go 测试二进制执行。后续修复采用专用 `/aegis-tmp` 可执行临时挂载及 `GOTMPDIR`，普通 `/tmp` 保持 `noexec`；真实 Linux 重跑已在上列 CI 34212218038 通过。 |
| [CI / Offline executable pipeline health](https://github.com/Molly166/AegisCodeAgent/actions/runs/34208979112/job/102005172508) | 离线实代码执行健康检查成功；未调用模型，不能据此推断生产 Review 或模型准确率通过。 |

两阶段迁移替代 PR #12 的一次性升级路径，保留首轮失败供追踪。阶段 1 已完成合入，CI/容器修复已获线上验证；阶段 2 尚未启用，因此仍不能声称原四 Job Review 链路的升级与在线交付已经完成。

## 本轮修复的本地回归

2026-09-08，当前 Go Test 误报修复分支，Go 1.26.6 / macOS arm64，重新执行并通过：

- `go test -race ./...`：14 个包通过；本次未重新统计覆盖率。
- Node 交付、迁移与脚本检查：26/26 通过；覆盖旧 Base、缺少 Publisher、阻断 HTML、过期提交和伪造评论标记。
- Actionlint 1.7.12 + ShellCheck 0.11.0：全部实际 Workflow、暂存 v1 Workflow 和 `scripts/*.sh` 通过；缺少 ShellCheck 会明确失败。
- gofmt、`go vet`、Staticcheck 0.7.0、Gosec 2.28.0 高严重度扫描、`git diff --check`：通过。
- Go Test 专项回归：30 个协议案例、16 个源码定位边界案例、5 个真实 Go 进程案例，以及 2 个空/无 JSON 输出案例通过；跳过/通过不会仅凭日志内容生成 P1，失败、构建失败及不完整输出分别处理。
- 本轮 Golden Replay：50/50 通过。实代码离线 Eval：12/12 执行完整、2/8 缺陷检出、6/12 标签判定符合、干净样本误阻断 0/4、0 次模型 API 调用。结果与此前基线一致；这不是模型准确率验收，也未显示本次解析修复提高了语义漏洞检出率。
- Docker 参数和边界单测通过；真实 Docker 在本机跳过。阶段 1 的真实 Linux Docker 集成测试已在线成功，本轮修复提交仍需由自己的 CI 再次验证。

此前阶段 1 还补充了可信源码协议预检和不依赖 Base 脚本的失败反馈；执行失败时，HTML 与摘要均明确置顶阻断说明，不再仅修改退出码。该交付机制的线上启用仍受阶段 2 约束。

## 已实现范围

- DeepSeek、OrcaRouter 和通用 OpenAI-compatible Provider；显式模型能力、超时/重试预算、凭据隔离及请求元数据。
- v1 可复用 GitHub Workflow 的实现与待启用配置：可信评审器、精确 Base/Head、Docker 分析、独立 Publisher 和最终门禁；在线启用受上述迁移阶段约束。
- P0–P3 统一展示；未确认假设不再消失，P2/P3 不会仅因自身等级阻断；模型不能降低独立确定性证据的风险等级。
- 50-case Golden Replay 和独立的 12-case 实代码 Eval；重复运行、Verifier 消融、带分母的指标和真实证据保留。
- 自包含 HTML、PR 评论、行标注、手动确认的公开 HTTPS 托管工作流，以及六平台发布候选构建。
- 四语言 README、Provider/接入/托管/安全与贡献说明。

v1 最低构建版本为 **Go 1.24**，使用 `os.Root` 限制模型工具实际打开文件时的仓库边界。以下历史本地测试使用 **Go 1.26.6 / macOS arm64**，与 CI 固定的 Go 版本一致。

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

以下基线已在当前 Go Test 解析器修复后重跑确认，结果与首轮候选及阶段 1 记录一致。配置：`provider=none`、`analyzers=default`（go test/go vet）、Verifier 开启、每个样本运行一次。12 个合成 Git PR：8 Bug、4 Clean。

| 指标 | 观测结果 |
| --- | --- |
| 执行完整 | 12/12 |
| 检出已标注缺陷 | 2/8，Recall 25% |
| 命中的已发布 Finding | 2/2；样本极小，不能外推准确率 |
| 门禁符合标签 | 6/12 |
| 干净样本误阻断 | 0/4 |
| API 调用 / Token | 未启用模型 / 0 |

这些漏检被保留，没有通过修改标签或使用固定报告替代实际执行来隐藏。该基线并非四分析器全开的结果，也不包含 DeepSeek/OrcaRouter 实际调用。Golden 回放的 100% 不能与这里的检出率混用。

首轮本地生成的证据位于被 Git 忽略的 `artifacts/local-v1-validation/`：`golden.json/html`、`live.json/html`、`coverage.out`、`gosec.json`；当前修复的重跑证据单独保存在本地临时验收目录，未覆盖历史结果。Live JSON 记录 Corpus/Binary SHA256、实际 Go 版本、精确 Base/Head 和完整评审结果；本机路径不属于可移植发布包。

## 尚未完成的外部验收

1. **真实模型 Eval：** 尚未完成固定语料上的真实模型对比验收。需要用明确可访问的固定模型，在相同语料、预算、分析器和门禁配置下进行多次 DeepSeek/OrcaRouter 实测与 Verifier 消融，保留超时/限流/漏检结果。在线 PR 中发生过模型 Review，不等于已完成这组 Eval。
2. **当前修复与容器回归：** 阶段 1 的真实 Linux Docker 集成测试已成功，但当前 Go Test 误报修复仍须通过自己的 GitHub CI。现有容器边界测试通过也不等于证明所有隔离攻击场景均安全。
3. **GitHub v1 在线链路：** 仍需在阶段 2 启用后验证外部仓库复用、同仓库 PR、Fork 无 Key 降级、P0/P1 阻断、P2/P3 放行、P0 Needs Review、失败报告保留和旧 Head 不覆盖新评论。
4. **阶段 2 激活：** 阶段 1 已合入可信 Base；当前解析器修复验收合入后，再经独立 PR 启用新版工作流。不得自动使用不可信 Head 评审器绕过，也不能把阶段 1 旧宿主执行链路称为新版沙箱已生效。
5. **公开报告与发布包：** 尚未部署 Pages、创建标签或发布 Release。Linux GNU tar/zip 归档流程还需线上运行。公开报告必须确认无敏感内容，且会替换当前 Pages 站点。

因此，当前定位是**阶段 1 已合入、基础 CI 和真实 Docker 已获线上验证，Go Test 误报修复正在验收、v1 工作流尚待阶段 2 激活的发布候选**，不是“已证明生产准确率的最终商用版”。完成上述验收后再发布不可变版本，并让消费者通过 PR 固定该版本 SHA。

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
