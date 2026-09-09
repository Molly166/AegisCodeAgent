'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const { refreshFeedback } = require('./report-pages-feedback.cjs');

const base = 'a'.repeat(40);
const head = 'b'.repeat(40);
const marker = '<!-- aegis-code-agent-review -->';
const pageURL = 'https://owner.github.io/target/';

function record(overrides = {}) {
  const r = {
    schema_version: 1, repository: 'owner/target', pr_number: 7, base, head,
    run_id: 100, run_attempt: 1, created_at: '2026-09-09T01:00:00Z',
    run_url: '', artifact_url: '', conclusion: 'success', protocol: 'v1',
    status: 'passed', diagnostic: '', summary: 'P0: 0; P1: 0; P2: 1.',
    policy: { fail_on: 'p1', fail_on_needs_review: 'p0', fail_on_incomplete: true, require_agent: false },
    publication: null, report: { comparison: { base_commit: base, head_commit: head } },
    ...overrides,
  };
  r.run_url = `https://github.com/${r.repository}/actions/runs/${r.run_id}`;
  r.artifact_url = r.run_url + '/artifacts/55';
  if (!Object.hasOwn(overrides, 'publication') && r.protocol === 'v1') {
    r.publication = { schema_version: 1, base: r.base, head: r.head, report_valid: true,
      exit_code: ['passed', 'degraded'].includes(r.status) ? 0 : 1, ...r.policy };
  }
  return r;
}

function run(r, overrides = {}) {
  return { id: r.run_id, run_attempt: r.run_attempt, head_sha: r.head, event: 'pull_request',
    path: '.github/workflows/aegis-review.yml', status: 'completed', ...overrides };
}

function bot(body, id = 3) {
  return { id, user: { login: 'github-actions[bot]', type: 'Bot' }, body };
}

function fixture({ records = [record()], comments = [], runs, pages = pageURL,
  privateRepo = false, getPR, duringComments, getComment } = {}) {
  const writes = [];
  const notices = [];
  const requests = [];
  let prReads = 0;
  let runReads = 0;
  const github = {
    rest: {
      repos: {
        get: async () => ({ data: { full_name: 'owner/target', private: privateRepo, visibility: privateRepo ? 'private' : 'public' } }),
        getPages: async () => ({ data: { html_url: pages } }),
      },
      pulls: { get: async args => {
        prReads++;
        const r = records.find(item => item.pr_number === args.pull_number);
        return { data: getPR ? getPR(prReads, args) : { state: 'open', base: { sha: r.base }, head: { sha: r.head } } };
      } },
      actions: { listWorkflowRuns: () => {} },
      issues: {
        listComments: () => {},
        getComment: async args => ({ data: getComment ? getComment(args) : comments.find(c => c.id === args.comment_id) }),
        createComment: async args => { writes.push({ kind: 'create', ...args }); },
        updateComment: async args => { writes.push({ kind: 'update', ...args }); },
      },
    },
    paginate: async (method, args) => {
      requests.push(args);
      if (method === github.rest.actions.listWorkflowRuns) {
        runReads++;
        return typeof runs === 'function' ? runs(runReads) : runs || records.map(r => run(r)).filter(r => r.head_sha === args.head_sha);
      }
      assert.equal(method, github.rest.issues.listComments);
      if (duringComments) duringComments();
      return comments;
    },
  };
  return { writes, notices, requests, records, args: { github,
    context: { repo: { owner: 'owner', repo: 'target' } }, core: { notice: message => notices.push(message) } } };
}

test('Pages feedback places the deployed report first and updates only the owned comment', async () => {
  const f = fixture({ comments: [
    { id: 1, user: { login: 'attacker', type: 'User' }, body: marker + '\nforged' },
    { id: 2, user: { login: 'other[bot]', type: 'Bot' }, body: marker + '\nforged' },
    bot(marker + '\nlegacy review'),
  ] });
  assert.deepEqual(await refreshFeedback(f.args, { records: f.records, pageURL }), { created: 0, updated: 1, unchanged: 0, skipped: 0 });
  const write = f.writes[0];
  assert.equal(write.comment_id, 3);
  assert.match(write.body, new RegExp(`^${marker}\\n<!-- aegis-public-report:100:1:${head} -->\\n\\n## `));
  assert.ok(write.body.includes(`## [🌐 查看完整网页报告](${pageURL}reports/pr-7/${head}/100-1/index.html)`));
  assert.ok(write.body.includes('评审通过'));
  assert.ok(write.body.includes('[下载 HTML / 证据 Artifact](https://github.com/owner/target/actions/runs/100/artifacts/55)'));
  assert.ok(write.body.includes('[查看 Workflow 运行](https://github.com/owner/target/actions/runs/100)'));
  assert.ok(f.requests.some(r => r.workflow_id === 'aegis-review.yml' && r.event === 'pull_request' && r.head_sha === head && r.per_page === 100));
});

