'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const { createHash } = require('node:crypto');
const { saveRecord, loadArchive } = require('./report-pages-archive.cjs');

const repo = { owner: 'owner', repo: 'target' };
const repository = 'owner/target';
const branch = 'aegis-report-history';
const markerPath = '.aegis-report-history.json';
const marker = { schema_version: 1, repository, kind: 'aegis-public-report-history' };

function record(run = 101, overrides = {}) {
  return {
    schema_version: 1, repository, pr_number: 7,
    base: 'a'.repeat(40), head: 'b'.repeat(40), run_id: run, run_attempt: 1,
    run_url: `https://github.com/${repository}/actions/runs/${run}`, artifact_url: '',
    created_at: '2026-09-09T10:00:00Z', conclusion: 'failure', protocol: 'diagnostic',
    diagnostic: 'Review evidence is unavailable.',
    policy: { fail_on: 'p1', fail_on_needs_review: 'p0', fail_on_incomplete: true, require_agent: false },
    report: null, publication: null, summary: 'Review incomplete.', status: 'incomplete', ...overrides,
  };
}

function recordFile(value) {
  return `records/pr-${value.pr_number}/${value.head}/${value.run_id}-${value.run_attempt}.json`;
}

function statusError(status) {
  return Object.assign(new Error(`GitHub status ${status}`), { status });
}

function fakeGit() {
  const blobs = new Map(), trees = new Map(), commits = new Map();
  const calls = [];
  let tip = null, sequence = 0;
  const hash = value => createHash('sha1').update(value).digest('hex');
  const state = { calls, blobs, trees, commits, beforeRefWrite: null, refConflicts: 0, mutateTree: null, mutateBlob: null };
  function addBlob(content) {
    const bytes = Buffer.isBuffer(content) ? Buffer.from(content) : Buffer.from(content, 'utf8');
    const sha = hash(Buffer.concat([Buffer.from(`blob ${bytes.length}\0`), bytes]));
    blobs.set(sha, bytes);
    return sha;
  }
  function addTree(entries) {
    const sha = hash(JSON.stringify([...entries.entries()].sort()));
    trees.set(sha, new Map(entries));
    return sha;
  }
  function addCommit(tree, parents) {
    const sha = hash(JSON.stringify({ tree, parents, sequence: sequence++ }));
    commits.set(sha, { sha, tree: { sha: tree }, parents: [...parents] });
    return sha;
  }
  function reference() {
    return { ref: `refs/heads/${branch}`, object: { type: 'commit', sha: tip } };
  }
  function entriesFor(treeSHA) {
    const files = trees.get(treeSHA);
    if (!files) throw statusError(404);
    const directories = new Map();
    for (const name of files.keys()) {
      const parts = name.split('/');
      for (let count = 1; count < parts.length; count++) {
        const path = parts.slice(0, count).join('/');
        directories.set(path, { path, type: 'tree', mode: '040000', sha: hash(path) });
      }
    }
    return [...directories.values(), ...[...files.entries()].map(([path, entry]) => ({ path, ...entry }))];
  }
  async function refRace(args) {
    if (state.beforeRefWrite) {
      const hook = state.beforeRefWrite;
      state.beforeRefWrite = null;
      await hook(args);
    }
    if (state.refConflicts > 0) {
      state.refConflicts--;
      throw statusError(409);
    }
  }
  function checked(name, fn) {
    return async args => {
      assert.equal(args.owner, repo.owner);
      assert.equal(args.repo, repo.repo);
      calls.push({ name, ...args });
      return fn(args);
    };
  }
  state.github = { rest: { git: {
    getRef: checked('getRef', async args => {
      assert.equal(args.ref, `heads/${branch}`);
      if (!tip) throw statusError(404);
      return { data: reference() };
    }),
    getCommit: checked('getCommit', async args => {
      const commit = commits.get(args.commit_sha);
      if (!commit) throw statusError(404);
      return { data: commit };
    }),
    getTree: checked('getTree', async args => {
      assert.equal(args.recursive, 'true');
      let data = { sha: args.tree_sha, truncated: false, tree: entriesFor(args.tree_sha) };
      if (state.mutateTree) data = state.mutateTree(data);
      return { data };
    }),
    getBlob: checked('getBlob', async args => {
      const bytes = blobs.get(args.file_sha);
      if (!bytes) throw statusError(404);
      let data = { sha: args.file_sha, encoding: 'base64', size: bytes.length, content: bytes.toString('base64') };
      if (state.mutateBlob) data = state.mutateBlob(data);
      return { data };
    }),
    createBlob: checked('createBlob', async args => {
      assert.equal(args.encoding, 'utf-8');
      return { data: { sha: addBlob(args.content) } };
    }),
    createTree: checked('createTree', async args => {
      const entries = new Map(args.base_tree ? trees.get(args.base_tree) : []);
      for (const entry of args.tree) {
        assert.ok(entry.sha, 'archive must never use null-SHA deletions');
        assert.equal(entry.mode, '100644');
        assert.equal(entry.type, 'blob');
        const bytes = blobs.get(entry.sha);
        entries.set(entry.path, { type: entry.type, mode: entry.mode, sha: entry.sha, size: bytes.length });
      }
      return { data: { sha: addTree(entries) } };
    }),
    createCommit: checked('createCommit', async args => ({ data: { sha: addCommit(args.tree, args.parents) } })),
    updateRef: checked('updateRef', async args => {
      assert.equal(args.ref, `heads/${branch}`);
      assert.equal(args.force, false, 'archive must never force push');
      await refRace(args);
      const commit = commits.get(args.sha);
      if (tip !== args.sha && !commit?.parents.includes(tip)) throw statusError(422);
      tip = args.sha;
      return { data: reference() };
    }),
    createRef: checked('createRef', async args => {
      assert.equal(args.ref, `refs/heads/${branch}`);
      await refRace(args);
      if (tip) throw statusError(422);
      tip = args.sha;
      return { data: reference() };
    }),
  } } };
  state.seed = ({ records = [], extras = [], archiveMarker = marker } = {}) => {
    const entries = new Map();
    const add = (path, content, mode = '100644', type = 'blob') => {
      const sha = addBlob(content);
      entries.set(path, { sha, mode, type, size: blobs.get(sha).length });
    };
    if (archiveMarker !== null) add(markerPath, JSON.stringify(archiveMarker));
    for (const value of records) add(recordFile(value), JSON.stringify(value));
    for (const extra of extras) add(extra.path, extra.content, extra.mode, extra.type);
    tip = addCommit(addTree(entries), []);
    return tip;
  };
  state.tip = () => tip;
  state.writes = () => calls.filter(call => ['createBlob', 'createTree', 'createCommit', 'createRef', 'updateRef'].includes(call.name));
  return state;
}

