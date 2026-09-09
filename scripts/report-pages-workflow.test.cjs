'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createHash } = require('node:crypto');

const root = path.join(__dirname, '..');
const yaml = fs.readFileSync(path.join(root, '.github/workflows/publish-report-pages.yml'), 'utf8');
const header = yaml.slice(0, yaml.indexOf('\njobs:\n'));
const jobsText = yaml.slice(yaml.indexOf('\njobs:\n') + '\njobs:\n'.length);
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;

function job(name) {
  const start = jobsText.indexOf(`  ${name}:\n`);
  assert.notEqual(start, -1, `Missing ${name} job`);
  const rest = jobsText.slice(start);
  const next = rest.slice(1).search(/^  [a-zA-Z][a-zA-Z0-9_-]*:\n/m);
  return next === -1 ? rest : rest.slice(0, next + 1);
}

function steps(value) {
  return [...value.matchAll(/^      - [\s\S]*?(?=^      - |$(?![\s\S]))/gm)].map(match => match[0]);
}

function namedStep(jobName, name) {
  const found = steps(job(jobName)).find(step => step.startsWith(`      - name: ${name}\n`));
  assert.ok(found, `Missing step ${jobName}/${name}`);
  return found;
}

function inline(jobName, name) {
  const step = namedStep(jobName, name);
  const match = step.match(/          script: \|\n((?:            [^\n]*(?:\n|$)|\n)+)/);
  assert.ok(match, 'Missing inline script');
  return new AsyncFunction('require', 'process', 'github', 'context', 'core',
    match[1].split('\n').map(line => line.slice(12)).join('\n'));
}

function permissions(jobName) {
  const match = job(jobName).match(/^    permissions:\n((?:      [^\n]+\n)+)/m);
  assert.ok(match, `Missing permissions for ${jobName}`);
  return Object.fromEntries(match[1].trim().split('\n').map(line => line.trim().split(/:\s*/)));
}

function fakeFS(initial = {}) {
  const files = new Map(Object.entries(initial));
  return {
    files,
    readFileSync: name => {
      if (!files.has(name)) throw new Error('Unexpected file read: ' + name);
      return files.get(name);
    },
    mkdirSync: () => {},
    writeFileSync: (name, content, options) => {
      assert.equal(options.flag, 'wx');
      assert.equal(files.has(name), false, 'Data handoff must not overwrite an existing file');
      files.set(name, content);
    },
  };
}

test('public publishing requires completed reviews or explicit dispatch approval on the default branch', () => {
  assert.match(header, /workflow_run:\n    workflows: \[Aegis Code Review\]\n    types: \[completed\]/);
  assert.match(header, /workflow_dispatch:/);
  assert.match(header, /confirm-public:[\s\S]*?default: false[\s\S]*?type: boolean/);
  assert.doesNotMatch(header, /^  (?:push|pull_request|pull_request_target):/m);
  assert.match(header, /^permissions: \{\}$/m);
  const prepare = job('prepare');
  assert.match(prepare, /github\.event\.repository\.private == false/);
  assert.match(prepare, /github\.ref == format\('refs\/heads\/\{0\}', github\.event\.repository\.default_branch\)/);
  assert.match(prepare, /github\.event_name == 'workflow_run' && vars\.AEGIS_PUBLIC_REPORTS == 'true'/);
  assert.match(prepare, /github\.event_name == 'workflow_dispatch' && inputs\.confirm-public/);
  assert.doesNotMatch(prepare, /workflow_run\.conclusion == 'success'/, 'failed reviews must also be publishable as blocked/diagnostic evidence');
});

