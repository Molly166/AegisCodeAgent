'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

function lintFixture(t, { shellcheck = true, migration = false } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'aegis-workflow-lint-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  for (const dir of ['scripts', '.github/workflows', 'bin']) {
    fs.mkdirSync(path.join(root, dir), { recursive: true });
  }
  const script = path.join(root, 'scripts/check-workflows.sh');
  fs.copyFileSync(path.join(__dirname, 'check-workflows.sh'), script);
  fs.writeFileSync(path.join(root, 'scripts/other.sh'), '#!/usr/bin/env bash\ntrue\n');
  fs.writeFileSync(path.join(root, '.github/workflows/ci.yml'), 'name: CI\n');
  fs.writeFileSync(path.join(root, '.github/workflows/other.yaml'), 'name: Other\n');
  if (migration) {
    fs.mkdirSync(path.join(root, 'examples'));
    fs.writeFileSync(path.join(root, 'examples/aegis-review-v1-migration.yml'), 'name: Staged review\n');
  }
  // A closed PATH makes the missing-ShellCheck test independent of the host.
  fs.symlinkSync('/usr/bin/dirname', path.join(root, 'bin/dirname'));
  fs.writeFileSync(path.join(root, 'bin/go'), '#!/bin/bash\nprintf "%s\\n" "$@" >> "$LINT_GO_ARGS"\nexit "${FAKE_GO_EXIT:-0}"\n', { mode: 0o700 });
  if (shellcheck) {
    fs.writeFileSync(path.join(root, 'bin/shellcheck'), '#!/bin/bash\nprintf "%s\\n" "$@" >> "$LINT_SHELL_ARGS"\nexit "${FAKE_SHELLCHECK_EXIT:-0}"\n', { mode: 0o700 });
  }
  const env = { ...process.env, PATH: path.join(root, 'bin'),
    LINT_GO_ARGS: path.join(root, 'go-args'), LINT_SHELL_ARGS: path.join(root, 'shell-args') };
  return { root, env, run: extra => spawnSync('/bin/bash', [script], {
    cwd: os.tmpdir(), env: { ...env, ...extra }, encoding: 'utf8',
  }) };
}

test('workflow lint fails explicitly when ShellCheck is unavailable', t => {
  const fixture = lintFixture(t, { shellcheck: false });
  const result = fixture.run();
  assert.equal(result.status, 2);
  assert.match(result.stderr, /ShellCheck is required/);
  assert.equal(fs.existsSync(fixture.env.LINT_GO_ARGS), false);
});

test('workflow lint checks active workflows, staged migration and every shell script', t => {
  const fixture = lintFixture(t, { migration: true });
  const result = fixture.run();
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(fs.readFileSync(fixture.env.LINT_GO_ARGS, 'utf8').trim().split('\n'), [
    'run', 'github.com/rhysd/actionlint/cmd/actionlint@v1.7.12',
    '-shellcheck', path.join(fixture.root, 'bin/shellcheck'),
    '.github/workflows/ci.yml', '.github/workflows/other.yaml', 'examples/aegis-review-v1-migration.yml',
  ]);
  assert.deepEqual(fs.readFileSync(fixture.env.LINT_SHELL_ARGS, 'utf8').trim().split('\n'), [
    '--shell=bash', 'scripts/check-workflows.sh', 'scripts/other.sh',
  ]);
});

test('workflow lint still works after the migration example is removed', t => {
  const fixture = lintFixture(t);
  const result = fixture.run();
  assert.equal(result.status, 0, result.stderr);
  assert.doesNotMatch(fs.readFileSync(fixture.env.LINT_GO_ARGS, 'utf8'), /examples/);
});

test('workflow lint propagates both actionlint and standalone ShellCheck failures', t => {
  const actionlint = lintFixture(t);
  assert.equal(actionlint.run({ FAKE_GO_EXIT: '7' }).status, 7);
  assert.equal(fs.existsSync(actionlint.env.LINT_SHELL_ARGS), false);
  const shellcheck = lintFixture(t);
  assert.equal(shellcheck.run({ FAKE_SHELLCHECK_EXIT: '8' }).status, 8);
});

for (const [workflow, env, expected] of [
  ['publish-report-pages.yml', { AEGIS_PAGE_URL: 'https://example.invalid/report/', AEGIS_HEAD: '$(printf NOT_EXECUTED)' },
    '[Open the public HTML report](https://example.invalid/report/) — commit `$(printf NOT_EXECUTED)`.'],
  ['release.yml', { AEGIS_RELEASE_TAG: 'v1.0.0-$(printf NOT_EXECUTED)' },
    'Release candidates for `v1.0.0-$(printf NOT_EXECUTED)` were built and checked.'],
]) {
  test(`${workflow} renders literal Markdown code quotes without evaluating values`, t => {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), 'aegis-summary-lint-'));
    t.after(() => fs.rmSync(root, { recursive: true, force: true }));
    const yaml = fs.readFileSync(path.join(__dirname, '../.github/workflows', workflow), 'utf8');
    const command = yaml.split('\n').find(line => line.includes('printf ') && line.includes('GITHUB_STEP_SUMMARY'));
    assert.ok(command, 'summary command must exist');
    const summary = path.join(root, 'summary');
    const result = spawnSync('/bin/bash', ['-euo', 'pipefail', '-c', command.trim()], {
      env: { ...process.env, ...env, GITHUB_STEP_SUMMARY: summary }, encoding: 'utf8',
    });
    assert.equal(result.status, 0, result.stderr);
    assert.ok(fs.readFileSync(summary, 'utf8').startsWith(expected));
  });
}
