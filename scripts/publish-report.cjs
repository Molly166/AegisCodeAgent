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

function writeFailureReport(output, message) {
  const escaped = message.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
  fs.writeFileSync(path.join(output, 'review.html'), '<!doctype html><html lang="en"><meta charset="utf-8">' +
    '<meta name="viewport" content="width=device-width,initial-scale=1"><title>Aegis — review incomplete</title>' +
    '<body><main><h1>Review incomplete — merge gate blocked</h1><p>' + escaped +
    '</p><p>No clean review conclusion is available. Inspect the workflow diagnostics before rerunning.</p></main></body></html>');
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
      const warning = '> ❌ **Review execution incomplete — merge gate blocked.** Findings below do not override this execution failure.\n\n';
      fs.writeFileSync(summary, warning + fs.readFileSync(summary, 'utf8'));
      const htmlPath = path.join(output, 'review.html');
      const html = fs.readFileSync(htmlPath, 'utf8');
      if (!/<body\b[^>]*>/i.test(html)) throw new Error('Trusted HTML renderer did not produce a document body');
      fs.writeFileSync(htmlPath, html.replace(/(<body\b[^>]*>)/i,
        '$1<div role="alert" style="background:#991b1b;color:white;padding:16px;font:600 16px system-ui">Review execution: BLOCKED. The review process did not complete successfully; findings below do not override this result.</div>'));
    }
  } catch (error) {
    exitCode = 1;
    reportValid = false;
    // Parser errors may contain attacker-controlled payloads: never echo them.
    fs.writeFileSync(summary, '# 🛡️ Aegis Code Review\n\n❌ **Review incomplete — merge gate blocked.** The evidence is missing, invalid, or does not match the requested commits. Inspect the workflow diagnostics and rerun.\n');
    writeFailureReport(output, 'The evidence is missing, invalid, or does not match the requested commits.');
    console.error('Aegis publishing failed: evidence or trusted publisher validation did not complete.');
  }
  if (!fs.existsSync(summary)) {
    exitCode = 1;
    fs.writeFileSync(summary, '# 🛡️ Aegis Code Review\n\n❌ **Review incomplete — merge gate blocked.** The publisher did not produce a summary.\n');
    writeFailureReport(output, 'The trusted publisher did not produce a summary.');
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
