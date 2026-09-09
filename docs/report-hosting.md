# 不下载 HTML，直接通过 HTTPS 查看

默认评审仍上传 HTML/JSON Artifact。Artifact 下载地址不是可直接浏览的 HTML 网页。公开仓库可另外启用 **Publish public HTML reports**：Aegis Review 完成后，独立工作流归档证据、部署 GitHub Pages，再把 PR 同一条 Bot 评论的顶部更新为 **「🌐 查看完整网页报告」**。

**公开托管默认关闭，代码存在不代表站点已经部署。** 自动发布需要实现合入默认分支、Pages 选择 GitHub Actions，并设置仓库变量 `AEGIS_PUBLIC_REPORTS=true` 才会启用；手动补发则需当次明确授权。HTTPS 地址以成功部署后返回的链接为准。这项发布功能不修改当前实际 Review 工作流，也不要求先激活 v1：旧版和 v1 的报告生产 Workflow 都必须匹配已审核、固定的 SHA256，v1 还必须验证 publication manifest。仅有一个格式正确的 manifest 不能证明来源可信。

当前只接受本仓库直接运行的 `.github/workflows/aegis-review.yml` PR Workflow。未知版本、reusable 或嵌套调用链尚未纳入公开证据的信任链，会返回失败诊断，而非宣称兼容。将来更新 Workflow 摘要白名单必须通过默认分支的独立审核，不能由 PR 输入或 Artifact 自行声明。

## 一次性启用自动发布

1. **先确认公开范围。** 仅支持 public 仓库；报告和归档 JSON 可能包含代码片段、PR 描述、漏洞细节及误提交的敏感信息。启用代表同意后续符合条件的评审结果公开，不是逐次人工检查。公开源码不等于所有诊断都适合再次公开展示。
2. 审核并将本次发布工作流、可信脚本和渲染器合入默认分支（本仓库为 `master`）。仅推送开发分支不会启用这条自动发布链路。
3. 在 **Settings → Pages → Build and deployment → Source** 选择 **GitHub Actions**。该工作流使用此仓库的**整个 Pages 站点**，不能与已有文档站独立共存；已有站点时不要直接启用，应先设计独立报告仓库或统一部署方案。本实现不会自动为你创建外部报告仓库。
4. 检查 `github-pages` Environment，只允许默认分支部署。是否设置 required reviewers 由维护者选择：设置审核者后，每次部署可能等待批准；不设置才能在完成一次性授权后无人值守发布。
5. 在 **Settings → Secrets and variables → Actions → Variables** 新建仓库变量 `AEGIS_PUBLIC_REPORTS`，值必须为 `true`。这是公开授权开关，不是 Secret，不需要额外的模型 API Key。
6. 创建或更新一个 PR。等待 **Aegis Code Review** 完成，再等待独立的 **Publish public HTML reports** 工作流完成。部署成功后，打开 PR 顶部网页报告链接，无需下载 HTML。

自动入口监听 `workflow_run` 的 `completed` 事件，并同时处理评审成功与失败；不会再次运行模型 Review。浏览器不会被强制从 GitHub 跳转，也没有本次范围之外的实时等待页。取消或改掉 `AEGIS_PUBLIC_REPORTS=true` 会停止后续自动发布，**不会删除已经公开的站点、归档或 Git 历史**。

## 手动补发一次报告

进入 **Actions → Publish public HTML reports → Run workflow**，选择默认分支：

- `run-id`：必填，已完成的本仓库 Aegis Review run ID。
- `run-attempt`：必填，默认 `1`；重新运行过的工作流需填写要发布的实际 attempt。
- `pr-number`：可选，仅用于核对 GitHub 已确认的 PR 关联，不能用它伪造关联。
- `confirm-public`：勾选，表示本次明确同意公开发布。手动补发不要求启用 `AEGIS_PUBLIC_REPORTS` 自动开关。

过期 Artifact、来源身份不一致或已经变化的运行 attempt 会被拒绝或呈现明确的失败诊断，不会伪造一次成功评审。已关闭 PR 的历史可保留，但不会再修改其评论；旧 Head 或旧 attempt 不会替换新评审的入口。