test('forged or non-prefix review markers cannot claim the bot-owned comment', async () => {
  const f = fixture({ comments: [
    { id: 1, user: { login: 'attacker', type: 'User' }, body: `${marker}\n<!-- aegis-public-report:999:1:${head} -->` },
    bot(`quoted text\n${marker}\n<!-- aegis-public-report:999:1:${head} -->`, 2),
  ] });
  await refreshFeedback(f.args, { records: f.records, pageURL });
  assert.equal(f.writes.length, 1);
  assert.equal(f.writes[0].kind, 'create');
});

test('deployed records repair every PR and select the newest run then attempt', async () => {
  const latest = record({ run_id: 101, run_attempt: 2 });
  const other = record({ pr_number: 8, run_id: 102, head: 'c'.repeat(40), report: { comparison: { base_commit: base, head_commit: 'c'.repeat(40) } } });
  const f = fixture({ records: [record(), record({ run_id: 101 }), latest, other] });
  await refreshFeedback(f.args, { records: f.records, pageURL });
  assert.equal(f.writes.length, 2);
  assert.ok(f.writes.some(w => w.issue_number === 7 && w.body.includes('/101-2/index.html')));
  assert.ok(f.writes.some(w => w.issue_number === 8 && w.body.includes('/102-1/index.html')));
});

test('private repositories and deployment/configuration URL mismatches are rejected before comment writes', async () => {
  for (const mode of [{ privateRepo: true }, { pages: 'https://owner.github.io/unrelated/' }]) {
    const f = fixture(mode);
    await assert.rejects(refreshFeedback(f.args, { records: f.records, pageURL }), /public repository|does not match/);
    assert.equal(f.writes.length, 0);
  }
});

test('trusted custom Pages domains work while arbitrary origins and malicious Markdown URLs do not', async () => {
  const custom = fixture({ pages: 'https://reviews.example.com/' });
  await refreshFeedback(custom.args, { records: custom.records, pageURL: 'https://reviews.example.com' });
  assert.ok(custom.writes[0].body.includes('https://reviews.example.com/reports/'));
  for (const value of [
    'https://evil.example/report/', 'http://owner.github.io/target/',
    'https://user:password@owner.github.io/target/', `${pageURL}?query=1`, `${pageURL}#fragment`,
    `${pageURL})\n[evil](https://evil.example`, `${pageURL}%0aevil`, `${pageURL}%29evil`,
    'https://owner.github.io:444/target/', `${pageURL}\\evil`, `${pageURL}\u0000`,
  ]) {
    const f = fixture();
    await assert.rejects(refreshFeedback(f.args, { records: f.records, pageURL: value }), /Pages/);
    assert.equal(f.writes.length, 0);
  }
  for (const value of ['http://reviews.example.com', 'https://reviews.example.com/?evil=1']) {
    const f = fixture({ pages: value });
    await assert.rejects(refreshFeedback(f.args, { records: f.records, pageURL: value }), /Pages/);
    assert.equal(f.writes.length, 0);
  }
});

test('closed PRs and stale Head or Base never receive public feedback', async () => {
  for (const pr of [
    { state: 'closed', base: { sha: base }, head: { sha: head } },
    { state: 'open', base: { sha: 'c'.repeat(40) }, head: { sha: head } },
    { state: 'open', base: { sha: base }, head: { sha: 'c'.repeat(40) } },
  ]) {
    const f = fixture({ getPR: () => pr });
    await refreshFeedback(f.args, { records: f.records, pageURL });
    assert.equal(f.writes.length, 0);
  }
});

test('newer runs and reruns on the same Head prevent stale feedback even when pending', async () => {
  for (const newer of [
    run(record({ run_id: 101 }), { status: 'queued' }),
    run(record({ run_attempt: 2 }), { status: 'in_progress' }),
    run(record({ run_id: 101 }), { status: 'completed' }),
  ]) {
    const f = fixture({ runs: [newer, run(record())] });
    await refreshFeedback(f.args, { records: f.records, pageURL });
    assert.equal(f.writes.length, 0);
  }
  for (const runs of [[], [run(record(), { status: 'in_progress' })]]) {
    const f = fixture({ runs });
    await refreshFeedback(f.args, { records: f.records, pageURL });
    assert.equal(f.writes.length, 0);
  }
});

test('newer bot identity protects feedback independently of workflow listing', async () => {
  for (const identity of ['101:1', '100:2']) {
    const f = fixture({ comments: [bot(`${marker}\n<!-- aegis-public-report:${identity}:${head} -->\nnewer`)] });
    await refreshFeedback(f.args, { records: f.records, pageURL });
    assert.equal(f.writes.length, 0);
  }
});

