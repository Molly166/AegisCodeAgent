'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { readEvidence } = require('./publish-report.cjs');

function validatePublication(publication, base, head) {
  if (publication.schema_version !== 1 || publication.report_valid !== true ||
      publication.base !== base || publication.head !== head || ![0, 1].includes(publication.exit_code)) {
    throw new Error('Invalid publication provenance');
  }
  for (const key of ['fail_on', 'fail_on_needs_review']) {
    if (!['p0', 'p1', 'p2', 'p3', 'none'].includes(publication[key])) throw new Error('Invalid gate priority');
  }
  if (typeof publication.require_agent !== 'boolean' || typeof publication.fail_on_incomplete !== 'boolean') {
    throw new Error('Invalid gate policy');
  }
  return publication;
}

function host(env = process.env) {
  const root = env.GITHUB_WORKSPACE;
  if (!root || !path.isAbsolute(root)) throw new Error('Absolute workspace required');
  const evidence = path.join(root, 'evidence/review.json');
  readEvidence(evidence, env.AEGIS_BASE, env.AEGIS_HEAD);
  const manifest = path.join(root, 'evidence/publication.json');
  const stat = fs.lstatSync(manifest);
  if (!stat.isFile() || stat.size > 8192) throw new Error('Invalid publication metadata file');
  const publication = validatePublication(JSON.parse(fs.readFileSync(manifest, 'utf8')), env.AEGIS_BASE, env.AEGIS_HEAD);
  const site = path.join(root, 'site');
  fs.mkdirSync(site); // Fresh output only; no archived files are copied to Pages.
  const html = path.join(site, 'index.html');
  const result = spawnSync(path.join(root, 'bin/aegis'), [
    'github', '--report', evidence, '--expected-base', env.AEGIS_BASE, '--expected-head', env.AEGIS_HEAD,
    '--html-output', html, '--summary', path.join(root, 'hosting-summary.md'), '--annotations=false',
    '--fail-on', publication.fail_on, '--fail-on-needs-review', publication.fail_on_needs_review,
    '--fail-on-incomplete=' + publication.fail_on_incomplete, '--require-agent=' + publication.require_agent,
  ], { stdio: 'inherit', timeout: 120000 });
  // Exit 1 is expected for a blocked review. It is still a publishable report.
  if (result.error || ![0, 1].includes(result.status) || !fs.existsSync(html)) throw new Error('Trusted HTML renderer failed');
  if (publication.exit_code === 1 && result.status === 0) {
    // Missing execution-level evidence can block a run separately from finding
    // thresholds. Do not silently turn that blocked run into an approved page.
    const content = fs.readFileSync(html, 'utf8');
    fs.writeFileSync(html, content.replace(/(<body\b[^>]*>)/i, '$1<div role="alert" style="background:#991b1b;color:white;padding:16px;font:600 16px system-ui">Original workflow result: BLOCKED. The review process or delivery did not complete successfully; findings below do not override that result.</div>'));
  }
}

module.exports = { validatePublication, host };
if (require.main === module) host();
