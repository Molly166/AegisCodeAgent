'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const { resolveSource, createRecord } = require('./report-pages-source.cjs');

const base = 'a'.repeat(40), head = 'b'.repeat(40), future = 'c'.repeat(40);
const repo = { owner: 'owner', repo: 'reviewer' };
const policy = { fail_on: 'p1', fail_on_needs_review: 'p0', fail_on_incomplete: true, require_agent: false };
const workflowPath = '.github/workflows/aegis-review.yml';
const stagedV1Workflow = path.join(__dirname, '../examples/aegis-review-v1-migration.yml');
const trustedV1Workflow = fs.readFileSync(fs.existsSync(stagedV1Workflow) ? stagedV1Workflow : path.join(__dirname, '..', workflowPath));

function workflowContent(bytes) {
  bytes = Buffer.from(bytes);
  return { type: 'file', path: workflowPath, encoding: 'base64', size: bytes.length, content: bytes.toString('base64') };
}

function apiFixture() {
  const association = { number: 7, base: { sha: base, repo: { url: 'https://api.github.com/repos/owner/reviewer' } },
    head: { sha: head, repo: { url: 'https://api.github.com/repos/contributor/fork' } } };
  const current = { id: 123, name: 'Aegis Code Review', run_attempt: 2, status: 'completed', event: 'pull_request',
    path: workflowPath, repository: { full_name: 'owner/reviewer', private: false }, head_sha: head, referenced_workflows: [],
    conclusion: 'failure', pull_requests: [association], created_at: '2026-09-08T12:00:00Z',
    run_started_at: '2026-09-09T08:00:00Z', updated_at: '2026-09-09T08:10:00Z' };
  const state = {
    repository: { full_name: 'owner/reviewer', private: false, visibility: 'public' },
    current, attempt: structuredClone(current), latest: null,
    artifacts: [{ id: 456, name: 'aegis-review-report-2', expired: false, size_in_bytes: 1234,
      created_at: '2026-09-09T08:09:00Z', workflow_run: { id: 123, head_sha: head } }],
    workflow: workflowContent(trustedV1Workflow),
    calls: [], reads: 0,
  };
  const github = {
    rest: {
      repos: {
        get: async args => { state.calls.push(['repo', args]); return { data: state.repository }; },
        getContent: async args => { state.calls.push(['content', args]); return { data: state.workflow }; },
      },
      actions: {
        getWorkflowRun: async args => { state.calls.push(['run', args]); return { data: state.reads++ > 0 && state.latest ? state.latest : state.current }; },
        getWorkflowRunAttempt: async args => { state.calls.push(['attempt', args]); return { data: state.attempt }; },
        listWorkflowRunArtifacts: () => {},
      },
    },
    paginate: async (endpoint, args) => {
      assert.equal(endpoint, github.rest.actions.listWorkflowRunArtifacts);
      state.calls.push(['artifacts', args]);
      return state.artifacts;
    },
  };
  return { state, github, options: { github, repo, runId: 123, attempt: 2, prNumber: 7 } };
}

function directoryFixture(t) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'aegis-pages-source-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  return directory;
}

function reportFixture(t, protocol = 'v1') {
  const directory = directoryFixture(t);
  const report = { comparison: { base_commit: base, head_commit: head }, findings: [],
    analysis: { status: 'complete' }, verification: { status: 'complete' } };
  const publication = { schema_version: 1, base, head, report_valid: true, exit_code: 1, ...policy };
  const source = { schema_version: 1, repository: 'owner/reviewer', pr_number: 7, base, head,
    run_id: 123, run_attempt: 2, run_url: 'https://github.com/owner/reviewer/actions/runs/123/attempts/2',
    artifact_url: 'https://github.com/owner/reviewer/actions/runs/123/artifacts/456',
    created_at: '2026-09-08T12:00:00.000Z', conclusion: 'failure', protocol,
    artifact_id: 456, diagnostic: protocol === 'diagnostic' ? 'No validated report evidence is available.' : '', policy: { ...policy } };
  fs.writeFileSync(path.join(directory, 'review.json'), JSON.stringify(report));
  if (protocol === 'v1') fs.writeFileSync(path.join(directory, 'publication.json'), JSON.stringify(publication));
  return { directory, report, publication, source };
}

