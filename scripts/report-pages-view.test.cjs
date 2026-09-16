'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { renderIndex, renderDiagnostic, decorateReport } = require('./report-pages-view.cjs');
const { recordPath, validateRecord, escapeHTML } = require('./report-pages-common.cjs');

const base = 'a'.repeat(40), head = 'b'.repeat(40);
const policy = { fail_on: 'p1', fail_on_needs_review: 'p0', fail_on_incomplete: true, require_agent: false };
const approvedLogo = fs.readFileSync(path.join(__dirname, '../docs/assets/aegis-pr-gate-harmony.png'));

function assertTrustedBrand(html) {
  const images = [...html.matchAll(/<img\b[^>]*>/gi)];
  assert.equal(images.length, 1, 'only the trusted brand image may be emitted');
  const image = images[0][0];
  const encoded = image.match(/\bsrc="data:image\/png;base64,([A-Za-z0-9+/=]+)"/)?.[1];
  assert.ok(encoded, 'brand must be an inline PNG, never an external image');
  assert.deepEqual(Buffer.from(encoded, 'base64'), approvedLogo);
  assert.match(image, /\balt=""/);
  assert.match(image, /\bwidth="48" height="48"/);
  assert.doesNotMatch(html, /<svg\b|M16 1\.8 29 6\.7|m10\.1 18\.4 3\.8 3\.8/);
  assert.match(html, /\.ap-brand-mark img\s*\{[^}]*width:\s*48px;[^}]*height:\s*48px;[^}]*object-fit:\s*contain/);
  assert.match(html, /\.ap-brand-mark img\s*\{[^}]*width:\s*44px;[^}]*height:\s*44px/);
  assert.ok(html.includes("img-src data:"));
}

function report(overrides = {}) {
  return {
    schema_version: 'v6', generated_at: '2026-09-09T01:00:00Z',
    comparison: { repository: 'owner/target', base: 'main', head: 'feature/review', base_commit: base, head_commit: head },
    analysis: { status: 'complete' },
    context: { status: 'complete', change_intent: { title: 'Tighten request validation', description: 'Validate request fields before execution.' } },
    agent: { status: 'skipped', candidates: [] },
    verification: { status: 'complete', summary: { candidates: 0, verified: 0, rejected: 0, needs_review: 0, inconclusive: 0, promoted: 0 }, candidates: [] },
    summary: { changed_files: 1, additions: 4, deletions: 2, findings: 0, critical: 0, high: 0, medium: 0, low: 0, info: 0 },
    files: [{ new_path: 'internal/request/validate.go', status: 'modified', stats: { additions: 4, deletions: 2 } }],
    findings: [], ...overrides,
  };
}

function record(overrides = {}) {
  const result = {
    schema_version: 1, repository: 'owner/target', pr_number: 7, base, head,
    run_id: 100, run_attempt: 1, created_at: '2026-09-09T01:00:00Z',
    conclusion: 'success', protocol: 'v1', status: 'passed', diagnostic: '', summary: '',
    policy: { ...policy }, report: report(), ...overrides,
  };
  result.run_url = `https://github.com/${result.repository}/actions/runs/${result.run_id}/attempts/${result.run_attempt}`;
  result.artifact_url = Object.hasOwn(overrides, 'artifact_url') ? overrides.artifact_url :
    `https://github.com/${result.repository}/actions/runs/${result.run_id}/artifacts/55`;
  result.publication = Object.hasOwn(overrides, 'publication') ? overrides.publication :
    { schema_version: 1, base: result.base, head: result.head, report_valid: true,
      exit_code: result.conclusion === 'success' ? 0 : 1, ...result.policy };
  validateRecord(result);
  return result;
}

function diagnosticRecord(overrides = {}) {
  return record({ report: null, publication: null, protocol: 'diagnostic', status: 'incomplete',
    conclusion: 'failure', diagnostic: '没有可验证的报告证据。', artifact_url: '', ...overrides });
}

function textContent(html) {
  return html.replace(/<style\b[^>]*>[\s\S]*?<\/style>/gi, '').replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim();
}

