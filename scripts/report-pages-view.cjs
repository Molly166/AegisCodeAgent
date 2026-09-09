'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { validateRecord, recordPath, escapeHTML: h } = require('./report-pages-common.cjs');

// Trusted, embedded assets. Published pages remain usable offline with no scripts,
// remote fonts or third-party resources; all evidence-derived text is escaped.
const styles = fs.readFileSync(path.join(__dirname, 'report-pages-view.css'), 'utf8');
const labels = { passed: '门禁通过', degraded: '通过，但需关注', blocked: '阻断合并', incomplete: '评审未完成' };
const descriptions = {
  passed: '未触发本次合并门禁；通过不代表没有缺陷。',
  degraded: '有未确认候选或评审降级项，请查看证据。',
  blocked: '本次评审触发合并门禁，请查看阻断原因。',
  incomplete: '没有完整证据，暂不能给出通过结论。',
};
const symbols = { passed: '✓', degraded: '!', blocked: '×', incomplete: '−' };

// Reuse Aegis's existing shield mark from internal/report/html.go.
function brand() {
  return '<span class="ap-brand-mark" aria-hidden="true"><svg viewBox="0 0 32 36"><path d="M16 1.8 29 6.7v9.9c0 8.2-5.2 14.6-13 17.6C8.2 31.2 3 24.8 3 16.6V6.7L16 1.8Z" fill="none" stroke="currentColor" stroke-width="2"/><path d="m10.1 18.4 3.8 3.8 8.5-9" fill="none" stroke="currentColor" stroke-width="2.2"/></svg></span><span class="ap-wordmark">Aegis</span>';
}

function doc(title, body) {
  return '<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">' +
    '<meta http-equiv="Content-Security-Policy" content="default-src \'none\'; style-src \'unsafe-inline\'; img-src data:; base-uri \'none\'; form-action \'none\'">' +
    `<title>${h(title)}</title><style>${styles}</style></head><body class="aegis-pages">${body}</body></html>`;
}

function badge(record) {
  return `<span class="ap-status ${record.status}"><span aria-hidden="true">${symbols[record.status]}</span>${labels[record.status]}</span>`;
}

function title(record) {
  const supplied = record.report?.context?.change_intent?.title;
  if (typeof supplied === 'string' && supplied.trim()) return supplied.trim();
  const files = record.report?.files;
  const file = Array.isArray(files) ? files[0]?.new_path || files[0]?.old_path : '';
  if (typeof file === 'string' && file) return `变更 ${file}${files.length > 1 ? ` 等 ${files.length} 个文件` : ''}`;
  return record.report === null ? '未取得有效评审证据' : '未提供变更标题';
}

function counts(record) {
  if (!Array.isArray(record.report?.findings)) return null;
  const result = [0, 0, 0, 0];
  // Match githubreport.PriorityForSeverity: low/info map to P3.
  const severity = { critical: 0, high: 1, medium: 2 };
  for (const finding of record.report.findings) {
    result[Object.hasOwn(severity, finding.severity) ? severity[finding.severity] : 3]++;
  }
  return result;
}

function priorities(record) {
  const values = counts(record);
  return `<div class="ap-priorities" aria-label="${values ? '正式 Findings 的 P0–P3 分布' : '无可验证证据，风险数量未知'}">` +
    [0, 1, 2, 3].map(level => `<span class="ap-priority p${level}${values?.[level] > 0 ? ' has-finding' : ''}"><small>P${level}</small><b>${values ? values[level] : '—'}</b></span>`).join('') + '</div>';
}

function time(record) {
  return `<time datetime="${h(record.created_at)}">${h(record.created_at.slice(0, 10))} · ${h(record.created_at.slice(11, 16))} UTC</time>`;
}

function row(record) {
  const link = recordPath(record) + '/index.html';
  const firstFile = record.report?.files?.[0];
  const filename = firstFile?.new_path || firstFile?.old_path;
  const scope = record.report?.context?.change_intent?.title && typeof filename === 'string' && filename
    ? `<span class="ap-change-file" title="${h(filename)}">${h(filename)}</span>` : '';
  return `<tr class="ap-review-row ${record.status}"><td class="ap-change"><a class="ap-report-link" href="${link}" aria-label="查看 PR ${record.pr_number} 运行 ${record.run_id} 的报告">` +
    `<span class="ap-pr-number">#${record.pr_number}</span><span class="ap-change-title">${h(title(record))}</span></a>` +
    scope + `<div class="ap-change-meta"><code>${record.head.slice(0, 12)}</code><span>Run ${record.run_id} / attempt ${record.run_attempt}</span>${time(record)}</div></td>` +
    `<td class="ap-verdict">${badge(record)}</td><td class="ap-findings">${priorities(record)}</td></tr>`;
}