test('absent dedicated branch loads as an empty archive without writes', async () => {
  const fake = fakeGit();
  assert.deepEqual(await loadArchive({ github: fake.github, repo }), { records: [], branchSha: null, treeSha: null, totalBytes: 0 });
  assert.equal(fake.writes().length, 0);
});

test('first save creates only marker and immutable JSON then reloads the record', async () => {
  const fake = fakeGit();
  const saved = await saveRecord({ github: fake.github, repo, record: record() });
  assert.equal(saved.created, true);
  assert.equal(saved.path, recordFile(record()));
  const loaded = await loadArchive({ github: fake.github, repo });
  assert.deepEqual(loaded.records, [record()]);
  assert.equal(loaded.branchSha, saved.branchSha);
  assert.ok(loaded.totalBytes > 0);
  const commit = fake.commits.get(saved.branchSha);
  assert.deepEqual([...fake.trees.get(commit.tree.sha).keys()].sort(), [markerPath, recordFile(record())].sort());
  assert.deepEqual(commit.parents, []);
});

test('canonical JSON makes reordered nested keys idempotent without creating objects', async () => {
  const fake = fakeGit();
  const first = record();
  await saveRecord({ github: fake.github, repo, record: first });
  const initialWrites = fake.writes().length;
  const reordered = Object.fromEntries(Object.entries(first).reverse());
  reordered.policy = Object.fromEntries(Object.entries(first.policy).reverse());
  const result = await saveRecord({ github: fake.github, repo, record: reordered });
  assert.equal(result.created, false);
  assert.equal(fake.writes().length, initialWrites);
});

test('same identity with different content never overwrites a historical record', async () => {
  const fake = fakeGit();
  fake.seed({ records: [record()] });
  const tip = fake.tip();
  await assert.rejects(saveRecord({ github: fake.github, repo, record: record(101, { summary: 'Changed historical conclusion.' }) }), /immutable/);
  assert.equal(fake.tip(), tip);
  assert.equal(fake.writes().length, 0);
});

