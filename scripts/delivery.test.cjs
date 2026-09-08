'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { readEvidence, executionCompatible, publish } = require('./publish-report.cjs');
const publishComment = require('./pr-comment.cjs');
const { validatePublication, host } = require('./host-report.cjs');
const { checkOfflineEval } = require('./check-offline-eval.cjs');
const base = 'a'.repeat(40);
const head = 'b'.repeat(40);

function fixture(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aegis-delivery-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('publisher rejects stale or mismatched evidence, symlinks and oversized files', t => {
  const dir = fixture(t);
  const file = path.join(dir, 'review.json');
  fs.writeFileSync(file, JSON.stringify({ comparison: { base_commit: base, head_commit: head } }));
  assert.ok(readEvidence(file, base, head).length);
  assert.throws(() => readEvidence(file, base, 'c'.repeat(40)), /identity/);
  const link = path.join(dir, 'link.json');
  fs.symlinkSync(file, link);
  assert.throws(() => readEvidence(link, base, head), /regular/);
  fs.truncateSync(file, 17 * 1024 * 1024);
  assert.throws(() => readEvidence(file, base, head), /16 MiB/);
});

test('missing evidence produces blocking feedback rather than a clean review', t => {
  const dir = fixture(t);
  const output = path.join(dir, 'output');
  const stepSummary = path.join(dir, 'summary');
  assert.equal(publish({ GITHUB_WORKSPACE: dir, AEGIS_BASE: base, AEGIS_HEAD: head,
    GITHUB_OUTPUT: output, GITHUB_STEP_SUMMARY: stepSummary }), 1);
  assert.match(fs.readFileSync(output, 'utf8'), /exit-code=1/);
  assert.match(fs.readFileSync(stepSummary, 'utf8'), /Review incomplete — merge gate blocked/);
});

test('optional degraded review exit does not replace publisher policy', t => {
  const report = { comparison: { base_commit: base, head_commit: head },
    analysis: { status: 'complete' }, verification: { status: 'complete' },
    agent: { status: 'partial' }, context: { status: 'complete' }, findings: [{ priority: 'p2' }] };
  assert.equal(executionCompatible(report, 'success', '1'), true);
  assert.equal(executionCompatible(report, 'failure', '1'), false);
  assert.equal(executionCompatible(report, 'success', '2'), false);
  assert.equal(executionCompatible({ ...report, analysis: { status: 'partial' } }, 'success', '1'), false);
  for (const required of [false, true]) {
    const dir = fixture(t);
    fs.mkdirSync(path.join(dir, 'bin'));
    fs.mkdirSync(path.join(dir, 'evidence'));
    fs.writeFileSync(path.join(dir, 'evidence/review.json'), JSON.stringify(report));
    // The fake publisher reflects the independent require-agent decision;
    // this verifies the delivery layer preserves it and passes the flag.
    fs.writeFileSync(path.join(dir, 'bin/aegis'), '#!/usr/bin/env node\nconst fs=require("node:fs"),args=process.argv;fs.writeFileSync(args[args.indexOf("--summary")+1],"P2 visible; agent partial");fs.writeFileSync(args[args.indexOf("--html-output")+1],"<html>Evidence</html>");process.exit(args.includes("--require-agent=true")?1:0);\n', { mode: 0o700 });
    assert.equal(publish({ GITHUB_WORKSPACE: dir, AEGIS_BASE: base, AEGIS_HEAD: head,
      AEGIS_ARTIFACT_NAME: 'review', AEGIS_REQUIRE_AGENT: String(required),
      AEGIS_ANALYSIS_RESULT: 'success', AEGIS_ANALYSIS_EXIT: '1' }), required ? 1 : 0);
  }
});

function commentFixture(t, { stale = false, changesDuringPagination = false, comments = [] } = {}) {
  const dir = fixture(t);
  fs.mkdirSync(path.join(dir, 'artifacts'));
  fs.writeFileSync(path.join(dir, 'artifacts/review-summary.md'), 'Review complete');
  const writes = [];
  let reads = 0;
  const github = {
    rest: {
      pulls: { get: async () => ({ data: { state: 'open', base: { sha: base },
        head: { sha: stale || (changesDuringPagination && reads++ > 0) ? 'c'.repeat(40) : head } } }) },
      issues: {
        listComments: () => {},
        updateComment: async args => writes.push({ kind: 'update', ...args }),
        createComment: async args => writes.push({ kind: 'create', ...args }),
      },
    },
    paginate: async () => comments,
  };
  return { writes, args: { github, context: { repo: { owner: 'owner', repo: 'target' },
    payload: { pull_request: { number: 7 } } }, core: { notice: () => {} } },
  env: { GITHUB_WORKSPACE: dir, AEGIS_HEAD: head, AEGIS_BASE: base,
    AEGIS_ARTIFACT_URL: 'https://github.com/owner/target/actions/runs/1/artifacts/2',
    AEGIS_RUN_URL: 'https://github.com/owner/target/actions/runs/1' } };
}

test('stale review cannot overwrite a newer PR comment', async t => {
  for (const mode of [{ stale: true }, { changesDuringPagination: true }]) {
    const f = commentFixture(t, mode);
    await publishComment(f.args, f.env);
    assert.equal(f.writes.length, 0);
  }
});

test('only a github-actions bot-owned marker can be updated', async t => {
  const marker = '<!-- aegis-code-agent-review -->';
  const f = commentFixture(t, { comments: [
    { id: 1, user: { login: 'attacker', type: 'User' }, body: marker },
    { id: 2, user: { login: 'another[bot]', type: 'Bot' }, body: marker },
    { id: 3, user: { login: 'github-actions[bot]', type: 'Bot' }, body: marker },
  ] });
  await publishComment(f.args, f.env);
  assert.equal(f.writes.length, 1);
  assert.equal(f.writes[0].kind, 'update');
  assert.equal(f.writes[0].comment_id, 3);
  assert.match(f.writes[0].body, new RegExp(head));
});

test('fake review marker in a user comment does not prevent new bot feedback', async t => {
  const f = commentFixture(t, { comments: [
    { id: 1, user: { login: 'attacker', type: 'User' }, body: '<!-- aegis-code-agent-review -->' },
  ] });
  await publishComment(f.args, f.env);
  assert.equal(f.writes[0].kind, 'create');
});

test('external report links are rejected', async t => {
  const f = commentFixture(t);
  await assert.rejects(publishComment(f.args, { ...f.env, AEGIS_ARTIFACT_URL: 'https://evil.example/report' }), /origin/);
  assert.equal(f.writes.length, 0);
});

test('CI review confines credentials and passes untrusted strings only as literal arguments', t => {
  const dir = fixture(t);
  fs.mkdirSync(path.join(dir, 'bin'));
  // A fake binary records arguments; it never invokes a model or executes PR code.
  fs.writeFileSync(path.join(dir, 'bin/aegis'), '#!/usr/bin/env node\nrequire("node:fs").writeFileSync(process.env.GITHUB_WORKSPACE + "/args.json", JSON.stringify(process.argv.slice(2)));\n', { mode: 0o700 });
  const env = { ...process.env, GITHUB_WORKSPACE: dir, RUNNER_TEMP: dir, GITHUB_OUTPUT: path.join(dir, 'output'),
    AEGIS_BASE: base, AEGIS_HEAD: head, AEGIS_PROVIDER: 'orcarouter', AEGIS_PROVIDER_API_KEY: '',
    AEGIS_MODEL: 'literal $(touch /tmp/aegis-must-not-run); --agent-provider deepseek' };
  const result = spawnSync('bash', [path.join(__dirname, 'run-ci-review.sh')], { env, encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  const args = JSON.parse(fs.readFileSync(path.join(dir, 'args.json')));
  assert.equal(args[args.indexOf('--agent-provider') + 1], 'none');
  assert.equal(args[args.indexOf('--config') + 1], '');
  assert.ok(args.includes('--agent-no-dotenv'));
  assert.equal(args[args.indexOf('--sandbox') + 1], 'docker');
  assert.equal(args[args.indexOf('--agent-model') + 1], env.AEGIS_MODEL);
});

test('reusable workflow builds Aegis from the called workflow SHA, never caller SHA', async () => {
  const yaml = fs.readFileSync(path.join(__dirname, '../.github/workflows/aegis-review.yml'), 'utf8');
  const block = yaml.match(/          script: \|\n((?:            .*\n|\n)+)/)[1];
  const script = block.split('\n').map(line => line.slice(12)).join('\n');
  const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
  const resolve = new AsyncFunction('github', 'context', 'core', script);
  const outputs = {};
  const context = { eventName: 'pull_request', runId: 1, repo: { owner: 'consumer', repo: 'service' },
    payload: { pull_request: { number: 7, base: { sha: base, repo: { full_name: 'consumer/service' } },
      head: { sha: head, repo: { full_name: 'fork/service' } } } } };
  const source = 'd'.repeat(40);
  const run = { path: '.github/workflows/review.yml', referenced_workflows: [{
    path: 'Molly166/AegisCodeAgent/.github/workflows/aegis-review.yml@v1.0.0', sha: source,
  }] };
  await resolve({ request: async () => ({ data: run }) }, context, { setOutput: (key, value) => { outputs[key] = value; } });
  assert.equal(outputs['reviewer-sha'], source);
  assert.equal(outputs['same-repository'], false);
  run.referenced_workflows = [];
  await assert.rejects(resolve({ request: async () => ({ data: run }) }, context, { setOutput: () => {} }), /unambiguously/);
});

test('workflow does not route a DeepSeek key to another provider or a fork', () => {
  const yaml = fs.readFileSync(path.join(__dirname, '../.github/workflows/aegis-review.yml'), 'utf8');
  const expression = yaml.match(/AEGIS_PROVIDER_API_KEY: \$\{\{ (.+) \}\}/)[1]
    .replaceAll('needs.prepare.outputs.same-repository', 'same')
    .replaceAll('github.actor', 'actor').replaceAll('inputs.provider', 'provider')
    .replaceAll('secrets.provider-api-key', 'partnerKey').replaceAll('secrets.DEEPSEEK_API_KEY', 'deepKey');
  const select = new Function('same', 'actor', 'provider', 'partnerKey', 'deepKey', `return ${expression}`);
  for (const provider of ['orcarouter', 'openai-compatible', 'none']) {
    assert.equal(select('true', 'molly', provider, '', 'deep-key'), '');
  }
  assert.equal(select('true', 'molly', 'deepseek', '', 'deep-key'), 'deep-key');
  assert.equal(select('true', 'molly', '', '', 'deep-key'), 'deep-key');
  assert.equal(select('false', 'molly', 'orcarouter', 'orca-key', 'deep-key'), '');
  assert.equal(select('true', 'dependabot[bot]', 'orcarouter', 'orca-key', 'deep-key'), '');
  assert.equal(select('true', 'molly', 'orcarouter', 'orca-key', 'deep-key'), 'orca-key');
});

test('required merge check runs and fails even when preparation was skipped or failed', () => {
  const yaml = fs.readFileSync(path.join(__dirname, '../.github/workflows/aegis-review.yml'), 'utf8');
  const gate = yaml.slice(yaml.indexOf('\n  gate:\n'));
  assert.match(gate, /if: always\(\)/);
  assert.match(gate, /needs: \[prepare, analyze, publish\]/);
  const script = gate.split('        run: |\n')[1].split('\n').map(line => line.slice(10)).join('\n');
  for (const prepare of ['failure', 'skipped', 'cancelled']) {
    const result = spawnSync('bash', ['-e', '-c', script], { env: {
      AEGIS_PREPARE: prepare, AEGIS_ANALYZE: 'success', AEGIS_PUBLISH: 'success', AEGIS_GATE: '0',
    } });
    assert.notEqual(result.status, 0);
  }
});

test('public report re-render rejects forged policy, stale commits and symlink manifests', t => {
  const publication = { schema_version: 1, report_valid: true, base, head, exit_code: 1,
    fail_on: 'p1', fail_on_needs_review: 'p0', fail_on_incomplete: true, require_agent: false };
  assert.equal(validatePublication(publication, base, head), publication);
  assert.throws(() => validatePublication({ ...publication, head: 'c'.repeat(40) }, base, head), /provenance/);
  assert.throws(() => validatePublication({ ...publication, require_agent: 'false' }, base, head), /policy/);
  assert.throws(() => validatePublication({ ...publication, fail_on: '--help' }, base, head), /priority/);
  const dir = fixture(t);
  fs.mkdirSync(path.join(dir, 'evidence'));
  fs.writeFileSync(path.join(dir, 'evidence/review.json'), JSON.stringify({ comparison: { base_commit: base, head_commit: head } }));
  fs.writeFileSync(path.join(dir, 'manifest.json'), JSON.stringify(publication));
  fs.symlinkSync(path.join(dir, 'manifest.json'), path.join(dir, 'evidence/publication.json'));
  assert.throws(() => host({ GITHUB_WORKSPACE: dir, AEGIS_BASE: base, AEGIS_HEAD: head }), /metadata file/);
});

test('publisher rejection cannot mark malformed status/verdict evidence as publishable', t => {
  const dir = fixture(t);
  fs.mkdirSync(path.join(dir, 'bin'));
  fs.mkdirSync(path.join(dir, 'evidence'));
  fs.writeFileSync(path.join(dir, 'evidence/review.json'), JSON.stringify({
    comparison: { base_commit: base, head_commit: head }, analysis: { status: 'not-a-status' },
  }));
  // The delivery layer must honor the trusted Go schema validator. It does
  // not keep a second, drifting copy of Go's status/verdict enum definitions.
  fs.writeFileSync(path.join(dir, 'bin/aegis'), '#!/usr/bin/env node\nprocess.exit(2);\n', { mode: 0o700 });
  assert.equal(publish({ GITHUB_WORKSPACE: dir, AEGIS_BASE: base, AEGIS_HEAD: head,
    AEGIS_ARTIFACT_NAME: 'review', AEGIS_ANALYSIS_RESULT: 'success', AEGIS_ANALYSIS_EXIT: '0' }), 1);
  assert.equal(JSON.parse(fs.readFileSync(path.join(dir, 'artifacts/publication.json'))).report_valid, false);
});

test('successful publisher exit without expected evidence files still blocks delivery', t => {
  const dir = fixture(t);
  fs.mkdirSync(path.join(dir, 'bin'));
  fs.mkdirSync(path.join(dir, 'evidence'));
  fs.writeFileSync(path.join(dir, 'evidence/review.json'), JSON.stringify({ comparison: { base_commit: base, head_commit: head } }));
  fs.writeFileSync(path.join(dir, 'bin/aegis'), '#!/usr/bin/env node\nprocess.exit(0);\n', { mode: 0o700 });
  assert.equal(publish({ GITHUB_WORKSPACE: dir, AEGIS_BASE: base, AEGIS_HEAD: head,
    AEGIS_ARTIFACT_NAME: 'review', AEGIS_ANALYSIS_RESULT: 'success', AEGIS_ANALYSIS_EXIT: '0' }), 1);
  assert.equal(JSON.parse(fs.readFileSync(path.join(dir, 'artifacts/publication.json'))).report_valid, false);
});

test('offline live eval distinguishes execution health from actual detection misses', () => {
  const report = { schema_version: 'live-v1', mode: 'live_pipeline', provider: 'none',
    live_model_requested: false, repeats: 1,
    metrics: { runs: 12, unique_cases: 12, tokens: 0, passed_runs: 6,
      incomplete_rate: { numerator: 0, denominator: 12, value: 0 },
      finding_recall: { numerator: 2, denominator: 8, value: 0.25 },
      finding_precision: { numerator: 2, denominator: 2, value: 1 },
      gate_accuracy: { numerator: 6, denominator: 12, value: 0.5 } },
    runs: Array.from({ length: 12 }, (_, i) => ({ case_id: 'case-' + i, incomplete: false, tokens: 0, review: {} })),
  };
  const result = checkOfflineEval(report);
  assert.equal(result.healthy, true);
  assert.equal(result.detectionPassed, false);
  assert.match(result.summary, /Finding recall \| 2\/8/);
  assert.match(result.summary, /not converted into passing labels/);
  assert.equal(checkOfflineEval({ ...report, live_model_requested: true }).healthy, false);
  assert.equal(checkOfflineEval({ ...report, runs: report.runs.slice(1) }).healthy, false);
  assert.equal(checkOfflineEval({ ...report, metrics: { ...report.metrics, incomplete_rate: { numerator: 1, denominator: 12 } } }).healthy, false);
});
