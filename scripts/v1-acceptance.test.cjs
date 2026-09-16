'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const base = 'a'.repeat(40), head = 'b'.repeat(40);
const policy = { fail_on: 'p1', fail_on_needs_review: 'p0', fail_on_incomplete: true, require_agent: false };
const integration = {
  skip: !process.env.AEGIS_TEST_BINARY && 'set AEGIS_TEST_BINARY to a freshly built trusted Aegis binary',
};

// These are offline, versioned synthetic reports, not claims of live analysis.
// Keep their stage outcomes and evidence intact; only bind their comparison to
// the local test run. No model, Docker daemon, API, or credentials are needed.
function golden(name) {
  const report = JSON.parse(fs.readFileSync(path.join(__dirname, '../eval/cases', name, 'report.json'), 'utf8'));
  report.comparison.base_commit = base;
  report.comparison.head_commit = head;
  return report;
}

function fixture(t, report, overrides = {}) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'aegis-v1-acceptance-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  for (const name of ['bin', 'evidence', 'trusted']) fs.mkdirSync(path.join(directory, name));
  const binary = path.resolve(process.env.AEGIS_TEST_BINARY);
  fs.accessSync(binary, fs.constants.X_OK);
  fs.symlinkSync(binary, path.join(directory, 'bin/aegis'));
  const input = path.join(directory, 'evidence/review.json');
  if (report) fs.writeFileSync(input, JSON.stringify(report));
  const configured = { ...policy, ...overrides };
  // The publisher inherits this deliberately minimal child environment, not
  // local provider keys or GitHub credentials from the developer's shell.
  const env = {
    PATH: process.env.PATH || '', GITHUB_WORKSPACE: directory,
    GITHUB_OUTPUT: path.join(directory, 'github-output'),
    GITHUB_STEP_SUMMARY: path.join(directory, 'github-summary'),
    AEGIS_BASE: base, AEGIS_HEAD: head, AEGIS_ARTIFACT_NAME: 'v1-acceptance-report',
    AEGIS_FAIL_ON: configured.fail_on,
    AEGIS_FAIL_ON_NEEDS_REVIEW: configured.fail_on_needs_review,
    AEGIS_FAIL_ON_INCOMPLETE: String(configured.fail_on_incomplete),
    AEGIS_REQUIRE_AGENT: String(configured.require_agent),
    AEGIS_ANALYSIS_RESULT: 'success', AEGIS_ANALYSIS_EXIT: '0',
  };
  return { directory, binary, input, configured, env };
}

function run(binary, args, f) {
  const result = spawnSync(binary, args, {
    cwd: f.directory, env: f.env, encoding: 'utf8', timeout: 30000, maxBuffer: 1024 * 1024,
  });
  assert.ifError(result.error);
  assert.equal(result.signal, null, result.stderr);
  return result;
}

function trustedGate(f) {
  const output = path.join(f.directory, 'trusted');
  const result = run(f.binary, [
    'github', '--report', f.input, '--expected-base', base, '--expected-head', head,
    '--gate-output', path.join(output, 'gate.json'),
    '--html-output', path.join(output, 'review.html'), '--summary', path.join(output, 'summary.md'),
    '--artifact-name', f.env.AEGIS_ARTIFACT_NAME, '--annotations=false',
    '--fail-on', f.configured.fail_on, '--fail-on-needs-review', f.configured.fail_on_needs_review,
    '--fail-on-incomplete=' + f.configured.fail_on_incomplete,
    '--require-agent=' + f.configured.require_agent,
  ], f);
  return { result, output };
}