test('existing-branch concurrent appends retry against the winning tip without losing either report', async () => {
  const fake = fakeGit();
  fake.seed({ records: [record(100)] });
  fake.beforeRefWrite = async () => saveRecord({ github: fake.github, repo, record: record(102) });
  await saveRecord({ github: fake.github, repo, record: record(101) });
  const loaded = await loadArchive({ github: fake.github, repo });
  assert.deepEqual(loaded.records.map(value => value.run_id).sort(), [100, 101, 102]);
  assert.equal(fake.calls.filter(call => call.name === 'updateRef').length, 3);
});

test('concurrent first branch creation retries instead of replacing the other creator', async () => {
  const fake = fakeGit();
  fake.beforeRefWrite = async () => saveRecord({ github: fake.github, repo, record: record(102) });
  await saveRecord({ github: fake.github, repo, record: record(101) });
  const loaded = await loadArchive({ github: fake.github, repo });
  assert.deepEqual(loaded.records.map(value => value.run_id).sort(), [101, 102]);
  assert.equal(fake.calls.filter(call => call.name === 'createRef').length, 2);
  assert.equal(fake.calls.filter(call => call.name === 'updateRef').length, 1);
});

test('concurrent identical records converge idempotently', async () => {
  const fake = fakeGit();
  fake.beforeRefWrite = async () => saveRecord({ github: fake.github, repo, record: record() });
  const result = await saveRecord({ github: fake.github, repo, record: record() });
  assert.equal(result.created, false);
  assert.equal((await loadArchive({ github: fake.github, repo })).records.length, 1);
});

test('persistent CAS conflicts stop after four attempts without changing history', async () => {
  const fake = fakeGit();
  fake.seed({ records: [record(100)] });
  const tip = fake.tip();
  fake.refConflicts = 10;
  await assert.rejects(saveRecord({ github: fake.github, repo, record: record() }), /four attempts/);
  assert.equal(fake.calls.filter(call => call.name === 'updateRef').length, 4);
  assert.equal(fake.tip(), tip);
});

test('authorization errors are not retried as CAS conflicts', async () => {
  const fake = fakeGit();
  fake.seed();
  fake.beforeRefWrite = async () => { throw statusError(403); };
  await assert.rejects(saveRecord({ github: fake.github, repo, record: record() }), error => error.status === 403);
  assert.equal(fake.calls.filter(call => call.name === 'updateRef').length, 1);
});

test('pre-existing branch without exact dedicated marker is never adopted', async () => {
  for (const archiveMarker of [null, { ...marker, repository: 'owner/other' }, { ...marker, kind: 'ordinary-pages' }, { ...marker, extra: true }]) {
    const fake = fakeGit();
    fake.seed({ archiveMarker });
    const tip = fake.tip();
    await assert.rejects(saveRecord({ github: fake.github, repo, record: record() }), /archive|marker/);
    assert.equal(fake.tip(), tip);
    assert.equal(fake.writes().length, 0);
  }
});

test('archive rejects unknown files, HTML, executables, symlinks, submodules and path traversal', async () => {
  for (const extra of [
    { path: 'index.html', content: '<script>untrusted()</script>' },
    { path: 'records/payload.js', content: 'process.exit(0)' },
    { path: 'records/../evil.json', content: '{}' },
    { path: 'records/pr-07/' + 'b'.repeat(40) + '/101-1.json', content: '{}' },
    { path: recordFile(record()), content: JSON.stringify(record()), mode: '100755' },
    { path: recordFile(record()), content: '../secret', mode: '120000' },
    { path: recordFile(record()), content: '{}', mode: '160000', type: 'commit' },
  ]) {
    const fake = fakeGit();
    fake.seed({ extras: [extra] });
    await assert.rejects(loadArchive({ github: fake.github, repo }), /archive|Archive|Unknown/);
    assert.equal(fake.writes().length, 0);
  }
});

test('archive rejects records whose own identity differs from the immutable path', async () => {
  for (const mutated of [record(102), record(101, { pr_number: 8 }), record(101, { head: 'c'.repeat(40) })]) {
    const fake = fakeGit();
    fake.seed({ extras: [{ path: recordFile(record()), content: JSON.stringify(mutated) }] });
    await assert.rejects(loadArchive({ github: fake.github, repo }), /identity|path/);
  }
});

