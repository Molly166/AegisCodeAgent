'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { renderRecord, renderIndex, buildSite, classify } = require('./report-pages-render.cjs');
const { recordPath, validateRecord } = require('./report-pages-common.cjs');

const base = 'a'.repeat(40), head = 'b'.repeat(40);
const policy = { fail_on: 'p1', fail_on_needs_review: 'p0', fail_on_incomplete: true, require_agent: false };
const cleanGate = { blocked: false, incomplete: false, degraded: false, needs_review: false };
const safeHTML = '<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="default-src \'none\'"></head><body><h1>Rendered evidence</h1></body></html>';

function golden(name = 'CLEAN-007-synchronized-map') {
  const report = JSON.parse(fs.readFileSync(path.join(__dirname, '../eval/cases', name, 'report.json'), 'utf8'));
  report.comparison.base_commit = base;
  report.comparison.head_commit = head;
  return report;
}

function record(overrides = {}) {
  const r = {
    schema_version: 1, repository: 'owner/target', pr_number: 7, base, head,
    run_id: 100, run_attempt: 1, created_at: '2026-09-09T01:00:00Z',
    conclusion: 'success', protocol: 'v1', status: 'incomplete', diagnostic: '', summary: '',
    policy: { ...policy }, report: golden(), ...overrides,
  };
  r.run_url = `https://github.com/${r.repository}/actions/runs/${r.run_id}/attempts/${r.run_attempt}`;
  r.artifact_url = `https://github.com/${r.repository}/actions/runs/${r.run_id}/artifacts/55`;
  r.publication = Object.hasOwn(overrides, 'publication') ? overrides.publication :
    { schema_version: 1, base: r.base, head: r.head, report_valid: true,
      exit_code: r.conclusion === 'success' ? 0 : 1, ...r.policy };
  return r;
}

function directoryFixture(t) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'aegis-pages-render-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  return directory;
}

function fakeRenderer(t, options = {}) {
  const root = directoryFixture(t);
  const binary = path.join(root, 'trusted-renderer');
  const capture = path.join(root, 'invocation.json');
  const source = `#!/usr/bin/env node
'use strict';
const fs = require('node:fs');
const args = process.argv.slice(2);
const get = flag => args[args.indexOf(flag) + 1];
const config = ${JSON.stringify(options)};
fs.writeFileSync(${JSON.stringify(capture)}, JSON.stringify({ args, env: process.env, cwd: process.cwd() }));
const gate = { schema_version: 1, base: get('--expected-base'), head: get('--expected-head'),
  blocked: false, incomplete: false, degraded: false, needs_review: false, ...config.gate };
if (!config.omit?.includes('gate')) fs.writeFileSync(get('--gate-output'), config.gateText ?? JSON.stringify(gate));
if (!config.omit?.includes('html')) fs.writeFileSync(get('--html-output'), config.html ?? ${JSON.stringify(safeHTML)});
if (!config.omit?.includes('summary')) fs.writeFileSync(get('--summary'), config.summary ?? 'Trusted rendered summary');
process.exit(config.exitCode ?? (gate.blocked ? 1 : 0));
`;
  fs.writeFileSync(binary, source, { mode: 0o700 });
  return { root, binary, capture, directory: path.join(root, 'rendered') };
}

function fileList(directory, prefix = '') {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const relative = prefix + entry.name;
    return entry.isDirectory() ? fileList(path.join(directory, entry.name), relative + '/') : [relative];
  }).sort();
}