test('source resolves one exact completed attempt and keeps historical PR commits', async () => {
  const fixture = apiFixture();
  // An attempt response can omit associations. The run's association remains
  // the source; no current-PR API exists in this fake to substitute newer Head.
  fixture.state.attempt.pull_requests = [];
  const source = await resolveSource({ ...fixture.options, repo: 'owner/reviewer', prNumber: undefined });
  assert.equal(source.head, head);
  assert.equal(source.base, base);
  assert.equal(source.pr_number, 7);
  assert.equal(source.protocol, 'v1');
  assert.equal(source.artifact_id, 456);
  assert.equal(source.run_attempt, 2);
  assert.equal(source.run_url, 'https://github.com/owner/reviewer/actions/runs/123/attempts/2');
  assert.equal(source.artifact_url, 'https://github.com/owner/reviewer/actions/runs/123/artifacts/456');
  assert.deepEqual(source.policy, policy);
  assert.equal(fixture.state.calls.filter(([name]) => name === 'run').length, 2);
  assert.deepEqual(fixture.state.calls.find(([name]) => name === 'attempt')[1], { ...repo, run_id: 123, attempt_number: 2 });
});

test('source rejects invalid run provenance and missing or ambiguous PR associations', async t => {
  const invalid = [
    ['private repository', f => { f.state.repository.private = true; }],
    ['internal repository', f => { f.state.repository.visibility = 'internal'; }],
    ['repository substitution', f => { f.state.repository.full_name = 'attacker/reviewer'; }],
    ['wrong source repository', f => { f.state.current.repository.full_name = 'attacker/reviewer'; }],
    ['uncompleted run', f => { f.state.current.status = 'in_progress'; }],
    ['wrong workflow', f => { f.state.current.path = '.github/workflows/ci.yml'; }],
    ['wrong workflow name', f => { f.state.current.name = 'Untrusted Workflow'; }],
    ['wrong event', f => { f.state.current.event = 'pull_request_target'; }],
    ['wrong run ID', f => { f.state.current.id = 987; }],
    ['invalid conclusion', f => { f.state.current.conclusion = '<script>bad</script>'; }],
    ['unassociated fork', f => { f.state.current.pull_requests = []; }],
    ['ambiguous association', f => { f.state.current.pull_requests.push(structuredClone(f.state.current.pull_requests[0])); }],
    ['mismatched requested PR', f => { f.options.prNumber = 8; }],
    ['nonimmutable Base', f => { f.state.current.pull_requests[0].base.sha = 'master'; }],
    ['different associated Head', f => { f.state.current.pull_requests[0].head.sha = future; }],
    ['foreign associated Base', f => { f.state.current.pull_requests[0].base.repo.url = 'https://api.github.com/repos/attacker/reviewer'; }],
    ['contradictory associated repository metadata', f => { f.state.current.pull_requests[0].base.repo.full_name = 'attacker/reviewer'; }],
    ['contradicting attempt association', f => { f.state.attempt.pull_requests[0].base.sha = future; }],
    ['wrong selected attempt', f => { f.options.attempt = 1; }],
    ['invalid selected attempt', f => { f.options.attempt = '2\nextra'; }],
    ['wrong attempt response', f => { f.state.attempt.run_attempt = 3; }],
    ['wrong attempt Head', f => { f.state.attempt.head_sha = future; }],
    ['invalid timestamp', f => { f.state.attempt.created_at = '2026-02-31T12:00:00Z'; }],
    ['reversed timestamps', f => { f.state.attempt.run_started_at = '2026-09-09T08:11:00Z'; }],
    ['unsafe repository path', f => { f.options.repo = 'owner/../attacker'; }],
  ];
  for (const [label, mutate] of invalid) {
    await t.test(label, async () => {
      const f = apiFixture(); mutate(f);
      await assert.rejects(resolveSource(f.options));
    });
  }
});

