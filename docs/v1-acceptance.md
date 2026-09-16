# v1 工作流激活与验收

## 状态与身份

2026-09-16：**当前修订已切换为 v1 配置并通过本地验收；线上验收未完成。** 推送本身不代表 GitHub 检查或接入验收通过。不自动修改 Secrets、Rulesets、Pages、Release 或合并 PR；本地验收未调用真实模型 API。

| 项目 | 身份 / 状态 |
| --- | --- |
| 激活分支 | `feat/v1-workflow-activation-20260916` |
| 可信基线 | `9fbdb52c75bb7fba33b2b58ac8e0e2ec70c91bac`，PR #17 合入后的 `master` |
| 激活方式 | 原样迁移 `examples/aegis-review-v1-migration.yml` 至 `.github/workflows/aegis-review.yml`，移除暂存文件 |
| 工作流 SHA256 | `5c1ca01490ab638d8ffa3c2835a79542ea48d49618ac79c1d746838c825c1cce` |
| 可信协议 | `scripts/reviewer-contract.json` 的 `workflow_protocol=1` |
| 发布器身份兼容 | 上述 SHA256 与 `scripts/report-pages-source.cjs` 已认可的 v1 producer 一致，无需放宽来源校验 |
| 基线在线 CI | [35076687212](https://github.com/Molly166/AegisCodeAgent/actions/runs/35076687212)：3/3 Job 成功，含真实 Linux Docker 边界测试；仅证明基线，不证明本次激活成功 |

本次切换不重写评审器、不调整风险等级或放松门禁。四个 Review Job 是一条评审流水线的不同阶段，不是四次模型评审：

1. `Resolve trusted reviewer`：解析可信评审器 SHA 与精确 PR Base/Head，检查协议。
2. `Sandboxed analysis`：Docker 内执行全部静态分析器；可信宿主进程运行可选模型推理。
3. `Evidence report and PR feedback`：新 runner 从可信代码重建 Publisher，独立计算门禁并交付证据。
4. `Aegis merge gate`：无论前序成功、失败或跳过都执行；仅在依赖成功且 Publisher 判定通过时成功。

## 本地验收

本轮环境：Go 1.26.6 / macOS arm64，Node 25.8.2。mock API / 本地二进制集成不等于 GitHub 托管运行；容器参数测试不等于真实 Docker 边界测试。本机没有可用 Docker CLI，本次真实 Linux Docker 验收留待激活分支的 GitHub CI。

| 检查 | 本轮结果 |
| --- | --- |
| 正式入口、四 Job、可信 Base、不可变 Action SHA、报告 producer 哈希 | 已核对；回归测试覆盖正式入口，不再退回暂存入口 |
| 实际 Go Publisher 门禁矩阵 | `scripts/v1-acceptance.test.cjs`，16/16 通过，无跳过 |
| 全部 Node 测试（含实际 Go 二进制） | 220/220 通过，0 失败，0 跳过；包括上述 16 个门禁场景 |
| Go Race / Vet / 格式检查 | 14 个包 Race 通过；`go vet ./...`、gofmt、`git diff --check` 通过 |
| Actionlint + ShellCheck | Actionlint 1.7.12 + ShellCheck 0.11.0：所有正式 Workflow 和 shell 脚本通过 |
| Staticcheck / Gosec | Staticcheck 0.7.0 与 Gosec 2.28.0 高严重度检查通过；不宣称所有等级零告警 |
| Golden Replay | 50/50 通过；仅用于报告和策略回归 |
| 本地实代码离线执行 | 12/12 完整，0 incomplete，检出 2/8，门禁符合 6/12，干净样本误阻断 0/4；无模型、仅 go test/go vet，不是 Docker 或模型质量验收 |
| 激活版本真实 Linux Docker / GitHub API 写入 / 模型 API / 外部仓库 | 未执行；必须在线验收，不从本地结果推断 |

离线执行健康检查 `scripts/check-offline-eval.cjs` 通过，但仍明确报告上述漏检；未改标签或把漏检计作正确发现。新工作流不会凭配置切换改善既有检测能力。

本机验收产物位于临时目录 `/private/tmp/aegis-v1-activation.M28D0W/`：`aegis`、`golden.json`、`live.json`、`node-tests.log`；未加入 Git。这是本轮本机证据路径，不是发布包或永久存储。实测二进制 SHA256 为 `d260a6965855230ba52a828b2f44f2257ba9f17b11171dbd6df92eb703194150`。

### 门禁矩阵的预期

| 场景 | 预期 |
| --- | --- |
| 完整静态评审，无发现、无模型 Key | 默认通过；报告明确模型未运行 |
| 已确认 P0 或 P1 | 阻断，保留 Findings、HTML、摘要及发布策略 |
| 仅 P2 / P3 | 默认不阻断，问题仍展示 |
| P0 Needs Review | 默认阻断，但不冒充已确认 Finding |
| P1 Needs Review | 默认不因自身阻断；显式设置 P1 待确认阈值时阻断 |
| 可选 Agent partial | 显示覆盖降级；不伪造 P1，不单独否决完整确定性证据 |
| `require-agent=true` 且源码评审模型 skipped / partial | 阻断；纯文档明确无需模型的 skip 不应误阻断 |
| 必需分析阶段 incomplete、JSON 缺失或 Base/Head 不匹配 | 阻断并交付诊断，不宣称安全 |
| Prepare / Analyze / Publish 失败、取消或跳过，或门禁输出缺失 | 最终 Gate 不能成功 |
| Fork / Dependabot | 不注入模型 Key；评论写入受限不应导致证据消失 |
| 同一 PR 的 Head / Base 已变化 | 旧运行不能更新新版本的评论 |

其中门禁矩阵通过实际 `aegis github --gate-output` 与生产 Publisher 校验；身份解析、Key 选择及评论行为还由现有契约测试覆盖。不能把这些测试叫作真实线上 PR 验收。

### 本地复现

要求本地 Go 与 CI 一致（当前为 1.26.6），安装 ShellCheck；输出目录使用新建的临时目录。

```sh
set -eu
acceptance_dir=$(mktemp -d)
test -n "$acceptance_dir" && test -d "$acceptance_dir"
go build -trimpath -o "$acceptance_dir/aegis" ./cmd/aegis
AEGIS_TEST_BINARY="$acceptance_dir/aegis" node --test scripts/*.test.cjs
go test -race ./...
go vet ./...
test -z "$(gofmt -l .)"
bash scripts/check-workflows.sh
"$acceptance_dir/aegis" eval --corpus eval/cases --format json --output "$acceptance_dir/golden.json"
git diff --check
```

未设置 `AEGIS_TEST_BINARY` 时集成矩阵会显式 skip，不能将该运行记录为集成验收通过；CI 已先构建并传入该二进制。

## 推送后的线上验收

### A. 自评审激活 PR

维护者审核本地变更后推送新分支并创建 PR，Base 必须是包含前置实现的最新 `master`。记录 PR URL、Base/Head SHA、Workflow Run URL 与 attempt，不自动合并。

- [ ] 独立 CI 的 `verify`、Docker 集成、离线执行健康检查均通过。
- [ ] Aegis Review 出现上述四个 Job；日志里的 reviewer SHA 是 PR Base，目标 checkout 是精确 Head。
- [ ] 实际调用 `--analyzers all --sandbox docker`；依赖准备和 PR 子进程中没有模型 Key。
- [ ] 没有 Key 时明确静态降级；有获准 Key 时另行确认真实模型完成，不能由 Secret 存在推断。
- [ ] Prepare 成功后，策略通过或阻断、分析失败与 Publisher 故障均应进入证据/诊断交付路径，上传本 run attempt 的 `aegis-review-report-<attempt>`；正常 Publisher 生成的 HTML 使用当前品牌并显示真实门禁原因。
- [ ] Prepare 自身失败时 Publish 会跳过，此时可能没有 Artifact；必须确认最终 Gate 阻断并通过失败 Check/日志定位原因，不能把没有报告视为评审通过。
- [ ] 同仓库 PR 只更新 bot 自己的同一条评论，链接指向该运行的证据。
- [ ] 再推一次安全变更，确认旧 Head 不覆盖新评论；取消与重跑保留正确 attempt 身份。
- [ ] 若已启用公开报告，验证 v1 的 `publication.json` 被接收，HTTPS 页面及 PR 链接对应正确 Head；未启用则记 N/A，不自动开启。

### B. 策略与权限场景

在经授权的隔离测试分支/仓库验证上表场景，漏洞样例只进入测试 fixture，不向生产路径引入真实漏洞，也不合入缺陷分支。

- [ ] 至少一例已确认 P1 阻断、一例仅 P2/P3 放行和一例必需证据失败阻断。
- [ ] 验证 P0 Needs Review 与可选 Agent 降级的显示和默认门禁。
- [ ] 用真正的 Fork PR 验证无 Key 降级及 Artifact 交付；不得改用 `pull_request_target` 或暴露 Secrets 让测试通过。
- [ ] 按实际运行产生的名称，将 **Aegis merge gate** 设为分支保护/Ruleset 的必需检查，确认绕过权限符合维护者策略。

新增 Check 名与旧单 Job 的 Check 名不同。保留旧必需检查会使 PR 一直等待旧检查；完全移除门禁则会放开合并。应由维护者在成功验收后明确迁移必需检查，不能由脚本自动关闭保护。

### C. 外部仓库复用

选择经授权的独立 Go 单模块测试仓库，用 [安装示例](github-action.md) 引用已审核的完整 SHA，先 `provider: none` 再按预算测试固定模型。

- [ ] 调用方只添加 YAML，不复制 Aegis 源码；`actions: read`、`contents: read`、`pull-requests: write` 权限满足被调用工作流。
- [ ] reviewer SHA 来自被调用工作流的 `referenced_workflows`，不是调用方 SHA；目标 PR Base/Head 身份正确。
- [ ] 重复验证评论、行标注、HTML 和最终 Gate，并记录实际 Check 名。
- [ ] 外部 reusable/nested 调用的自动公开 Pages 发布当前不属于已支持来源；Artifact 交付须通过，不能承诺自动公网报告。
- [ ] 模型 API 成功只证明该配置的兼容性；真实检出质量需要独立 Eval，不能由一次成功运行推断。

## 证据登记与完成标准

| 对象 | PR / Run / SHA | 结果 |
| --- | --- | --- |
| 前置可信基线 CI | `9fbdb52` / [35076687212](https://github.com/Molly166/AegisCodeAgent/actions/runs/35076687212) | 已只读确认 3/3 Job 成功 |
| 本次激活 PR 的四 Job | 待推送与运行 | 待验收 |
| 策略与 Fork 场景 | 待授权测试 | 待验收 |
| 外部仓库可复用入口 | 待授权测试仓库 | 待验收 |
| Required check 配置 | 待维护者核对 | 未修改 |

配置已切换、本地测试通过、基线 CI 通过，均不能单独把上表待验收项标为完成。只有取得对应的运行与设置证据后，才能宣称 v1 在线启用与接入验收完成。