test('P1 blocks and optional partial stages remain explicitly degraded', t => {
  for (const c of [
    { gate: { blocked: true }, conclusion: 'failure', expected: 'blocked', label: '阻断合并' },
    { gate: { degraded: true }, conclusion: 'success', expected: 'degraded', label: '通过，但需关注' },
    { gate: { needs_review: true }, conclusion: 'success', expected: 'degraded', label: '通过，但需关注' },
    { gate: cleanGate, conclusion: 'success', expected: 'passed', label: '门禁通过' },
  ]) {
    const fixture = fakeRenderer(t, { gate: c.gate });
    const input = record({ conclusion: c.conclusion });
    const original = structuredClone(input);
    const output = renderRecord(input, fixture);
    assert.equal(output.record.status, c.expected);
    assert.ok(output.html.includes(`<header class="identity-banner ${c.expected}">`));
    const summary = output.html.match(/<details class="ap-provenance"><summary>([\s\S]*?)<\/summary>/)?.[1];
    assert.ok(summary, 'the closed provenance disclosure must keep its truthful status visible');
    assert.ok(summary.includes(`<strong>${c.label}</strong>`));
    assert.ok(summary.includes(`PR #${input.pr_number}`));
    assert.doesNotMatch(summary, /\shidden(?:\s|=|>)|display:\s*none/);
    assert.ok(output.html.indexOf('identity-banner ' + c.expected) < output.html.indexOf('<h1>Rendered evidence'));
    assert.equal(output.record.summary, 'Trusted rendered summary');
    assert.deepEqual(input, original, 'rendering must not mutate the input archive object');
    validateRecord(output.record);
  }
});

test('original workflow failures and publication exits never become green', t => {
  for (const conclusion of ['failure', 'cancelled', 'timed_out', 'startup_failure']) {
    const fixture = fakeRenderer(t);
    const result = renderRecord(record({ conclusion }), fixture);
    assert.equal(result.record.status, 'incomplete');
    assert.match(result.record.summary, /^> ❌ \*\*Original review workflow did not pass/);
    assert.ok(result.html.includes('identity-banner incomplete'));
    assert.ok(result.html.includes(`原始工作流状态：${conclusion}`));
    assert.ok(result.html.includes('不构成评审通过结论'));
    assert.equal(result.html.includes('<strong>门禁通过'), false);
  }
  const fixture = fakeRenderer(t);
  const input = record();
  input.publication.exit_code = 1;
  assert.equal(renderRecord(input, fixture).record.status, 'incomplete');
});

test('missing evidence renders an escaped diagnostic without starting the renderer', t => {
  const directory = path.join(directoryFixture(t), 'diagnostic');
  const diagnostic = '<img src=x onerror=alert(1)> & <script>bad()</script>';
  const input = record({ report: null, publication: null, protocol: 'diagnostic', diagnostic,
    conclusion: 'failure', artifact_url: '' });
  input.artifact_url = '';
  const result = renderRecord(input, { directory, binary: '/nonexistent/never-execute' });
  assert.equal(result.record.status, 'incomplete');
  assert.equal(result.record.report, null);
  assert.ok(result.html.includes('评审未完成'));
  assert.ok(result.html.includes('&lt;img src=x onerror=alert(1)&gt;'));
  assert.ok(result.html.includes('&lt;script&gt;bad()&lt;/script&gt;'));
  assert.equal(result.html.includes('<img'), false);
  assert.equal(result.html.includes('<script>'), false);
  assert.equal(result.html.includes('下载原始证据'), false);
  assert.match(result.html, /Content-Security-Policy/);
  assert.ok(result.html.includes("default-src 'none'"));
});

test('invalid JSON, gate identity/types, exit mismatches and missing files become incomplete', t => {
  for (const options of [
    { gateText: 'not-json' }, { gateText: '{}' }, { gate: { schema_version: 2 } },
    { gate: { head: 'c'.repeat(40) } }, { gate: { base: 'c'.repeat(40) } },
    { gate: { blocked: 'false' } }, { gate: { incomplete: 'false' } },
    { gate: { blocked: true }, exitCode: 0 }, { exitCode: 2 },
    { omit: ['gate'] }, { omit: ['html'] }, { omit: ['summary'] },
    { html: '<html><body>missing CSP</body></html>' }, { html: 'Content-Security-Policy without document body' },
  ]) {
    const fixture = fakeRenderer(t, options);
    const result = renderRecord(record(), fixture);
    assert.equal(result.record.status, 'incomplete', JSON.stringify(options));
    assert.equal(result.record.report, null, JSON.stringify(options));
    assert.ok(result.record.diagnostic);
    assert.ok(result.html.includes('identity-banner incomplete'));
  }
});