function published(f, expectedCode, reportValid) {
  // Exercise the production delivery function and its actual Go subprocess.
  // The workflow intentionally enforces its returned gate after artifact upload.
  const result = run(process.execPath, [
    '-e', 'process.exitCode = require(process.argv[1]).publish();',
    path.join(__dirname, 'publish-report.cjs'),
  ], f);
  assert.equal(result.status, expectedCode, result.stderr);
  const artifacts = path.join(f.directory, 'artifacts');
  for (const name of ['review.html', 'review-summary.md', 'publication.json']) {
    const stat = fs.lstatSync(path.join(artifacts, name));
    assert.ok(stat.isFile() && stat.size > 0, `${name} must survive either gate outcome`);
  }
  const summary = fs.readFileSync(path.join(artifacts, 'review-summary.md'), 'utf8');
  const html = fs.readFileSync(path.join(artifacts, 'review.html'), 'utf8');
  const publication = JSON.parse(fs.readFileSync(path.join(artifacts, 'publication.json'), 'utf8'));
  assert.deepEqual(publication, {
    schema_version: 1, base, head, report_valid: reportValid, exit_code: expectedCode, ...f.configured,
  });
  assert.match(html, /<!doctype html>/i);
  assert.equal(fs.readFileSync(f.env.GITHUB_OUTPUT, 'utf8'), `exit-code=${expectedCode}\n`);
  assert.equal(fs.readFileSync(f.env.GITHUB_STEP_SUMMARY, 'utf8'), summary);
  return { artifacts, summary, html };
}

const cases = [
  { name: 'P0 findings block', fixture: 'SEC-003-command-injection', blocked: true,
    verdict: 'blocked', summary: /Merge gate blocked\.\*\* A finding/ },
  { name: 'P1 findings block', fixture: 'BUG-001-failing-regression', blocked: true,
    verdict: 'blocked', summary: /Merge gate blocked\.\*\* A finding/ },
  { name: 'P2-only findings remain advisory', fixture: 'QUAL-001-close-error',
    verdict: 'advisory', summary: /Review completed with non-blocking findings/ },
  { name: 'P3-only findings remain advisory', fixture: 'QUAL-007-dead-helper',
    verdict: 'advisory', summary: /Review completed with non-blocking findings/ },
  { name: 'unresolved P0 blocks without promoting a finding', fixture: 'NR-001-authorization-boundary',
    blocked: true, needsReview: true, verdict: 'needs-review', summary: /Merge gate blocked pending human review/ },
  { name: 'unresolved P1 requires attention but does not block by default', fixture: 'NR-003-state-corruption',
    needsReview: true, verdict: 'needs-review', summary: /Review completed with unresolved hypotheses/ },
  { name: 'configured P1 needs-review threshold blocks unresolved P1', fixture: 'NR-003-state-corruption',
    policy: { fail_on_needs_review: 'p1' }, blocked: true, needsReview: true,
    verdict: 'needs-review', summary: /Merge gate blocked pending human review/ },
  { name: 'clean static-only source review is allowed when the model is optional', fixture: 'CLEAN-007-synchronized-map',
    verdict: 'clear', summary: /Review completed without findings/ },
  { name: 'clean documentation may skip even a required model', fixture: 'CLEAN-001-documentation',
    policy: { require_agent: true }, verdict: 'clear', summary: /Review completed without findings/ },
  { name: 'require-agent blocks a skipped model for source changes', fixture: 'CLEAN-007-synchronized-map',
    policy: { require_agent: true }, blocked: true, incomplete: true,
    verdict: 'incomplete', summary: /The required Reasoning Agent did not complete/ },
  { name: 'optional partial Agent exit 1 remains degraded, not a P1 finding', fixture: 'RES-001-agent-partial-p2-policy',
    analysisExit: '1', degraded: true, verdict: 'degraded', summary: /Review completed with degraded optional stages/ },
  { name: 'require-agent blocks partial reasoning without inventing a P1 finding', fixture: 'RES-001-agent-partial-p2-policy',
    analysisExit: '1', policy: { require_agent: true }, blocked: true, incomplete: true, degraded: true,
    verdict: 'incomplete', summary: /The required Reasoning Agent did not complete/ },
  { name: 'mandatory partial analysis blocks and preserves incomplete evidence', fixture: 'RES-002-analysis-partial',
    analysisExit: '1', executionIncomplete: true, blocked: true, incomplete: true,
    verdict: 'incomplete', summary: /Deterministic analysis completed only partially/ },
];