手动补发仍需要 GitHub API 返回该 run 的明确 PR 关联。若合并后的旧 run 已失去这项关联，即使填写 `pr-number` 也会被拒绝，不能用当前 PR 的提交反推历史身份。自动入口在排队期间遇到 PR 合并时，只能使用可信 `workflow_run` 完成事件里保留的关联；手动输入没有这个替代来源。

## 历史链接如何保持稳定

发布后的每份报告使用独立路径：

```text
<GitHub Pages base URL>/reports/pr-<number>/<full-head-sha>/<run-id>-<attempt>/index.html
```

上面是路径格式，不是已经上线的地址。站点首页列出历史报告，每页绑定具体 PR、Base/Head、run/attempt 和原始结论；历史通过不代表 PR 当前提交已经通过。

经校验的 JSON 证据及结论保存在专用 `aegis-report-history` 分支，由仓库身份 marker 识别；不归档 Artifact 中的 HTML、JavaScript 或可执行文件。写入通过 Git Database API 追加提交，并以当前分支 tip 为父提交、`force: false` 更新。并发冲突会重新读取后重试；相同身份但内容不同的记录不会覆盖旧证据，也不会接管一个不属于此功能的同名分支。

每次取得部署锁后重新读取完整归档，用默认分支的可信渲染器生成全部历史页面，再部署整个站点。因此后续部署不会只保留最新报告；已归档但因 GitHub 替换 pending 部署任务而暂未展示的记录，可随下次部署一起补齐。评论回填也遍历本次已部署的记录，为各 PR 选择最新 run/attempt，不仅处理触发部署的单个 PR。

当前容量限制为 **最多 200 条记录、归档总量 64 MiB、单条记录 17 MiB**；全站渲染后的 HTML 另有 **512 MiB** 上限。归档达到上限时停止追加，渲染超限时停止准备部署，并明确失败，**不自动删除历史**。扩容或迁移需维护者另行审核；稳定路径不代表永久可用保证，站点关闭、分支删除或域名变化仍会影响访问。

## 权限、来源与失败处理

| 阶段 | 权限与行为 |
| --- | --- |
| Prepare | 只读查询 GitHub run、attempt、PR 关联和 Artifact；下载有限大小的 JSON 输入，不检出或执行 PR 代码。旧版与 v1 均须匹配已审核的 Workflow SHA256，v1 还须验证 publication manifest；未知或 reusable/嵌套拓扑仅能产生诊断。 |
| Archive | 仅此阶段取得 `contents: write`，向专用历史分支追加经校验的记录；不运行下载的 HTML/JS。 |
| Deploy | 使用 Pages 写权限及 OIDC，由可信代码全量重建并部署站点；不持有模型 Key，也不重新评审。 |
| Feedback | 独立 PR 评论写权限；仅在成功部署后更新 `github-actions[bot]` 自有评论，部署 URL 必须匹配 GitHub Pages API 返回的可信 HTTPS 地址。 |

评论分页前后及正式写入前会核对 PR 是否仍打开、Base/Head 是否相同，并检查同 Head 是否已有更新的运行或重跑（包括 pending），避免旧结果覆盖新入口。GitHub 评论 API 没有跨 PR 状态与评论写入的原子 compare-and-swap，检查只能尽量缩小竞争窗口，不能承诺不存在任何并发竞争。

来源可确认但证据缺失等情形，会尽可能生成置顶 **「评审未完成」** 的诊断页；来源无法验证则拒绝发布。**网页发布不能把失败结果变成通过，也不创建或修改 Review 的合并门禁。** Pages 构建或部署失败时，独立发布工作流会失败并给出提示，原有 Artifact/Checks 及下载评论保留，不额外覆盖成一个尚不存在的网页链接。只有成功部署后才回填网页地址。

## 私有仓库

自带工作流明确拒绝 private/internal 仓库，不能通过变量或手动确认开关绕过。继续使用遵守仓库权限的 GitHub Artifact；如需要浏览器直接打开，应部署带 GitHub/企业 SSO 的报告服务或短期有效的私有对象存储签名链接。那需要单独配置身份验证、存储和访问策略，不属于本次公开 Pages 方案。

不要把 GitHub raw 下载 URL、第三方 HTML 预览代理或“难猜的公共 URL”当作私有报告访问控制。

参考：[使用自定义 Workflow 部署 Pages](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages)、[Pages 访问控制](https://docs.github.com/en/pages/getting-started-with-github-pages/changing-the-visibility-of-your-github-pages-site)。