test('historical gate drift or rendering failure fails rather than replacing archive conclusions', t => {
  for (const [status, conclusion, options] of [
    ['passed', 'success', { gate: { blocked: true } }],
    ['blocked', 'failure', { gate: cleanGate }],
    ['degraded', 'success', { gate: cleanGate }],
    ['passed', 'success', { gateText: '{invalid' }],
  ]) {
    const fixture = fakeRenderer(t, options);
    assert.throws(() => renderRecord(record({ status, conclusion }), { ...fixture, archived: true }), /Archived report could not be faithfully rendered/);
  }
  const fixture = fakeRenderer(t, { gate: { blocked: true } });
  assert.equal(renderRecord(record({ status: 'blocked', conclusion: 'failure' }), { ...fixture, archived: true }).record.status, 'blocked');
});

test('child renderer receives only its narrow environment, trusted command and literal policy arguments', t => {
  const forbidden = { GITHUB_TOKEN: 'github-token-canary', DEEPSEEK_API_KEY: 'provider-key-canary',
    NODE_OPTIONS: '--require /must-not-load-untrusted-script.cjs', GOFLAGS: '-toolexec=must-not-execute',
    GIT_CONFIG_COUNT: '99', GIT_CONFIG_KEY_0: 'core.sshCommand', AEGIS_API_KEY: 'api-key-canary' };
  const before = Object.fromEntries(Object.keys(forbidden).map(key => [key, process.env[key]]));
  t.after(() => {
    for (const [key, value] of Object.entries(before)) {
      if (value === undefined) delete process.env[key]; else process.env[key] = value;
    }
  });
  Object.assign(process.env, forbidden);
  const fixture = fakeRenderer(t);
  const input = record({ policy: { ...policy, fail_on: 'p0', require_agent: true }, run_attempt: 2 });
  const result = renderRecord(input, fixture);
  assert.equal(result.record.status, 'passed');
  const invocation = JSON.parse(fs.readFileSync(fixture.capture, 'utf8'));
  for (const key of Object.keys(forbidden)) assert.equal(Object.hasOwn(invocation.env, key), false, key);
  assert.equal(invocation.env.GOTOOLCHAIN, 'local');
  assert.equal(invocation.args[0], 'github');
  assert.equal(invocation.args.includes('review'), false);
  assert.equal(invocation.args.includes('--annotations=false'), true);
  assert.equal(invocation.args.includes('--require-agent=true'), true);
  assert.equal(invocation.args[invocation.args.indexOf('--fail-on') + 1], 'p0');
  assert.equal(invocation.args[invocation.args.indexOf('--artifact-name') + 1], 'aegis-review-report-2');
  assert.equal(invocation.cwd, fs.realpathSync(fixture.directory));
});

test('history build retains separate attempts and PRs while publishing only regenerated HTML', t => {
  const fixture = fakeRenderer(t);
  const records = [record({ status: 'passed' }), record({ status: 'passed', run_attempt: 2 }),
    record({ status: 'passed', pr_number: 8, run_id: 101 })];
  const site = path.join(fixture.root, 'site');
  buildSite(records, { binary: fixture.binary, directory: site, scratch: path.join(fixture.root, 'scratch') });
  assert.deepEqual(fileList(site), ['.nojekyll', 'index.html', ...records.map(r => recordPath(r) + '/index.html')].sort());
  const index = fs.readFileSync(path.join(site, 'index.html'), 'utf8');
  for (const r of records) {
    assert.ok(index.includes(`href="${recordPath(r)}/index.html"`));
    const html = fs.readFileSync(path.join(site, recordPath(r), 'index.html'), 'utf8');
    assert.ok(html.includes(`Run ${r.run_id} / attempt ${r.run_attempt}`));
    assert.ok(html.includes('此页只对应这次评审，不代表 PR 当前最新状态'));
    assert.ok(html.includes('href="../../../../index.html"'));
  }
  assert.ok(index.indexOf('101-1/index.html') < index.indexOf('100-2/index.html'));
  assert.ok(index.indexOf('100-2/index.html') < index.indexOf('100-1/index.html'));
});