test('record and URL identities remain confined to the caller repository', async () => {
  for (const mutated of [
    record(101, { repository: 'owner/other', run_url: 'https://github.com/owner/other/actions/runs/101' }),
    record(101, { run_url: 'https://evil.example/actions/runs/101' }),
    record(101, { artifact_url: 'https://github.com/owner/other/actions/runs/101/artifacts/1' }),
  ]) {
    const fake = fakeGit();
    await assert.rejects(saveRecord({ github: fake.github, repo, record: mutated }), /repository|URL|identity/);
    assert.equal(fake.writes().length, 0);
  }
});

test('truncated trees and duplicated paths are rejected before reading JSON blobs', async () => {
  for (const mutate of [
    data => ({ ...data, truncated: true }),
    data => ({ ...data, tree: [...data.tree, data.tree.find(entry => entry.path === markerPath)] }),
  ]) {
    const fake = fakeGit();
    fake.seed({ records: [record()] });
    fake.mutateTree = mutate;
    await assert.rejects(loadArchive({ github: fake.github, repo }), /Truncated|duplicate/);
    assert.equal(fake.calls.filter(call => call.name === 'getBlob').length, 0);
  }
});

test('oversized tree entries and total archive size fail before downloading blobs', async () => {
  for (const limit of ['record', 'total']) {
    const fake = fakeGit();
    fake.seed({ records: [record(101), record(102), record(103), record(104)] });
    fake.mutateTree = data => ({ ...data, tree: data.tree.map(entry => entry.path.startsWith('records/') && entry.type === 'blob' ?
      { ...entry, size: limit === 'record' ? 17 * 1024 * 1024 + 1 : 17 * 1024 * 1024 } : entry) });
    await assert.rejects(loadArchive({ github: fake.github, repo }), /17 MiB|64 MiB/);
    assert.equal(fake.calls.filter(call => call.name === 'getBlob').length, 0);
  }
});

test('full archive refuses another record without deleting history but remains idempotent', async () => {
  const fake = fakeGit();
  const records = Array.from({ length: 200 }, (_, index) => record(index + 1));
  fake.seed({ records });
  const tip = fake.tip();
  await assert.rejects(saveRecord({ github: fake.github, repo, record: record(201) }), /capacity/);
  assert.equal(fake.tip(), tip);
  assert.equal(fake.writes().length, 0);
  assert.equal((await saveRecord({ github: fake.github, repo, record: records[0] })).created, false);
  assert.equal((await loadArchive({ github: fake.github, repo })).records.length, 200);
});

test('over-capacity archive is rejected rather than silently pruning records', async () => {
  const fake = fakeGit();
  fake.seed({ records: Array.from({ length: 201 }, (_, index) => record(index + 1)) });
  await assert.rejects(loadArchive({ github: fake.github, repo }), /200-record/);
  assert.equal(fake.writes().length, 0);
});

test('new oversized record is rejected before any API access', async () => {
  const fake = fakeGit();
  const oversized = record(101, { report: { comparison: { base_commit: 'a'.repeat(40), head_commit: 'b'.repeat(40) }, data: 'x'.repeat(17 * 1024 * 1024) } });
  await assert.rejects(saveRecord({ github: fake.github, repo, record: oversized }), /17 MiB/);
  assert.equal(fake.calls.length, 0);
});

test('invalid UTF-8, malformed JSON, inconsistent blob size and invalid base64 fail closed', async () => {
  for (const content of [Buffer.from([0xff, 0xfe]), '{broken-json']) {
    const fake = fakeGit();
    fake.seed({ extras: [{ path: recordFile(record()), content }] });
    await assert.rejects(loadArchive({ github: fake.github, repo }), /UTF-8 JSON/);
  }
  for (const mutate of [
    data => ({ ...data, size: data.size + 1 }),
    data => ({ ...data, content: '%not-base64%' }),
    data => ({ ...data, encoding: 'utf-8' }),
  ]) {
    const fake = fakeGit();
    fake.seed({ records: [record()] });
    fake.mutateBlob = mutate;
    await assert.rejects(loadArchive({ github: fake.github, repo }), /blob|encoding/);
  }
});

test('cyclic values and executable toJSON objects cannot enter the JSON archive', async () => {
  const fake = fakeGit();
  const cyclic = record();
  cyclic.extra = cyclic;
  await assert.rejects(saveRecord({ github: fake.github, repo, record: cyclic }), /acyclic/);
  let invoked = false;
  const method = record();
  method.toJSON = () => { invoked = true; return {}; };
  await assert.rejects(saveRecord({ github: fake.github, repo, record: method }), /JSON/);
  assert.equal(invoked, false);
  assert.equal(fake.calls.length, 0);
});