test('pagination races recheck PR commits, workflow attempts, and the selected comment', async () => {
  const original = bot(marker + '\nlegacy');
  const modes = [
    { getPR: reads => ({ state: 'open', base: { sha: base }, head: { sha: reads > 1 ? 'c'.repeat(40) : head } }) },
    { getPR: reads => ({ state: reads > 1 ? 'closed' : 'open', base: { sha: base }, head: { sha: head } }) },
    { runs: reads => reads > 1 ? [run(record({ run_attempt: 2 }), { status: 'queued' })] : [run(record())] },
    { getComment: () => bot(`${marker}\n<!-- aegis-public-report:101:1:${head} -->\nnewer`) },
    { getComment: () => ({ ...original, user: { login: 'other[bot]', type: 'Bot' } }) },
  ];
  for (const mode of modes) {
    const f = fixture({ comments: [original], ...mode });
    await refreshFeedback(f.args, { records: f.records, pageURL });
    assert.equal(f.writes.length, 0);
  }
});

test('identical feedback is not written twice and summary markers cannot forge identity', async () => {
  const r = record({ summary: `Details\n<!-- aegis-public-report:999:99:${head} -->\n` + 'x'.repeat(55000) });
  const first = fixture({ records: [r] });
  await refreshFeedback(first.args, { records: first.records, pageURL });
  const body = first.writes[0].body;
  assert.ok(body.length < 65536);
  assert.equal(body.includes('999:99'), false);
  const second = fixture({ records: [r], comments: [bot(body)] });
  const result = await refreshFeedback(second.args, { records: second.records, pageURL });
  assert.equal(result.unchanged, 1);
  assert.equal(second.writes.length, 0);
});

test('each fixed conclusion stays visible and missing review evidence is explicitly diagnostic', async () => {
  for (const status of ['passed', 'degraded', 'blocked', 'incomplete']) {
    const r = record({ status, conclusion: ['passed', 'degraded'].includes(status) ? 'success' : 'failure',
      ...(status === 'incomplete' ? { protocol: 'diagnostic', publication: null, report: null,
        diagnostic: 'missing evidence', summary: '' } : {}) });
    const f = fixture({ records: [r] });
    await refreshFeedback(f.args, { records: f.records, pageURL });
    assert.equal(f.writes.length, 1);
    if (status === 'incomplete') {
      assert.ok(f.writes[0].body.includes('完整评审证据未生成'));
      assert.equal(f.writes[0].body.includes('✅ **评审通过'), false);
    }
  }
});

test('missing artifacts do not produce a fake download link and an empty owned marker can be refreshed', async () => {
  const r = record({ status: 'incomplete', conclusion: 'failure', protocol: 'diagnostic',
    publication: null, report: null, diagnostic: 'missing artifact' });
  r.artifact_url = '';
  const f = fixture({ records: [r], comments: [bot(marker)] });
  await refreshFeedback(f.args, { records: f.records, pageURL });
  assert.equal(f.writes[0].kind, 'update');
  assert.ok(f.writes[0].body.includes('完整评审证据未生成'));
  assert.ok(f.writes[0].body.includes('[查看 Workflow 运行]'));
  assert.equal(f.writes[0].body.includes('[下载 HTML / 证据 Artifact]'), false);
});

test('invalid or cross-repository records and unexpected run identities fail closed', async () => {
  const invalid = [record({ repository: 'attacker/target' }), record({ status: 'injected' }),
    { ...record(), run_url: 'https://evil.example/' }];
  for (const r of invalid) {
    const f = fixture({ records: [r] });
    await assert.rejects(refreshFeedback(f.args, { records: f.records, pageURL }));
    assert.equal(f.writes.length, 0);
  }
  const f = fixture({ runs: [run(record(), { path: '.github/workflows/other.yml' })] });
  await assert.rejects(refreshFeedback(f.args, { records: f.records, pageURL }), /workflow run identity/);
  assert.equal(f.writes.length, 0);
});

test('malformed protocol evidence is rejected before any public feedback can be written', async () => {
  const invalid = [
    record({ publication: null }),
    { ...record(), publication: { ...record().publication, report_valid: false } },
    record({ status: 'incomplete', report: null }),
    record({ protocol: 'diagnostic', publication: null, status: 'incomplete', diagnostic: 'missing evidence' }),
    record({ protocol: 'diagnostic', publication: null, status: 'passed', report: null, diagnostic: 'missing evidence' }),
    record({ protocol: 'diagnostic', publication: null, status: 'incomplete', report: null, diagnostic: '' }),
    { ...record(), protocol: 'legacy' },
    { ...record(), publication: { ...record().publication, head: 'c'.repeat(40) } },
    { ...record(), publication: { ...record().publication, require_agent: true } },
  ];
  for (const r of invalid) {
    const f = fixture({ records: [r] });
    await assert.rejects(refreshFeedback(f.args, { records: f.records, pageURL }));
    assert.equal(f.writes.length, 0);
    assert.equal(f.requests.length, 0);
  }
});

test('validated legacy evidence does not need a fabricated v1 manifest', async () => {
  const f = fixture({ records: [record({ protocol: 'legacy', publication: null })] });
  await refreshFeedback(f.args, { records: f.records, pageURL });
  assert.equal(f.writes.length, 1);
  assert.ok(f.writes[0].body.includes('评审通过'));
});