function priorityCount(html, priority) {
  const text = textContent(html);
  const match = text.match(new RegExp(`\\b${priority}\\s+(\\d+|—)(?=\\s|$)`));
  assert.ok(match, `missing visible ${priority} count`);
  return match[1];
}

test('archive and diagnostic pages embed the exact approved PNG with an unchanged warning icon', () => {
  assert.deepEqual(fs.readFileSync(path.join(__dirname, '../internal/report/aegis-logo.png')), approvedLogo);
  for (const html of [renderIndex([record()]), renderIndex([]), renderDiagnostic(diagnosticRecord())]) {
    assertTrustedBrand(html);
    assert.doesNotMatch(html, /<script\b|\b(?:src|srcset)\s*=\s*["'](?:https?:)?\/\//i);
  }
  assert.match(renderDiagnostic(diagnosticRecord()), /<div class="ap-diagnostic-icon" aria-hidden="true">!<\/div>/);
});

test('brand and stylesheet paths resolve from the trusted module rather than the process cwd', () => {
  const renderer = path.join(__dirname, 'report-pages-view.cjs');
  const result = spawnSync(process.execPath, ['-e', `process.stdout.write(require(${JSON.stringify(renderer)}).renderIndex([]))`],
    { cwd: os.tmpdir(), encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  assertTrustedBrand(result.stdout);
});

test('archive overview uses the recorded change intent and preserves the input evidence', () => {
  const input = record();
  const original = structuredClone(input);
  const html = renderIndex([input]);
  assert.ok(html.includes('Tighten request validation'));
  assert.ok(html.includes('internal/request/validate.go'));
  assert.ok(html.includes(`href="${recordPath(input)}/index.html"`));
  assert.deepEqual(input, original);
});

test('missing intent titles fall back to changed and deleted file paths', () => {
  for (const file of [{ new_path: 'internal/cache/fresh.go' }, { old_path: 'internal/cache/removed.go', status: 'deleted' }]) {
    const input = record({ report: report({ context: { status: 'complete', change_intent: {} }, files: [file] }) });
    const html = renderIndex([input]);
    assert.ok(html.includes(file.new_path || file.old_path));
    assert.equal(html.includes('undefined'), false);
    assert.equal(html.includes('[object Object]'), false);
    assert.equal(html.includes('Tighten request validation'), false);
  }
});

test('overview escapes untrusted change titles and file names as text', () => {
  const title = '<script>alert("title")</script> & forged';
  const filename = 'src/<img src=x onerror=alert(1)>.go';
  const html = renderIndex([record({ report: report({
    context: { status: 'complete', change_intent: { title } }, files: [{ new_path: filename }],
  }) })]);
  assert.ok(html.includes(escapeHTML(title)));
  assert.ok(html.includes(escapeHTML(filename)));
  assert.equal(html.includes('<script>'), false);
  assert.equal(html.includes('<img src=x'), false);
});

test('priority counts include only formal findings, not verified or promoted candidate totals', () => {
  const findings = ['critical', 'high', 'high', 'medium', 'medium', 'medium', 'low', 'low', 'low', 'low']
    .map((severity, index) => ({ id: `F-${index}`, title: `Formal finding ${index}`, severity,
      source: 'deterministic-fixture', location: { path: 'internal/request/validate.go', start_line: index + 1 } }));
  const input = record({ status: 'blocked', conclusion: 'failure', report: report({ findings,
    summary: { changed_files: 1, additions: 4, deletions: 2, findings: 10, critical: 1, high: 2, medium: 3, low: 4, info: 0 },
    verification: { status: 'complete', candidates: [],
      summary: { candidates: 150, verified: 90, rejected: 5, needs_review: 3, inconclusive: 2, promoted: 50 } },
  }) });
  const original = structuredClone(input);
  const html = renderIndex([input]);
  for (const [priority, count] of [['P0', '1'], ['P1', '2'], ['P2', '3'], ['P3', '4']]) {
    assert.equal(priorityCount(html, priority), count);
  }
  assert.deepEqual(input, original);
});

test('informational formal findings map to P3 with low severity findings', () => {
  const findings = ['low', 'info', 'info'].map((severity, index) => ({
    id: `INFO-${index}`, title: `Recorded ${severity} finding`, severity, source: 'deterministic-fixture',
    location: { path: 'internal/request/validate.go', start_line: index + 1 },
  }));
  const html = renderIndex([record({ report: report({ findings,
    summary: { changed_files: 1, additions: 4, deletions: 2, findings: 3, critical: 0, high: 0, medium: 0, low: 1, info: 2 },
  }) })]);
  for (const priority of ['P0', 'P1', 'P2']) assert.equal(priorityCount(html, priority), '0');
  assert.equal(priorityCount(html, 'P3'), '3');
});

test('one report directory cannot mix archive records from different repositories', () => {
  const first = record();
  const second = record({ repository: 'another/target', run_id: 101, pr_number: 8 });
  const inputs = [first, second];
  const original = structuredClone(inputs);
  assert.throws(() => renderIndex(inputs), /one repository/);
  assert.deepEqual(inputs, original);
});

test('archive keeps exact report identity links and orders runs then attempts descending', () => {
  const inputs = [record({ run_id: 100, run_attempt: 1 }), record({ run_id: 101, pr_number: 8 }),
    record({ run_id: 100, run_attempt: 2 })];
  const html = renderIndex(inputs);
  const links = inputs.map(input => `href="${recordPath(input)}/index.html"`);
  links.forEach(link => assert.ok(html.includes(link)));
  assert.ok(html.indexOf(links[1]) < html.indexOf(links[2]));
  assert.ok(html.indexOf(links[2]) < html.indexOf(links[0]));
  assert.deepEqual(inputs.map(input => input.run_attempt), [1, 1, 2], 'sorting must not reorder the archive input');
});

test('CSS-only archive filters are mutually exclusive labelled radios without scripts or external assets', () => {
  const html = renderIndex([record()]);
  for (const id of ['filter-all', 'filter-attention', 'filter-passed']) {
    const input = html.match(new RegExp(`<input\\b[^>]*\\bid=["']${id}["'][^>]*>`, 'i'))?.[0];
    assert.ok(input, `missing ${id}`);
    assert.match(input, /\btype=["']radio["']/i);
    assert.match(input, /\bname=["']report-filter["']/i);
    assert.match(html, new RegExp(`<label\\b[^>]*\\bfor=["']${id}["']`, 'i'));
    assert.ok(html.includes(`#${id}:checked`), `${id} must control a CSS state`);
  }
  const allInput = html.match(/<input\b[^>]*\bid=["']filter-all["'][^>]*>/i)?.[0];
  assert.match(allInput, /\bchecked(?:\s|=|>)/i);
  assert.doesNotMatch(html, /<script\b|\bon(?:click|change|load|error)\s*=|<link\b[^>]*href\s*=|@import\b/i);
  assert.doesNotMatch(html, /\b(?:src|srcset)\s*=\s*["'](?:https?:)?\/\//i);
  assert.doesNotMatch(html, /url\(\s*["']?(?:https?:)?\/\//i);
  assert.match(html, /Content-Security-Policy/);
  assert.ok(html.includes("default-src 'none'"));
});

test('no report evidence stays incomplete with unavailable metrics instead of a clean zero', () => {
  const input = diagnosticRecord();
  const html = renderIndex([input]);
  const text = textContent(html);
  assert.ok(text.includes('评审未完成'));
  assert.ok(text.includes('—'), 'missing evidence requires an unavailable metric marker');
  for (const priority of ['P0', 'P1', 'P2', 'P3']) assert.equal(priorityCount(html, priority), '—');
  assert.doesNotMatch(text, /(?:0\s*(?:项)?\s*(?:已确认|正式)?(?:发现|风险)|(?:发现|风险)\s*0)/);
  assert.equal(html.includes('identity-banner passed'), false);
  assert.deepEqual(input.report, null);
});

test('diagnostics escape failure evidence and retain the no-pass boundary', () => {
  const diagnostic = '<img src=x onerror=alert(1)> & <script>bad()</script>';
  const input = diagnosticRecord({ diagnostic });
  const html = renderDiagnostic(input);
  assert.ok(html.includes(escapeHTML(diagnostic)));
  assert.equal(html.includes('<img src=x'), false);
  assertTrustedBrand(html);
  assert.equal(html.includes('<script>'), false);
  assert.match(html, /评审未完成/);
  assert.match(html, /Content-Security-Policy/);
  assert.ok(html.includes("default-src 'none'"));
  assert.match(textContent(html), /门禁|通过结论|判定代码安全/);
});

test('decorating trusted report HTML adds provenance without replacing the evidence body', () => {
  const body = '<main id="evidence"><h1>Trusted evidence stays unchanged</h1><p>P1 finding evidence.</p></main>';
  const html = '<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="default-src \'none\'"><style>.evidence{color:red}</style></head><body>' + body + '</body></html>';
  const input = record({ status: 'blocked', conclusion: 'failure' });
  const original = structuredClone(input);
  const result = decorateReport(html, input);
  assert.ok(result.includes(body));
  assert.ok(result.includes('<style>.evidence{color:red}</style>'));
  assert.ok(result.includes('identity-banner blocked'));
  assert.match(result, /阻断合并/);
  assert.ok(result.includes('href="../../../../index.html"'));
  assert.ok(result.includes(`href="${input.run_url}"`));
  assert.ok(result.includes('此页只对应这次评审，不代表 PR 当前最新状态'));
  assert.ok(result.indexOf('identity-banner blocked') < result.indexOf(body));
  assert.deepEqual(input, original);
});

test('provenance is a compact disclosure with visible PR and verdict and complete snapshot details', () => {
  const input = record({ status: 'blocked', conclusion: 'failure', run_attempt: 3 });
  const html = decorateReport('<html><head></head><body><main>Evidence</main></body></html>', input);
  const disclosure = html.match(/<details class="ap-provenance">([\s\S]*?)<\/details>/)?.[1];
  assert.ok(disclosure, 'provenance must be a closed native disclosure');
  const summary = disclosure.match(/<summary>([\s\S]*?)<\/summary>/)?.[1];
  assert.match(textContent(summary), /PR #7/);
  assert.match(textContent(summary), /阻断合并/);
  assert.match(textContent(summary), /来源与运行信息/);
  assert.ok(disclosure.includes(input.repository));
  assert.ok(disclosure.includes(`title="${input.head}"`));
  assert.ok(disclosure.includes(input.head.slice(0, 12)));
  assert.match(disclosure, /Run 100 \/ attempt 3/);
  assert.match(disclosure, /原始工作流状态：failure/);
  assert.ok(disclosure.includes(`href="${input.run_url}"`));
  assert.ok(disclosure.includes(`href="${input.artifact_url}"`));
  assert.match(disclosure, /此页只对应这次评审，不代表 PR 当前最新状态/);
  assert.match(html, /\.ap-provenance > summary\s*\{[^}]*min-height:\s*46px/);
  assert.doesNotMatch(html, /<script\b|\bon(?:click|change|load|error)\s*=/i);
});

test('incomplete workflow and delivery warnings remain visible outside the collapsed disclosure', () => {
  for (const overrides of [
    { status: 'incomplete', conclusion: 'failure' },
    { status: 'incomplete', conclusion: 'success', publication: {
      schema_version: 1, base, head, report_valid: true, exit_code: 1, ...policy,
    } },
  ]) {
    const html = decorateReport('<html><head></head><body><main>Evidence</main></body></html>', record(overrides));
    const header = html.match(/<header class="identity-banner incomplete">([\s\S]*?)<\/header>/)?.[1];
    assert.ok(header);
    const disclosureEnd = header.indexOf('</details>');
    assert.ok(disclosureEnd > 0);
    const visibleWarning = header.slice(disclosureEnd + '</details>'.length);
    assert.match(visibleWarning, /<p class="ap-execution-warning" role="alert">/);
    assert.match(visibleWarning, /不构成评审通过结论/);
    assert.doesNotMatch(visibleWarning, /\bhidden\b|display:\s*none|<details\b/);
  }
  const passed = decorateReport('<html><head></head><body></body></html>', record());
  assert.doesNotMatch(passed, /<p class="ap-execution-warning"/);
});

test('trusted report placeholders use only validated PR identity and do not add invented defaults', () => {
  const template = '<html><head><meta http-equiv="Content-Security-Policy" content="default-src \'none\'"></head>' +
    '<body><nav><!--AEGIS_REPORT_ACTION--></nav><main><span><!--AEGIS_REPORT_PR--></span></main></body></html>';
  const input = record({ repository: 'molly-166/AegisCodeAgent', pr_number: 24 });
  const original = structuredClone(input);
  const html = decorateReport(template, input);
  assert.ok(html.includes('<a class="report-github-link" href="https://github.com/molly-166/AegisCodeAgent/pull/24">在 GitHub 查看'));
  assert.ok(html.includes('<main><span>PR #24</span></main>'));
  assert.doesNotMatch(html, /<!--AEGIS_REPORT_(?:ACTION|PR)-->/);
  assert.ok(html.includes('content="default-src \'none\'"'), 'decoration must preserve the original CSP');
  assert.doesNotMatch(html, /<script\b|target="_blank"|javascript:|<link\b/i);
  assert.deepEqual(input, original);

  const withoutMarkers = decorateReport('<html><head></head><body><main>Evidence</main></body></html>', record());
  assert.doesNotMatch(withoutMarkers, /class="report-github-link"/);
  for (const overrides of [
    { repository: 'evil.example/target?next=javascript:alert(1)' },
    { repository: 'owner/../evil' },
    { repository: 'owner/" onclick="alert(1)' },
    { pr_number: 0 }, { pr_number: '24' }, { pr_number: NaN },
  ]) {
    assert.throws(() => decorateReport(template, { ...input, ...overrides }), /Invalid public report identity or status/);
  }
});

test('archive empty state is explicit and does not invent evidence', () => {
  const html = renderIndex([]);
  assert.match(html, /暂无已发布报告/);
  assert.doesNotMatch(html, /href=["']reports\//);
  assert.doesNotMatch(textContent(html), /undefined|NaN|\[object Object\]/);
});

test('report directory is a three-column document with a readable type floor', () => {
  const html = renderIndex([record()]);
  assert.doesNotMatch(html, /<aside\b|class="ap-breadcrumb"|class="ap-archive-count"/);
  const header = html.match(/<thead>([\s\S]*?)<\/thead>/)[1];
  assert.equal((header.match(/<th\b/g) || []).length, 3);
  const row = html.match(/<tr class="ap-review-row[^>]*>([\s\S]*?)<\/tr>/)[1];
  assert.equal((row.match(/<td\b/g) || []).length, 3);
  assert.match(row, /Run 100 \/ attempt 1/);
  assert.match(row, /<time datetime="2026-09-09T01:00:00Z">/);
  const css = html.match(/<style>([\s\S]*?)<\/style>/)[1];
  const sizes = [...css.matchAll(/\bfont(?:-size)?:\s*(?:\d+\s+)?(\d+(?:\.\d+)?)px\b/g)];
  assert.ok(sizes.length > 40, 'check desktop, mobile and embedded provenance typography');
  for (const [, size] of sizes) assert.ok(Number(size) >= 14, `font floor regressed to ${size}px`);
});

test('business report typography retains strong headings and readable body weight', () => {
  const css = renderIndex([record()]).match(/<style>([\s\S]*?)<\/style>/)[1];
  assert.match(css, /\.aegis-pages\s*\{[^}]*font:\s*500 18px\//);
  for (const selector of ['ap-page-heading h1', 'ap-latest h2']) {
    assert.match(css, new RegExp(`\\.${selector}\\s*\\{[^}]*font-weight:\\s*800`));
  }
  assert.match(css, /\.ap-report-link\s*\{[^}]*font-weight:\s*700/);
  assert.match(css, /\.ap-priority b\s*\{[^}]*font-weight:\s*750/);
  assert.doesNotMatch(css, /-webkit-font-smoothing:\s*antialiased/);
});