test('every job checks out only the immutable trusted default-branch revision', () => {
  let checkoutCount = 0;
  for (const name of ['prepare', 'archive', 'deploy', 'feedback']) {
    const checkouts = steps(job(name)).filter(step => /uses: actions\/checkout@/.test(step));
    assert.equal(checkouts.length, 1, name);
    for (const checkout of checkouts) {
      checkoutCount++;
      assert.match(checkout, /ref: \$\{\{ github\.sha \}\}/);
      assert.match(checkout, /path: publisher/);
      assert.match(checkout, /persist-credentials: false/);
      assert.doesNotMatch(checkout, /head_sha|pull_request|repository:|refs\/pull\//);
    }
  }
  assert.equal(checkoutCount, 4);
  for (const action of yaml.matchAll(/\buses:\s+([^\s#]+)/g)) {
    assert.match(action[1], /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+@[0-9a-f]{40}$/, 'Actions must remain pinned to reviewed SHAs');
  }
});

test('permissions separate artifact parsing, history append, Pages deployment and PR feedback', () => {
  assert.deepEqual(permissions('prepare'), { contents: 'read', actions: 'read', 'pull-requests': 'read' });
  assert.deepEqual(permissions('archive'), { contents: 'write' });
  assert.deepEqual(permissions('deploy'), { contents: 'read', pages: 'write', 'id-token': 'write' });
  assert.deepEqual(permissions('feedback'), { contents: 'read', pages: 'read', actions: 'read', 'pull-requests': 'write' });
  assert.doesNotMatch(yaml, /DEEPSEEK_API_KEY|ORCAROUTER_API_KEY|agent-provider|checks:\s*write|statuses:\s*write/);
});

test('archive completes outside concurrency before deployment reloads every historical JSON record', () => {
  assert.doesNotMatch(header, /^concurrency:/m, 'workflow-level cancellation could drop unarchived reviews');
  assert.doesNotMatch(job('prepare'), /^    concurrency:/m);
  assert.doesNotMatch(job('archive'), /^    concurrency:/m);
  assert.match(job('archive'), /^    needs: prepare$/m);
  assert.match(job('archive'), /await saveRecord\(\{ github, repo: context\.repo, record \}\)/);
  const deploy = job('deploy');
  assert.match(deploy, /^    needs: \[prepare, archive\]$/m);
  assert.match(deploy, /concurrency:\n      group: aegis-public-report-pages\n      cancel-in-progress: false/);
  assert.match(deploy, /const \{ records \} = await loadArchive\(\{ github, repo: context\.repo \}\)/);
  assert.match(deploy, /if \(!records\.length\) throw new Error/);
  assert.ok(deploy.indexOf('await loadArchive(') < deploy.indexOf('buildSite(records,'));
  assert.ok(deploy.indexOf('buildSite(records,') < deploy.indexOf('uses: actions/upload-pages-artifact@'));
  assert.match(deploy, /environment:\n      name: github-pages/);
});

test('feedback consumes exactly the successfully deployed manifest, never a newer archive snapshot', () => {
  const deploy = job('deploy'), feedback = job('feedback');
  assert.match(deploy, /name: public-report-deployment-\$\{\{ github\.run_attempt \}\}/);
  assert.match(deploy, /path: deployment\/records\.json/);
  assert.match(deploy, /page-url: \$\{\{ steps\.deployment\.outputs\.page_url \}\}/);
  assert.match(feedback, /^    needs: deploy$/m);
  assert.doesNotMatch(feedback, /always\(\)|continue-on-error:\s*true|loadArchive\(/);
  assert.match(feedback, /name: public-report-deployment-\$\{\{ github\.run_attempt \}\}/);
  assert.match(feedback, /AEGIS_PAGE_URL: \$\{\{ needs\.deploy\.outputs\.page-url \}\}/);
  assert.match(feedback, /fs\.readFileSync\('deployment\/records\.json', 'utf8'\)/);
});

test('missing artifacts retain diagnostic fallback and publishing never changes Review gates', () => {
  const download = namedStep('prepare', 'Download only the resolved evidence artifact');
  assert.match(download, /if: \$\{\{ steps\.source\.outputs\.artifact-id != '0' \}\}/);
  assert.match(download, /continue-on-error: true/);
  assert.match(download, /artifact-ids: \$\{\{ steps\.source\.outputs\.artifact-id \}\}/);
  assert.match(download, /run-id: \$\{\{ steps\.source\.outputs\.run-id \}\}/);
  assert.match(download, /path: evidence/);
  const validate = namedStep('prepare', 'Validate JSON and original result (never execute artifact HTML or PR code)');
  assert.match(validate, /createRecord\(source, process\.env\.GITHUB_WORKSPACE \+ '\/evidence'\)/);
  assert.match(validate, /renderRecord\(input, \{ binary: process\.env\.RUNNER_TEMP \+ '\/aegis-renderer'/);
  assert.doesNotMatch(validate, /if:|continue-on-error:/);
  assert.doesNotMatch(yaml, /checks\.(?:create|update)|createCommitStatus|pulls\.merge|git\s+push|git\s+checkout|git\s+reset/);
  assert.doesNotMatch(yaml, /working-directory:\s*(?:target|evidence)|\b(?:go test|go run|npm|npx|python|bash\s+evidence)\b/);
  for (const step of steps(job('prepare')).filter(value => /uses: actions\/download-artifact@/.test(value))) {
    assert.doesNotMatch(step, /path:\s*(?:publisher|\.|\/)/);
  }
});

test('public evidence hosting preserves the reviewed legacy producer hash and existing merge workflow', () => {
  const source = fs.readFileSync(path.join(root, 'scripts/report-pages-source.cjs'), 'utf8');
  const legacy = source.match(/legacy:\s*'([0-9a-f]{64})'/)?.[1];
  const v1 = source.match(/v1:\s*'([0-9a-f]{64})'/)?.[1];
  assert.equal(legacy, '11dcdd369a4a83eba8dbcffd3ac7cae9870db81570616bb4dcb82cf1f4308d3c');
  assert.ok(v1);
  const activeHash = createHash('sha256').update(fs.readFileSync(path.join(root, '.github/workflows/aegis-review.yml'))).digest('hex');
  assert.equal(activeHash, fs.existsSync(path.join(root, 'examples/aegis-review-v1-migration.yml')) ? legacy : v1);
});

test('workflow source resolution passes trusted event metadata and never artifact or dispatch-supplied associations', async () => {
  const run = inline('prepare', 'Resolve exact review provenance and artifact');
  for (const eventName of ['workflow_run', 'workflow_dispatch']) {
    const files = fakeFS(), outputs = {};
    const eventRun = { id: 100, run_attempt: 2, trusted: true };
    let resolved;
    await run(name => {
      if (name === 'node:fs') return files;
      assert.equal(name, './publisher/scripts/report-pages-source.cjs');
      return { resolveSource: async args => {
        resolved = args;
        return { artifact_id: 42, run_id: args.runId, head: 'a'.repeat(40) };
      } };
    }, { env: { RUNNER_TEMP: '/runner-temp' } }, {}, {
      eventName, repo: { owner: 'owner', repo: 'repo' },
      payload: { workflow_run: eventRun, inputs: { 'run-id': '200', 'run-attempt': '3', 'pr-number': '9', eventRun: { untrusted: true } } },
    }, { setOutput: (key, value) => { outputs[key] = value; } });
    assert.equal(resolved.runId, eventName === 'workflow_run' ? 100 : '200');
    assert.equal(resolved.attempt, eventName === 'workflow_run' ? 2 : '3');
    assert.equal(resolved.eventRun, eventName === 'workflow_run' ? eventRun : undefined);
    assert.equal(resolved.prNumber, eventName === 'workflow_run' ? undefined : '9');
    assert.equal(outputs['artifact-id'], 42);
    assert.ok(files.files.has('/runner-temp/public-source.json'));
  }
});

test('archive and deploy recheck live repository visibility before any persistence or rendering', async () => {
  for (const [jobName, stepName] of [
    ['archive', 'Append immutable JSON with compare-and-swap (no force push)'],
    ['deploy', 'Reload the entire archive after acquiring the deployment lock'],
  ]) {
    const run = inline(jobName, stepName);
    const files = fakeFS({ 'prepared/record.json': '{}' });
    let touched = false;
    const modules = {
      'node:fs': files,
      './publisher/scripts/report-pages-archive.cjs': {
        saveRecord: async () => { touched = true; }, loadArchive: async () => { touched = true; },
      },
      './publisher/scripts/report-pages-render.cjs': { buildSite: () => { touched = true; } },
    };
    await assert.rejects(run(name => modules[name], { env: {} }, {
      rest: { repos: { get: async () => ({ data: { private: true, visibility: 'private' } }) } },
    }, { repo: { owner: 'owner', repo: 'repo' } }, { notice: () => {} }), /remain public/);
    assert.equal(touched, false, jobName);
  }
});

test('deployment builds and records the full locked archive, not only the triggering report', async () => {
  const run = inline('deploy', 'Reload the entire archive after acquiring the deployment lock');
  const records = [{ run_id: 1 }, { run_id: 2 }, { run_id: 3 }], files = fakeFS();
  let rendered;
  const modules = {
    'node:fs': files,
    './publisher/scripts/report-pages-archive.cjs': { loadArchive: async () => ({ records }) },
    './publisher/scripts/report-pages-render.cjs': { buildSite: (values, options) => { rendered = { values, options }; } },
  };
  await run(name => modules[name], { env: { GITHUB_WORKSPACE: '/workspace', RUNNER_TEMP: '/runner-temp' } }, {
    rest: { repos: { get: async () => ({ data: { private: false, visibility: 'public' } }) } },
  }, { repo: { owner: 'owner', repo: 'repo' } }, { notice: () => {} });
  assert.equal(rendered.values, records);
  assert.equal(rendered.options.binary, '/runner-temp/aegis-renderer');
  assert.equal(rendered.options.directory, '/workspace/site');
  assert.deepEqual(JSON.parse(files.files.get('deployment/records.json')), records);
});

test('feedback receives only the deployment manifest and successful Pages URL', async () => {
  const run = inline('feedback', 'Refresh current PR links only after successful deployment');
  const records = [{ run_id: 1 }, { run_id: 2 }];
  const files = fakeFS({ 'deployment/records.json': JSON.stringify(records) });
  let received;
  await run(name => {
    if (name === 'node:fs') return files;
    assert.equal(name, './publisher/scripts/report-pages-feedback.cjs');
    return { refreshFeedback: async (_context, input) => { received = input; return { updated: 2 }; } };
  }, { env: { AEGIS_PAGE_URL: 'https://owner.github.io/repo/' } }, {}, { repo: { owner: 'owner', repo: 'repo' } }, { notice: () => {} });
  assert.deepEqual(received, { records, pageURL: 'https://owner.github.io/repo/' });
});