test('a rerun advancing during artifact lookup cannot mix attempts', async () => {
  const f = apiFixture();
  f.state.latest = { ...structuredClone(f.state.current), run_attempt: 3 };
  await assert.rejects(resolveSource(f.options), /attempt/);
});

test('trusted completion event restores association removed after a prompt PR merge', async () => {
  const f = apiFixture();
  const eventRun = structuredClone(f.state.current);
  f.state.current.pull_requests = [];
  f.state.attempt.pull_requests = [];
  const source = await resolveSource({ ...f.options, eventRun });
  assert.equal(source.pr_number, 7);
  assert.equal(source.head, head);
  assert.equal(source.base, base);
  assert.equal(source.protocol, 'v1');
  await assert.rejects(resolveSource(f.options), /exactly one/); // Manual has no event fallback.
});

test('trusted event may omit optional topology metadata without weakening REST or identity checks', async t => {
  const f = apiFixture();
  const eventRun = structuredClone(f.state.current);
  delete eventRun.referenced_workflows;
  f.state.current.pull_requests = [];
  f.state.attempt.pull_requests = [];
  assert.equal((await resolveSource({ ...f.options, eventRun })).protocol, 'v1');
  for (const which of ['current', 'attempt']) {
    await t.test(`missing ${which} REST topology remains rejected`, async () => {
      const changed = apiFixture();
      delete changed.state[which].referenced_workflows;
      const source = await resolveSource({ ...changed.options, eventRun });
      assert.equal(source.protocol, 'diagnostic');
      assert.match(source.diagnostic, /workflow chains/);
    });
  }
  for (const references of [null, [{ path: 'owner/reviewer/.github/workflows/nested.yml@main', sha: head }]]) {
    await t.test('present event topology must be an empty array', async () => {
      const changed = apiFixture();
      const source = await resolveSource({ ...changed.options, eventRun: { ...eventRun, referenced_workflows: references } });
      assert.equal(source.protocol, 'diagnostic');
    });
  }
  const invalidEvent = { ...eventRun };
  delete invalidEvent.run_attempt;
  await assert.rejects(resolveSource({ ...f.options, eventRun: invalidEvent }), /attempt/);
});

test('event fallback rejects mismatched provenance and conflicting live associations', async t => {
  const invalid = [
    ['different ID', event => { event.id = 987; }],
    ['different attempt', event => { event.run_attempt = 1; }],
    ['different Head', event => { event.head_sha = future; }],
    ['different conclusion', event => { event.conclusion = 'success'; }],
    ['different repository', event => { event.repository.full_name = 'attacker/reviewer'; }],
    ['wrong event', event => { event.event = 'push'; }],
    ['wrong workflow', event => { event.path = '.github/workflows/ci.yml'; }],
    ['unassociated event', event => { event.pull_requests = []; }],
    ['ambiguous event', event => { event.pull_requests.push(structuredClone(event.pull_requests[0])); }],
    ['foreign Base repository', event => { event.pull_requests[0].base.repo.url = 'https://api.github.com/repos/attacker/reviewer'; }],
    ['conflicting Base', event => { event.pull_requests[0].base.sha = future; }],
    ['conflicting PR', event => { event.pull_requests[0].number = 8; }],
  ];
  for (const [label, mutate] of invalid) {
    await t.test(label, async () => {
      const f = apiFixture();
      const eventRun = structuredClone(f.state.current);
      mutate(eventRun);
      await assert.rejects(resolveSource({ ...f.options, eventRun }));
    });
  }
});