test('duplicate or unsafe archive identities and excessive record counts are rejected', t => {
  const fixture = fakeRenderer(t);
  const r = record({ status: 'passed' });
  assert.throws(() => buildSite([r, r], { binary: fixture.binary, directory: path.join(fixture.root, 'duplicates'),
    scratch: path.join(fixture.root, 'duplicate-scratch') }), /Duplicate archive identity/);
  assert.throws(() => buildSite(Array(201).fill(r), { binary: fixture.binary,
    directory: path.join(fixture.root, 'oversized'), scratch: path.join(fixture.root, 'oversized-scratch') }), /Invalid report archive/);
  for (const change of [{ head: '../escape' }, { pr_number: 0 }, { run_attempt: '../1' }]) {
    assert.throws(() => recordPath({ ...r, ...change }), /path identity/);
  }
  assert.throws(() => renderIndex([{ ...r, repository: 'owner/<script>' }]), /identity/);
  assert.match(renderIndex([]), /暂无已发布报告/);
});

test('classification never turns incomplete evidence into clean when policy does not block', () => {
  assert.equal(classify(record(), { ...cleanGate, incomplete: true }), 'incomplete');
  assert.equal(classify(record(), { ...cleanGate, blocked: true, degraded: true }), 'blocked');
});

test('real trusted Go publisher keeps P1 blocking and escapes finding HTML', {
  skip: !process.env.AEGIS_TEST_BINARY && 'set AEGIS_TEST_BINARY to a freshly built trusted Aegis binary',
}, t => {
  const directory = path.join(directoryFixture(t), 'real-blocked');
  const report = golden('BUG-001-failing-regression');
  report.findings[0].title = '<img src=x onerror=alert(1)> P1 assertion';
  report.findings[0].evidence = '<script>must-not-execute()</script>';
  const result = renderRecord(record({ conclusion: 'failure', report }), {
    binary: path.resolve(process.env.AEGIS_TEST_BINARY), directory,
  });
  assert.equal(result.record.status, 'blocked', result.record.diagnostic);
  assert.equal(result.record.report.findings.length, 1);
  assert.ok(result.html.includes('identity-banner blocked'));
  assert.ok(result.html.includes('&lt;img src=x onerror=alert(1)&gt;'));
  assert.equal(result.html.includes('<img src=x onerror=alert(1)>'), false);
  assert.equal(result.html.includes('<script>must-not-execute()</script>'), false);
  const trustedHTML = fs.readFileSync(path.join(directory, 'report.html'), 'utf8');
  for (const marker of ['<!--AEGIS_REPORT_ACTION-->', '<!--AEGIS_REPORT_PR-->']) {
    assert.ok(trustedHTML.includes(marker), `html/template must retain the trusted ${marker} marker`);
    assert.equal(result.html.includes(marker), false, 'the public page must replace trusted markers');
  }
  assert.match(result.html, /<div class="masthead-action">\s*<a class="report-github-link" href="https:\/\/github\.com\/owner\/target\/pull\/7">在 GitHub 查看/);
  assert.match(result.html, /<div class="hero-meta">\s*PR #7/);
  const gate = JSON.parse(fs.readFileSync(path.join(directory, 'gate.json'), 'utf8'));
  assert.equal(gate.blocked, true);
  assert.equal(gate.base, base);
  assert.equal(gate.head, head);
});

test('real trusted Go publisher preserves optional Agent partial as degraded, not clean or blocked', {
  skip: !process.env.AEGIS_TEST_BINARY && 'set AEGIS_TEST_BINARY to a freshly built trusted Aegis binary',
}, t => {
  const directory = path.join(directoryFixture(t), 'real-degraded');
  const report = golden();
  report.agent.status = 'partial';
  report.agent.warnings = ['fixture provider timeout; no API was invoked'];
  const result = renderRecord(record({ report }), { binary: path.resolve(process.env.AEGIS_TEST_BINARY), directory });
  assert.equal(result.record.status, 'degraded', result.record.diagnostic);
  assert.ok(result.html.includes('identity-banner degraded'));
  const gate = JSON.parse(fs.readFileSync(path.join(directory, 'gate.json'), 'utf8'));
  assert.equal(gate.degraded, true);
  assert.equal(gate.blocked, false);
});