for (const c of cases) {
  test('V1 offline acceptance: ' + c.name, integration, t => {
    const report = golden(c.fixture);
    const f = fixture(t, report, c.policy);
    f.env.AEGIS_ANALYSIS_EXIT = c.analysisExit || '0';
    const expectedCode = c.blocked ? 1 : 0;
    const trusted = trustedGate(f);
    assert.equal(trusted.result.status, expectedCode, trusted.result.stderr);
    // Consume the production binary's machine decision, never a JS copy of its
    // severity or stage policy. Explicit expectations are the acceptance contract.
    const gate = JSON.parse(fs.readFileSync(path.join(trusted.output, 'gate.json'), 'utf8'));
    assert.deepEqual(gate, { schema_version: 1, base, head, blocked: Boolean(c.blocked),
      incomplete: Boolean(c.incomplete), degraded: Boolean(c.degraded), needs_review: Boolean(c.needsReview) });
    const output = published(f, expectedCode, true);
    assert.match(output.summary, c.summary);
    assert.match(output.html, new RegExp('class="[^"\\n]*\\brisk-' + c.verdict + '\\b'));
    const trustedSummary = fs.readFileSync(path.join(trusted.output, 'summary.md'), 'utf8');
    if (c.executionIncomplete) {
      assert.match(output.summary, /^> ❌ \*\*Review execution incomplete — merge gate blocked/);
      assert.ok(output.summary.endsWith(trustedSummary));
      assert.match(output.html, /Review execution: BLOCKED/);
    } else {
      assert.equal(output.summary, trustedSummary);
      assert.doesNotMatch(output.html, /Review execution: BLOCKED/);
    }
    // Partial stages and Needs Review must remain evidence, not fabricated P1s
    // or completed runs. The publisher copies the original JSON byte-for-byte.
    assert.deepEqual(fs.readFileSync(path.join(output.artifacts, 'review.json')), fs.readFileSync(f.input));
    assert.deepEqual(JSON.parse(fs.readFileSync(f.input, 'utf8')), report);
    const s = report.summary;
    assert.ok(output.summary.includes(`| ${s.critical} | ${s.high} | ${s.medium} | ${s.low + s.info} | ${s.changed_files} | ${s.findings} |`));
    for (const item of [...report.findings, ...report.verification.candidates]) {
      assert.ok(output.summary.includes(item.title), `summary must retain ${item.title}`);
      assert.ok(output.html.includes(item.title), `HTML must retain ${item.title}`);
    }
  });
}

for (const mode of ['missing', 'stale-base', 'stale-head']) {
  test(`V1 offline acceptance: ${mode} evidence blocks and preserves diagnostic artifacts`, integration, t => {
    const report = mode === 'missing' ? null : golden('CLEAN-007-synchronized-map');
    if (mode === 'stale-base') report.comparison.base_commit = 'c'.repeat(40);
    if (mode === 'stale-head') report.comparison.head_commit = 'c'.repeat(40);
    const f = fixture(t, report);
    const trusted = trustedGate(f);
    assert.equal(trusted.result.status, 1, trusted.result.stderr);
    for (const name of ['gate.json', 'review.html', 'summary.md']) {
      assert.equal(fs.existsSync(path.join(trusted.output, name)), false, 'rejected evidence cannot produce a valid gate');
    }
    const output = published(f, 1, false);
    assert.match(output.summary, /Review incomplete — merge gate blocked/);
    assert.match(output.html, /Review incomplete — merge gate blocked/);
    assert.match(output.html, /No clean review conclusion is available/);
    assert.equal(fs.existsSync(path.join(output.artifacts, 'review.json')), false);
  });
}