test('invalid report artifacts become controlled diagnostics without download IDs', async t => {
  const invalid = [
    ['missing', state => { state.artifacts = []; }],
    ['old attempt only', state => { state.artifacts[0].name = 'aegis-review-report-1'; }],
    ['ambiguous', state => { state.artifacts.push({ ...state.artifacts[0], id: 789 }); }],
    ['mixed legacy and v1', state => { state.artifacts.push({ ...state.artifacts[0], id: 789, name: 'aegis-review-report' }); }],
    ['expired', state => { state.artifacts[0].expired = true; }],
    ['oversized', state => { state.artifacts[0].size_in_bytes = 20 * 1024 * 1024 + 1; }],
    ['foreign run', state => { state.artifacts[0].workflow_run.id = 987; }],
    ['foreign commit', state => { state.artifacts[0].workflow_run.head_sha = future; }],
    ['old timestamp', state => { state.artifacts[0].created_at = '2026-09-08T12:09:00Z'; }],
    ['future timestamp', state => { state.artifacts[0].created_at = '2026-09-09T08:11:00Z'; }],
    ['invalid timestamp', state => { state.artifacts[0].created_at = 'bad-untrusted-value'; }],
    ['invalid artifact ID', state => { state.artifacts[0].id = '../456'; }],
  ];
  for (const [label, mutate] of invalid) {
    await t.test(label, async () => {
      const f = apiFixture(); mutate(f.state);
      const source = await resolveSource(f.options);
      assert.equal(source.protocol, 'diagnostic');
      assert.equal(source.artifact_id, 0);
      assert.equal(source.artifact_url, '');
      assert.ok(source.diagnostic);
      assert.doesNotMatch(source.diagnostic, /bad-untrusted-value/);
    });
  }
});

test('legacy policy is unavailable for an unapproved workflow digest', async () => {
  const f = apiFixture();
  f.state.artifacts[0].name = 'aegis-review-report';
  const source = await resolveSource(f.options);
  assert.equal(source.protocol, 'diagnostic');
  assert.equal(source.artifact_id, 0);
  assert.match(source.diagnostic, /approved version/);
  assert.deepEqual(f.state.calls.find(([name]) => name === 'content')[1], { ...repo, path: workflowPath, ref: head });
});

test('approved v1 producer digest matches the reviewed activation snapshot', async () => {
  assert.equal(crypto.createHash('sha256').update(trustedV1Workflow).digest('hex'),
    '5c1ca01490ab638d8ffa3c2835a79542ea48d49618ac79c1d746838c825c1cce',
    'Workflow edits require a reviewed producer allow-list update, not an automatic digest refresh');
  const f = apiFixture();
  assert.equal((await resolveSource(f.options)).protocol, 'v1');
  assert.deepEqual(f.state.calls.find(([name]) => name === 'content')[1], { ...repo, path: workflowPath, ref: head });
});

test('a spoofed v1 artifact cannot bypass producer trust with a valid-looking name or manifest', async t => {
  for (const content of ['name: Aegis Code Review\njobs: fake\n', Buffer.concat([trustedV1Workflow, Buffer.from('\n# unapproved change\n')])]) {
    await t.test('unknown producer bytes', async () => {
      const f = apiFixture();
      f.state.workflow = workflowContent(content);
      // The artifact list cannot attest the manifest's policy or authenticity.
      f.state.artifacts[0].publication = { schema_version: 1, report_valid: true, exit_code: 0, fail_on: 'none' };
      const source = await resolveSource(f.options);
      assert.equal(source.protocol, 'diagnostic');
      assert.equal(source.artifact_id, 0);
      assert.equal(source.artifact_url, '');
      assert.match(source.diagnostic, /producer workflow.*approved version/);
    });
  }
});

test('producer policy versions cannot be swapped by changing the artifact name', async t => {
  const f = apiFixture();
  t.mock.method(crypto, 'createHash', () => ({ update() { return this; },
    digest() { return '11dcdd369a4a83eba8dbcffd3ac7cae9870db81570616bb4dcb82cf1f4308d3c'; } }));
  assert.equal((await resolveSource(f.options)).protocol, 'diagnostic'); // Legacy bytes + v1 name.
});

