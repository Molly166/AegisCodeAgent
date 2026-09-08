'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { createHash } = require('node:crypto');
const root = path.join(__dirname, '..');
const pending = path.join(root, 'examples/aegis-review-v1-migration.yml');
const active = path.join(root, '.github/workflows/aegis-review.yml');
const yaml = fs.readFileSync(fs.existsSync(pending) ? pending : active, 'utf8');
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
const base = 'a'.repeat(40), head = 'b'.repeat(40), reviewer = 'c'.repeat(40);

function inlineStep(name) {
  const start = yaml.indexOf('      - name: ' + name + '\n');
  assert.notEqual(start, -1, 'Missing workflow step ' + name);
  const remaining = yaml.slice(start);
  const next = remaining.indexOf('\n      - name:', 1);
  const step = next < 0 ? remaining : remaining.slice(0, next);
  const match = step.match(/          script: \|\n((?:            [^\n]*\n|\n)+)/);
  assert.ok(match, 'Missing inline script ' + name);
  return new AsyncFunction('require', 'process', 'github', 'context', 'core',
    match[1].split('\n').map(line => line.slice(12)).join('\n'));
}

function fixture(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aegis-migration-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function trustedFixture(dir) {
  fs.mkdirSync(path.join(dir, 'reviewer/scripts'), { recursive: true });
  for (const file of ['analysis.Dockerfile', 'prepare-analysis-cache.sh', 'run-ci-review.sh',
    'publish-report.cjs', 'pr-comment.cjs']) {
    fs.writeFileSync(path.join(dir, 'reviewer/scripts', file), 'not executed by preflight');
  }
  fs.writeFileSync(path.join(dir, 'reviewer/scripts/reviewer-contract.json'), '{"workflow_protocol":1}');
}

test('staged migration cannot silently replace the active legacy review contract', () => {
  if (fs.existsSync(pending)) {
    // Trusted master workflow, with only a documented SC2129 style suppression.
    assert.equal(createHash('sha256').update(fs.readFileSync(active)).digest('hex'),
      '11dcdd369a4a83eba8dbcffd3ac7cae9870db81570616bb4dcb82cf1f4308d3c');
  } else {
    assert.match(yaml, /workflow_call:/);
    assert.match(yaml, /reviewerSHA = pr\.base\.sha/);
  }
});

test('trusted source protocol preflight accepts only complete regular v1 source', async t => {
  const run = inlineStep('Check trusted reviewer protocol before analysis');
  for (const mode of ['valid', 'legacy', 'wrong-version', 'missing-file', 'symlink-file', 'symlink-directory', 'oversized']) {
    const dir = fixture(t), outputs = {}, warnings = [];
    if (mode !== 'legacy') trustedFixture(dir);
    const scripts = path.join(dir, 'reviewer/scripts');
    if (mode === 'wrong-version') fs.writeFileSync(path.join(scripts, 'reviewer-contract.json'), '{"workflow_protocol":2}');
    if (mode === 'missing-file') fs.unlinkSync(path.join(scripts, 'publish-report.cjs'));
    if (mode === 'symlink-file') {
      fs.renameSync(path.join(scripts, 'publish-report.cjs'), path.join(dir, 'publisher'));
      fs.symlinkSync(path.join(dir, 'publisher'), path.join(scripts, 'publish-report.cjs'));
    }
    if (mode === 'symlink-directory') {
      fs.renameSync(scripts, path.join(dir, 'outside-scripts'));
      fs.symlinkSync(path.join(dir, 'outside-scripts'), scripts);
    }
    if (mode === 'oversized') fs.writeFileSync(path.join(scripts, 'reviewer-contract.json'), ' '.repeat(4097));
    await run(require, { env: { GITHUB_WORKSPACE: dir } }, {}, {}, {
      setOutput: (key, value) => { outputs[key] = value; }, warning: message => warnings.push(message),
    });
    assert.equal(outputs.compatible, String(mode === 'valid'), mode);
    assert.equal(warnings.length, mode === 'valid' ? 0 : 1, mode);
  }
  assert.match(yaml, /name: Sandboxed analysis\n    needs: prepare\n    if: \$\{\{ needs\.prepare\.outputs\.compatible == 'true' \}\}/);
});

test('publisher bootstrap failure preserves blocking HTML without executing Base or Head scripts', async t => {
  const run = inlineStep('Preserve blocking diagnostics when the trusted publisher cannot run');
  for (const compatible of ['false', 'true', '']) {
    const dir = fixture(t), outputs = {};
    let summary = '';
    const core = { setOutput: (key, value) => { outputs[key] = value; }, summary: {
      addRaw: value => { summary += value; return { write: async () => {} }; },
    } };
    await run(require, { env: { GITHUB_WORKSPACE: dir, AEGIS_BASE: base, AEGIS_HEAD: head,
      AEGIS_REVIEWER_SHA: reviewer, AEGIS_COMPATIBLE: compatible } }, {}, {}, core);
    assert.equal(outputs['exit-code'], '1');
    assert.match(summary, /Review incomplete — merge gate blocked/);
    assert.match(summary, compatible === 'true' ? /publisher setup or execution failed/ : /Analysis was not started/);
    const html = fs.readFileSync(path.join(dir, 'artifacts/review.html'), 'utf8');
    assert.match(html, /<!doctype html>/);
    assert.match(html, /No clean review conclusion/);
    const publication = JSON.parse(fs.readFileSync(path.join(dir, 'artifacts/publication.json')));
    assert.equal(publication.report_valid, false);
    assert.equal(publication.exit_code, 1);
    assert.equal(publication.head, head);
    assert.equal(fs.existsSync(path.join(dir, 'reviewer')), false);
  }
  assert.match(yaml, /always\(\) && steps\.gate\.outcome != 'success'/);
});

test('inline diagnostic feedback respects ownership and both commit identities', async t => {
  const run = inlineStep('Refresh bot-owned PR feedback only for the current Head');
  for (const mode of ['current', 'stale-head', 'stale-base', 'closed', 'changed-during-list', 'forged-marker', 'bad-origin']) {
    const dir = fixture(t), writes = [];
    fs.mkdirSync(path.join(dir, 'artifacts'));
    fs.writeFileSync(path.join(dir, 'artifacts/review-summary.md'), 'Review incomplete — merge gate blocked');
    let reads = 0;
    const current = () => ({ state: mode === 'closed' ? 'closed' : 'open',
      base: { sha: mode === 'stale-base' ? reviewer : base },
      head: { sha: mode === 'stale-head' || (mode === 'changed-during-list' && reads++ > 0) ? reviewer : head } });
    const github = { rest: { pulls: { get: async () => ({ data: current() }) }, issues: {
      listComments: () => {}, createComment: async value => writes.push({ kind: 'create', ...value }),
      updateComment: async value => writes.push({ kind: 'update', ...value }),
    } }, paginate: async () => [{ id: 1, user: { login: mode === 'forged-marker' ? 'attacker' : 'github-actions[bot]',
      type: mode === 'forged-marker' ? 'User' : 'Bot' }, body: '<!-- aegis-code-agent-review -->' }] };
    const env = { GITHUB_WORKSPACE: dir, GITHUB_SERVER_URL: 'https://github.com',
      AEGIS_BASE: base, AEGIS_HEAD: head, AEGIS_ARTIFACT_URL: mode === 'bad-origin' ?
        'https://evil.example/report' : 'https://github.com/owner/repo/actions/runs/1/artifacts/2',
      AEGIS_RUN_URL: 'https://github.com/owner/repo/actions/runs/1' };
    const promise = run(require, { env }, github, { repo: { owner: 'owner', repo: 'repo' },
      payload: { pull_request: { number: 12 } } }, {});
    if (mode === 'bad-origin') await assert.rejects(promise, /Untrusted report link/);
    else await promise;
    if (['current', 'forged-marker'].includes(mode)) {
      assert.equal(writes.length, 1);
      assert.equal(writes[0].kind, mode === 'current' ? 'update' : 'create');
      assert.match(writes[0].body, /Review incomplete/);
    } else assert.equal(writes.length, 0, mode);
  }
});
