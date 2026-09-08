# 不下载 HTML，直接通过 HTTPS 查看

默认评审仍将 HTML 存入 GitHub Artifact。GitHub Artifact 下载地址不是静态网页地址，无法直接把 `review.html` 当作网页渲染。Aegis 提供一个**默认不运行、需手动批准的公开 GitHub Pages 发布工作流**，满足公开项目演示需求。

## 公开仓库的可选使用方式

1. 确认仓库为 public，并先阅读待发布 Artifact 中的报告。报告可能包含代码、漏洞细节及误提交的敏感内容，公开源码不等于所有诊断都适合再次公开展示。
2. 在 **Settings → Pages → Build and deployment** 选择 GitHub Actions。注意：该工作流会替换此仓库当前 Pages 站点。如果仓库已有文档站点，使用独立报告仓库或先整合部署方案。
3. 推荐在 `github-pages` Environment 配置审核者，并只允许默认分支部署。
4. 进入 **Actions → Publish public HTML report (opt-in)**，从默认分支手动运行，输入 Aegis review 的 run ID 和 PR number，勾选已检查并同意公开发布。
5. 工作流完成后，在 Deployment 或 Job Summary 中点击 HTTPS 地址。页面保留具体 Base/Head 与原始门禁结论。

工作流只接受本仓库 `.github/workflows/aegis-review.yml` 产生的、已完成的 PR review。它查询 GitHub API 确认仓库可见性、run/PR 关系、当前 Base/Head 和 Artifact 的名字/大小，然后只用 JSON 证据重新生成 HTML；不会复制或执行上传包里的 HTML/JS。发布阶段独立运行，只取得 Pages 部署所需权限。

这个入口目前托管**最新一次手动发布的报告**，下一次部署会替换页面，不提供永久历史链接。请保留 GitHub Artifact 作为对应 run 的原始证据。工作流不会自动在每次 PR push 后公开代码，也不会给旧 PR 评论插入一个会指向其他提交的动态链接。

## 私有仓库

自带工作流明确拒绝 private/internal 仓库，不能通过一个布尔开关绕过。继续使用遵守仓库权限的 GitHub Artifact；如需要浏览器直接打开，应部署带 GitHub/企业 SSO 的报告服务或短期有效的私有对象存储签名链接。那需要单独配置身份验证、存储和访问策略，不属于默认公开 Pages 方案。

不要把 GitHub raw 下载 URL、第三方 HTML 预览代理或“难猜的公共 URL”当作私有报告访问控制。

参考：[使用自定义 Workflow 部署 Pages](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages)、[Pages 访问控制](https://docs.github.com/en/pages/getting-started-with-github-pages/changing-the-visibility-of-your-github-pages-site)。