test('reusable, nested and unknown producer topology remains diagnostic even for approved bytes', async t => {
  for (const [label, change] of [
    ['run chain', f => { f.state.current.referenced_workflows = [{ path: 'owner/reviewer/.github/workflows/other.yml@main', sha: head }]; }],
    ['attempt chain', f => { f.state.attempt.referenced_workflows = [{ path: 'owner/reviewer/.github/workflows/other.yml@main', sha: head }]; }],
    ['unknown topology', f => { delete f.state.current.referenced_workflows; }],
  ]) {
    await t.test(label, async () => {
      const f = apiFixture(); change(f);
      const source = await resolveSource(f.options);
      assert.equal(source.protocol, 'diagnostic');
      assert.equal(source.artifact_id, 0);
      assert.match(source.diagnostic, /workflow chains/);
    });
  }
});

test('approved legacy digest permits fixed policy only for the selected attempt', async t => {
  const f = apiFixture();
  f.state.artifacts[0].name = 'aegis-review-report';
  f.state.workflow = workflowContent('test');
  // Digest is mocked only for the allow-list branch; the preceding test uses
  // real SHA256 and proves arbitrary content cannot claim legacy compatibility.
  const digest = t.mock.method(crypto, 'createHash', algorithm => {
    assert.equal(algorithm, 'sha256');
    return { update(bytes) { assert.equal(bytes.toString(), 'test'); return this; },
      digest(encoding) { assert.equal(encoding, 'hex'); return '11dcdd369a4a83eba8dbcffd3ac7cae9870db81570616bb4dcb82cf1f4308d3c'; } };
  });
  const source = await resolveSource(f.options);
  assert.equal(source.protocol, 'legacy');
  assert.equal(source.artifact_id, 456);
  assert.deepEqual(source.policy, policy);
  assert.equal(digest.mock.callCount(), 1);
  f.state.artifacts[0].created_at = '2026-09-08T12:09:00Z';
  const stale = await resolveSource(f.options);
  assert.equal(stale.protocol, 'diagnostic');
  assert.equal(digest.mock.callCount(), 1); // Reject stale evidence before digest lookup.
});

test('record preserves v1 policy and original exit code without consuming HTML or script', t => {
  const f = reportFixture(t);
  const publication = { ...f.publication, fail_on: 'p0', fail_on_needs_review: 'none',
    fail_on_incomplete: false, require_agent: true, injected: '<script>bad</script>' };
  fs.writeFileSync(path.join(f.directory, 'publication.json'), JSON.stringify(publication));
  fs.writeFileSync(path.join(f.directory, 'review.html'), '<script>throw new Error("Do not execute")</script>');
  const record = createRecord(f.source, f.directory);
  assert.deepEqual(record.report, f.report);
  assert.equal(record.publication.exit_code, 1);
  assert.deepEqual(record.policy, { fail_on: 'p0', fail_on_needs_review: 'none', fail_on_incomplete: false, require_agent: true });
  assert.equal(Object.hasOwn(record, 'artifact_id'), false);
  assert.equal(Object.hasOwn(record.publication, 'injected'), false);
  assert.equal(record.summary, '');
});

test('legacy record never fabricates the missing publication manifest', t => {
  const f = reportFixture(t, 'legacy');
  // A legacy artifact cannot override policy through an unexpected manifest.
  fs.writeFileSync(path.join(f.directory, 'publication.json'), JSON.stringify({ ...f.publication, fail_on: 'none' }));
  const record = createRecord(f.source, f.directory);
  assert.equal(record.protocol, 'legacy');
  assert.deepEqual(record.policy, policy);
  assert.deepEqual(record.report, f.report);
  assert.equal(record.publication, null);
});

test('created records match the common archive schema after the trusted renderer assigns status', t => {
  const { validateRecord } = require('./report-pages-common.cjs');
  for (const protocol of ['v1', 'legacy', 'diagnostic']) {
    const f = reportFixture(t, protocol);
    const record = createRecord(f.source, f.directory);
    record.status = protocol === 'diagnostic' ? 'incomplete' : 'blocked';
    assert.equal(validateRecord(record), record);
  }
});