function readingGuide() {
  return '<details class="ap-guide" id="reading-guide"><summary><span>如何读懂这份报告</span><span class="ap-expand" aria-hidden="true">+</span></summary>' +
    '<div class="ap-guide-content"><p><b>先看结论，再看证据。</b>P0–P3 只统计最终 Findings，未确认候选不混入；“—”表示没有可验证证据。具体阻断原因与门禁阈值以单份报告为准。</p>' +
    '<dl><div><dt>P0</dt><dd>最高优先级风险</dd></div><div><dt>P1</dt><dd>高优先级风险</dd></div><div><dt>P2</dt><dd>中优先级风险</dd></div><div><dt>P3</dt><dd>低优先级风险</dd></div></dl>' +
    '<p>历史结论仅对应当次 Base / Head 与运行，不代表 PR 当前状态。门禁通过不等于代码无缺陷，网页展示也不替代 GitHub 合并门禁。</p></div></details>';
}

function renderIndex(records) {
  if (!Array.isArray(records) || records.length > 200) throw new Error('Invalid report archive');
  records.forEach(validateRecord);
  const ordered = [...records].sort((a, b) => b.run_id - a.run_id || b.run_attempt - a.run_attempt);
  const repo = records[0]?.repository;
  if (records.some(record => record.repository !== repo)) throw new Error('Report directory must belong to one repository');
  const attention = records.filter(record => record.status !== 'passed').length;
  const passed = records.length - attention;
  const newest = ordered[0];
  const repositoryLink = repo ? `https://github.com/${repo}` : '';
  const spotlight = newest ? `<section class="ap-latest ${newest.status}" aria-label="最近一次已发布评审">` +
    '<div class="ap-latest-top">最近一次评审</div>' +
    '<div class="ap-latest-main">' +
    `<div class="ap-latest-copy"><h2>${labels[newest.status]}</h2><p>${descriptions[newest.status]}</p>` +
    `<div class="ap-latest-meta"><b>PR #${newest.pr_number}</b><code>${newest.head.slice(0, 12)}</code><span>Run ${newest.run_id} / attempt ${newest.run_attempt}</span></div></div>` +
    `<a class="ap-button" href="${recordPath(newest)}/index.html">查看完整报告 <span aria-hidden="true">↗</span></a></div></section>` : '';
  const listing = records.length ? `<section class="ap-review-browser" id="history" aria-label="历史评审报告">` +
    '<input class="ap-filter-input" type="radio" id="filter-all" name="report-filter" checked>' +
    '<input class="ap-filter-input" type="radio" id="filter-attention" name="report-filter">' +
    '<input class="ap-filter-input" type="radio" id="filter-passed" name="report-filter">' +
    `<div class="ap-list-heading"><div class="ap-filters" role="group" aria-label="按评审结论筛选">` +
    `<label for="filter-all">全部记录 <span>${records.length}</span></label><label for="filter-attention">需关注 <span>${attention}</span></label><label for="filter-passed">门禁通过 <span>${passed}</span></label></div>` +
    '<span class="ap-order">按运行倒序</span></div><div class="ap-table-wrap"><table class="ap-review-table"><caption class="ap-sr-only">按运行倒序排列的已发布报告；同一 PR 可以有多次评审。</caption>' +
    '<thead><tr><th scope="col">变更 / 提交</th><th scope="col">评审结论</th><th scope="col">风险分布</th></tr></thead>' +
    `<tbody>${ordered.map(row).join('')}</tbody></table>` +
    `<p class="ap-filter-empty ap-empty-attention"${attention ? ' hidden' : ''}>没有需关注的历史记录。门禁通过不代表代码没有缺陷。</p>` +
    `<p class="ap-filter-empty ap-empty-passed"${passed ? ' hidden' : ''}>暂无门禁通过的历史记录，请查看需关注的评审。</p></div>` +
    '<p class="ap-list-foot">P0–P3 为正式 Findings；— 表示证据不可用。</p></section>' :
    '<section class="ap-empty" id="history"><span class="ap-empty-mark" aria-hidden="true">↳</span><h2>暂无已发布报告</h2><p>完成一次 PR 评审并成功发布后，报告会出现在这里。</p></section>';
  return doc('Aegis · 评审报告', `<header class="ap-topbar" id="top"><a class="ap-brand" href="#top" aria-label="Aegis 评审报告首页">${brand()}</a>` +
    `${repositoryLink ? `<a class="ap-repo-link" href="${h(repositoryLink)}">${h(repo)} <span aria-hidden="true">↗</span></a>` : '<span class="ap-repo-link">公开报告</span>'}</header>` +
    '<main class="ap-content"><header class="ap-page-heading"><h1>代码评审报告</h1><p>每次变更的结论与证据。</p></header>' +
    spotlight + listing + readingGuide() +
    '<footer class="ap-footer"><span>AegisCodeAgent</span><span>公开归档 · 仅对应当次提交，不代表 PR 当前状态</span></footer></main>');
}

