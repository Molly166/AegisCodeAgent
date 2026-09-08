# v1 发布候选验收记录

记录日期：2026-09-08。工作分支：`feat/aegis-v1`，基于 `master` 提交 `75fd9868f468dea955fe490391761f058327068e`。这是本地工作树的验收记录，不是已经发布的 Release 或 GitHub 线上成功记录。

## 已实现范围

- DeepSeek、OrcaRouter 和通用 OpenAI-compatible Provider；显式模型能力、超时/重试预算、凭据隔离及请求元数据。
- 可跨仓库调用的 GitHub Workflow：可信评审器、精确 Base/Head、Docker 分析、独立 Publisher 和最终门禁。
- P0–P3 统一展示；未确认假设不再消失，P2/P3 不会仅因自身等级阻断；模型不能降低独立确定性证据的风险等级。
- 50-case Golden Replay 和独立的 12-case 实代码 Eval；重复运行、Verifier 消融、带分母的指标和真实证据保留。
- 自包含 HTML、PR 评论、行标注、手动确认的公开 HTTPS 托管工作流，以及六平台发布候选构建。
- 四语言 README、Provider/接入/托管/安全与贡献说明。

v1 最低构建版本为 **Go 1.24**，使用 `os.Root` 限制模型工具实际打开文件时的仓库边界。以下最终测试使用 **Go 1.26.6 / macOS arm64**，与 CI 固定的 Go 版本一致。

## 已实际执行的检查

| 检查 | 本地结果 |
| --- | --- |
| `go test -race ./...` | 全部 14 个包通过；语句覆盖率约 **76.2%**，不是缺陷检出率 |
| `go vet ./...` | 通过 |
| Staticcheck `v0.7.0` | 通过 |
| Gosec `v2.28.0 -severity high` | 通过：未报告高严重度诊断 |
| Gosec 全量扫描 | 仍有 **11 Medium、2 Low** 诊断，已保留；没有声称零安全告警 |
| Node 发布/反馈安全测试 | **15/15** 通过 |
| Actionlint `v1.7.12`、Bash 语法、gofmt、diff whitespace | 通过 |
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
2. **真实 Docker：** 本机没有 Docker。容器参数与边界单测已通过，真实容器攻击夹具已接入 Linux CI，但不能声称已在本机跑过容器隔离测试。
3. **GitHub 在线链路：** 尚未 push 或创建 PR。需验证外部仓库复用、同仓库 PR、Fork 无 Key 降级、P0/P1 阻断、P2/P3 放行、P0 Needs Review、失败报告保留和旧 Head 不覆盖新评论。
4. **首次升级：** 旧可信 Base 不支持新 Sandbox/Publisher 时，bootstrap PR 会 fail closed。维护者必须审查并显式完成迁移，不得自动使用不可信 Head 评审器绕过。
5. **公开报告与发布包：** 尚未部署 Pages、创建标签或发布 Release。Linux GNU tar/zip 归档流程还需线上运行。公开报告必须确认无敏感内容，且会替换当前 Pages 站点。

因此，当前定位是**工程功能已补齐、待外部验收的 v1 发布候选**，不是“已证明生产准确率的最终商用版”。完成上述验收后再发布不可变版本，并让消费者通过 PR 固定该版本 SHA。

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