test('diagnostic sources never read evidence directories', t => {
  const f = reportFixture(t, 'diagnostic');
  f.source.diagnostic = 'The review report artifact is missing.';
  const record = createRecord(f.source, '/path/which/does/not/exist');
  assert.equal(record.protocol, 'diagnostic');
  assert.equal(record.report, null);
  assert.equal(record.publication, null);
  assert.equal(record.diagnostic, f.source.diagnostic);
});

test('record refuses missing, forged, invalid and false-validity manifests', async t => {
  const invalid = [
    ['missing manifest', f => { fs.unlinkSync(path.join(f.directory, 'publication.json')); }],
    ['invalid JSON', f => { fs.writeFileSync(path.join(f.directory, 'publication.json'), 'attacker-payload'); }],
    ['wrong schema', f => { f.publication.schema_version = 2; }],
    ['stale Base', f => { f.publication.base = future; }],
    ['stale Head', f => { f.publication.head = future; }],
    ['invalid evidence marker', f => { f.publication.report_valid = false; }],
    ['string evidence marker', f => { f.publication.report_valid = 'true'; }],
    ['invalid gate status', f => { f.publication.exit_code = 2; }],
    ['invalid priority', f => { f.publication.fail_on = '--help'; }],
    ['invalid unresolved priority', f => { f.publication.fail_on_needs_review = 'P0'; }],
    ['string incomplete flag', f => { f.publication.fail_on_incomplete = 'false'; }],
    ['string model flag', f => { f.publication.require_agent = 'false'; }],
  ];
  for (const [label, mutate] of invalid) {
    await t.test(label, child => {
      const f = reportFixture(child);
      const original = JSON.stringify(f.publication);
      mutate(f);
      if (JSON.stringify(f.publication) !== original) fs.writeFileSync(path.join(f.directory, 'publication.json'), JSON.stringify(f.publication));
      const record = createRecord(f.source, f.directory);
      assert.equal(record.protocol, 'diagnostic');
      assert.equal(record.report, null);
      assert.equal(record.publication, null);
      assert.doesNotMatch(record.diagnostic, /attacker-payload|--help/);
    });
  }
});

test('record rejects symlinks, oversized files, invalid identities and non-JSON evidence', async t => {
  for (const name of ['review.json', 'publication.json']) {
    await t.test(`${name} symlink`, child => {
      const f = reportFixture(child);
      const saved = path.join(f.directory, 'saved');
      fs.renameSync(path.join(f.directory, name), saved);
      fs.symlinkSync(saved, path.join(f.directory, name));
      assert.equal(createRecord(f.source, f.directory).protocol, 'diagnostic');
    });
    await t.test(`${name} oversized`, child => {
      const f = reportFixture(child);
      fs.truncateSync(path.join(f.directory, name), name === 'review.json' ? 16 * 1024 * 1024 + 1 : 8193);
      assert.equal(createRecord(f.source, f.directory).protocol, 'diagnostic');
    });
  }
  await t.test('directory symlink', child => {
    const f = reportFixture(child);
    const linked = path.join(directoryFixture(child), 'link');
    fs.symlinkSync(f.directory, linked);
    assert.equal(createRecord(f.source, linked).protocol, 'diagnostic');
  });
  await t.test('invalid UTF-8 is not silently replaced', child => {
    const f = reportFixture(child);
    const bytes = Buffer.from(JSON.stringify({ ...f.report, untrusted: 'marker' }));
    bytes[bytes.indexOf('marker')] = 0xff;
    fs.writeFileSync(path.join(f.directory, 'review.json'), bytes);
    assert.equal(createRecord(f.source, f.directory).protocol, 'diagnostic');
  });
  for (const body of ['', '<script>attacker</script>', '[]', 'null', JSON.stringify({ comparison: { base_commit: base, head_commit: future } })]) {
    await t.test(`invalid report ${body.slice(0, 12)}`, child => {
      const f = reportFixture(child);
      fs.writeFileSync(path.join(f.directory, 'review.json'), body);
      assert.equal(createRecord(f.source, f.directory).protocol, 'diagnostic');
    });
  }
});