function renderDiagnostic(record) {
  validateRecord(record);
  return doc('Aegis · 评审未完成', '<main class="ap-diagnostic"><div class="ap-diagnostic-icon" aria-hidden="true">!</div>' +
    `<h1>还不能给出评审结论</h1><p class="ap-diagnostic-lead">本次运行未取得可验证的完整证据。<br>这不是一份“没有问题”的报告。</p>` +
    `<div class="ap-diagnostic-reason"><h2>需要检查的原因</h2><p>${h(record.diagnostic || '没有可验证的报告证据。')}</p></div>` +
    `<a class="ap-button" href="${h(record.run_url)}">查看工作流日志 <span aria-hidden="true">↗</span></a>` +
    '<p class="ap-diagnostic-foot">检查错误并重跑评审后，会生成新的独立报告。原有合并门禁不受网页展示影响。</p></main>');
}

function decorateReport(html, record) {
  validateRecord(record);
  const artifact = record.artifact_url ? `<a href="${h(record.artifact_url)}">原始证据 ↗</a>` : '';
  const executionWarning = record.status === 'incomplete' && record.report !== null &&
    (record.conclusion !== 'success' || record.publication?.exit_code === 1)
    ? '<p class="ap-execution-warning" role="alert">原始评审执行或交付未成功完成。下面的 Findings 与门禁统计仅描述已有证据，不构成评审通过结论。</p>' : '';
  const header = `<header class="identity-banner ${record.status}"><details class="ap-provenance"><summary>` +
    `<span class="ap-provenance-label">公开快照 <span aria-hidden="true">·</span> PR #${record.pr_number}</span>` +
    `<strong>${labels[record.status]}</strong><span class="ap-provenance-toggle">来源与运行信息 <span class="ap-provenance-chevron" aria-hidden="true">⌄</span></span></summary>` +
    '<div class="ap-provenance-content"><div class="ap-identity-main"><a class="ap-back" href="../../../../index.html">← 全部评审报告</a>' +
    `<span class="ap-identity-repo">${h(record.repository)}</span></div>` +
    `<div class="ap-identity-meta"><div><code title="${h(record.head)}">${record.head.slice(0, 12)}</code><span>Run ${record.run_id} / attempt ${record.run_attempt}</span><span>原始工作流状态：${h(record.conclusion)}</span></div>` +
    `<nav aria-label="报告入口"><a href="${h(record.run_url)}">查看工作流 ↗</a>${artifact}</nav></div>` +
    '<p class="ap-snapshot-note">此页只对应这次评审，不代表 PR 当前最新状态。</p></div></details>' + executionWarning + '</header>';
  // These markers are emitted by the trusted renderer, never derived from PR content.
  // Build external navigation exclusively from the already-validated archive identity.
  const pullURL = `https://github.com/${record.repository}/pull/${record.pr_number}`;
  const action = `<a class="report-github-link" href="${h(pullURL)}">在 GitHub 查看 <span aria-hidden="true">↗</span></a>`;
  return html.replace('<!--AEGIS_REPORT_ACTION-->', () => action)
    .replace('<!--AEGIS_REPORT_PR-->', () => `PR #${record.pr_number}`)
    .replace(/(<body\b[^>]*>)/i, '$1' + header).replace('</head>', `<style>${styles}</style></head>`);
}

module.exports = { renderIndex, renderDiagnostic, decorateReport };
