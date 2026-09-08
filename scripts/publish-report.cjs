'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

function readEvidence(file, base, head) {
  for (const value of [base, head]) {
    if (!/^[0-9a-f]{40}$/.test(value || '')) throw new Error('Invalid expected commit SHA');
  }
  const stat = fs.lstatSync(file);
  if (!stat.isFile() || stat.size > 16 * 1024 * 1024) throw new Error('Evidence must be a regular JSON file of at most 16 MiB');
  const bytes = fs.readFileSync(file);
  const report = JSON.parse(bytes);
  if (report.comparison?.base_commit !== base || report.comparison?.head_commit !== head) {
    throw new Error('Evidence commit identity does not match this PR run');
  }
  return bytes;
}

function executionCompatible(report, result, code) {
  if (result !== 'success') return false;
  if (code === '0') return true;
  // Review exit 1 can describe an OPTIONAL stage failure. Preserve the
  // publisher's configured policy when mandatory evidence remains complete.
  return code === '1' && report.analysis?.status === 'complete' &&
    report.verification?.status === 'complete' &&
    [report.agent?.status, report.context?.status].some(status => ['partial', 'failed'].includes(status));
}

function publish(env = process.env) {
  const workspace = env.GITHUB_WORKSPACE;
  if (!workspace || !path.isAbsolute(workspace)) throw new Error('Absolute workspace required');
  const output = path.join(workspace, 'artifacts');
  fs.mkdirSync(output, { recursive: true });
  const summary = path.join(output, 'review-summary.md');
  let exitCode = 1;
  let reportValid = false;
  try {
    const bytes = readEvidence(path.join(workspace, 'evidence/review.json'), env.AEGIS_BASE, env.AEGIS_HEAD);
    const report = JSON.parse(bytes);
    fs.writeFileSync(path.join(output, 'review.json'), bytes, { flag: 'wx' });
    const result = spawnSync(path.join(workspace, 'bin/aegis'), [
      'github', '--report', path.join(output, 'review.json'),
      '--expected-base', env.AEGIS_BASE, '--expected-head', env.AEGIS_HEAD,
      '--html-output', path.join(output, 'review.html'), '--summary', summary,
      '--artifact-name', env.AEGIS_ARTIFACT_NAME,
      '--fail-on', env.AEGIS_FAIL_ON || 'p1',
      '--fail-on-needs-review', env.AEGIS_FAIL_ON_NEEDS_REVIEW || 'p0',
      '--fail-on-incomplete=' + (env.AEGIS_FAIL_ON_INCOMPLETE || 'true'),
      '--require-agent=' + (env.AEGIS_REQUIRE_AGENT || 'false'),
      '--max-annotations', '50',
    ], { stdio: 'inherit', timeout: 120000 });
    exitCode = result.status === 0 ? 0 : 1;
    if (result.error) throw new Error('Publisher process could not finish');
    // JSON identity validation is not full report-schema validation. The
    // trusted Go publisher owns status/verdict/schema checks. Mark evidence
    // publishable only after it successfully produced both expected outputs.
    if (![0, 1].includes(result.status) || !fs.existsSync(summary) ||
        !fs.lstatSync(summary).isFile() || !fs.existsSync(path.join(output, 'review.html')) ||
        !fs.lstatSync(path.join(output, 'review.html')).isFile()) {
      throw new Error('Trusted publisher rejected evidence or failed to render it');
    }
    reportValid = true;
    if (!executionCompatible(report, env.AEGIS_ANALYSIS_RESULT, env.AEGIS_ANALYSIS_EXIT)) {
      exitCode = 1;
      fs.appendFileSync(summary, '\n\n> ❌ Review execution did not complete successfully. The merge gate is blocked even if the partial report contains no P0/P1 findings.\n');
    }
  } catch (error) {
    exitCode = 1;
    reportValid = false;
    // Parser errors may contain attacker-controlled payloads: never echo them.
    fs.writeFileSync(summary, '# 🛡️ Aegis Code Review\n\n❌ **Review incomplete — merge gate blocked.** The evidence is missing, invalid, or does not match the requested commits. Inspect the workflow diagnostics and rerun.\n');
    console.error('Aegis publishing failed: evidence or trusted publisher validation did not complete.');
  }
  if (!fs.existsSync(summary)) {
    exitCode = 1;
    fs.writeFileSync(summary, '# 🛡️ Aegis Code Review\n\n❌ **Review incomplete — merge gate blocked.** The publisher did not produce a summary.\n');
  }
  fs.writeFileSync(path.join(output, 'publication.json'), JSON.stringify({
    schema_version: 1, base: env.AEGIS_BASE, head: env.AEGIS_HEAD,
    report_valid: reportValid, exit_code: exitCode,
    fail_on: env.AEGIS_FAIL_ON || 'p1', fail_on_needs_review: env.AEGIS_FAIL_ON_NEEDS_REVIEW || 'p0',
    fail_on_incomplete: (env.AEGIS_FAIL_ON_INCOMPLETE || 'true') === 'true',
    require_agent: (env.AEGIS_REQUIRE_AGENT || 'false') === 'true',
  }, null, 2) + '\n');
  if (env.GITHUB_STEP_SUMMARY) fs.appendFileSync(env.GITHUB_STEP_SUMMARY, fs.readFileSync(summary));
  if (env.GITHUB_OUTPUT) fs.appendFileSync(env.GITHUB_OUTPUT, `exit-code=${exitCode}\n`);
  return exitCode;
}

module.exports = { readEvidence, executionCompatible, publish };
if (require.main === module) publish(); // Enforce gate after artifact/comment upload.
