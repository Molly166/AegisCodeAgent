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
const yaml = fs.readFileSync(active, 'utf8');
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

test('v1 is the active entry point and matches the approved public-report producer', () => {
  assert.equal(fs.existsSync(pending), false, 'acceptance must not silently test a staged workflow');
  assert.match(yaml, /workflow_call:/);
  assert.match(yaml, /reviewerSHA = pr\.base\.sha/);
  const jobs = yaml.slice(yaml.indexOf('\njobs:\n'));
  assert.deepEqual([...jobs.matchAll(/^  ([a-z]+):$/gm)].map(match => match[1]),
    ['prepare', 'analyze', 'publish', 'gate']);
  const publisher = fs.readFileSync(path.join(root, 'scripts/report-pages-source.cjs'), 'utf8');
  const approved = publisher.match(/v1:\s*'([0-9a-f]{64})'/)?.[1];
  assert.ok(approved, 'the public publisher must recognize an audited v1 producer');
  assert.equal(createHash('sha256').update(yaml).digest('hex'), approved);
  for (const action of yaml.matchAll(/\buses:\s+([^\s#]+)/g)) {
    assert.match(action[1], /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+@[0-9a-f]{40}$/,
      'active v1 actions must use immutable reviewed SHAs');
  }
});

test('active v1 self-review resolves Base, rejects ambiguous sources and never falls back to Head', async () => {
  const resolve = inlineStep('Resolve immutable workflow identity and PR commits');
  for (const mode of ['self', 'fork', 'external', 'external-missing', 'ambiguous', 'invalid-sha', 'wrong-base-repo', 'unsafe-event']) {
    const external = mode.startsWith('external');
    const owner = external ? 'consumer' : 'Molly166';
    const repo = external ? 'service' : 'AegisCodeAgent';
    const context = { eventName: mode === 'unsafe-event' ? 'pull_request_target' : 'pull_request',
      runId: 100, repo: { owner, repo }, payload: { pull_request: { number: 7,
        base: { sha: base, repo: { full_name: mode === 'wrong-base-repo' ? 'attacker/repo' : `${owner}/${repo}` } },
        head: { sha: head, repo: { full_name: mode === 'fork' ? `fork/${repo}` : `${owner}/${repo}` } },
      } } };
    const called = { path: 'Molly166/AegisCodeAgent/.github/workflows/aegis-review.yml@v1.0.0', sha: reviewer };
    const run = { path: external ? '.github/workflows/caller.yml' : '.github/workflows/aegis-review.yml',
      referenced_workflows: mode === 'external' ? [called] : mode === 'ambiguous' ? [called, called] :
        mode === 'invalid-sha' ? [{ ...called, sha: 'master' }] : [] };
    const outputs = {};
    const execute = () => resolve(require, { env: { GITHUB_RUN_ATTEMPT: '2' } }, {
      request: async (route, params) => {
        assert.equal(route, 'GET /repos/{owner}/{repo}/actions/runs/{run_id}/attempts/{attempt_number}');
        assert.equal(params.attempt_number, 2);
        return { data: run };
      },
    }, context, { setOutput: (key, value) => { outputs[key] = value; } });
    if (['self', 'fork', 'external'].includes(mode)) {
      await execute();
      assert.deepEqual(outputs, { 'reviewer-sha': external ? reviewer : base,
        base, head, number: '7', 'same-repository': mode !== 'fork' }, mode);
      assert.notEqual(outputs['reviewer-sha'], head);
    } else {
      await assert.rejects(execute(), /unambiguously|immutable Git commit SHA|base repository|pull_request event/, mode);
      assert.deepEqual(outputs, {}, 'rejected identities must not reach downstream jobs');
    }
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
