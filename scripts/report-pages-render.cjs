'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { validateRecord, recordPath } = require('./report-pages-common.cjs');
const { renderIndex, renderDiagnostic, decorateReport } = require('./report-pages-view.cjs');

function readRegular(file, limit) {
  const stat = fs.lstatSync(file);
  if (!stat.isFile() || stat.size > limit) throw new Error('Renderer output must be a bounded regular file');
  return fs.readFileSync(file, 'utf8');
}

function classify(record, gate) {
  if (gate.incomplete) return 'incomplete';
  if (gate.blocked) return 'blocked';
  if (record.conclusion !== 'success' || record.publication?.exit_code === 1) return 'incomplete';
  if (gate.degraded || gate.needs_review) return 'degraded';
  return 'passed';
}

// The binary is compiled from the trusted default-branch source. No target Go
// code, artifact HTML/JS, provider, or model-verification command is executed.
function renderRecord(input, { binary, directory, archived = false }) {
  const record = { ...input, status: input.status || 'incomplete', summary: input.summary || '' };
  validateRecord(record);
  fs.mkdirSync(directory, { recursive: false });
  let html;
  if (record.report !== null && !record.diagnostic) {
    try {
      fs.writeFileSync(path.join(directory, 'input.json'), JSON.stringify(record.report), { flag: 'wx' });
      const args = ['github', '--report', path.join(directory, 'input.json'), '--expected-base', record.base,
        '--expected-head', record.head, '--html-output', path.join(directory, 'report.html'),
        '--summary', path.join(directory, 'summary.md'), '--gate-output', path.join(directory, 'gate.json'),
        '--annotations=false', '--artifact-name', record.protocol === 'legacy' ? 'aegis-review-report' : `aegis-review-report-${record.run_attempt}`,
        '--fail-on', record.policy.fail_on, '--fail-on-needs-review', record.policy.fail_on_needs_review,
        '--fail-on-incomplete=' + record.policy.fail_on_incomplete, '--require-agent=' + record.policy.require_agent];
      const result = spawnSync(binary, args, { encoding: 'utf8', timeout: 120000, maxBuffer: 1024 * 1024,
        cwd: directory, env: { PATH: process.env.PATH, LANG: 'C.UTF-8', GOTOOLCHAIN: 'local' } });
      if (result.error || ![0, 1].includes(result.status)) throw new Error('Trusted renderer failed');
      const gate = JSON.parse(readRegular(path.join(directory, 'gate.json'), 8192));
      if (gate.schema_version !== 1 || gate.base !== record.base || gate.head !== record.head ||
          ['blocked', 'incomplete', 'degraded', 'needs_review'].some(key => typeof gate[key] !== 'boolean') ||
          gate.blocked !== (result.status === 1)) throw new Error('Invalid renderer gate evidence');
      html = readRegular(path.join(directory, 'report.html'), 32 * 1024 * 1024);
      if (!/<body\b[^>]*>/i.test(html) || !html.includes('Content-Security-Policy')) throw new Error('Missing safe HTML document');
      const status = classify(record, gate);
      if (archived && record.status !== status) throw new Error('Archived decision requires an explicit migration');
      record.status = status;
      record.summary = readRegular(path.join(directory, 'summary.md'), 2 * 1024 * 1024).slice(0, 54000);
      if ((record.conclusion !== 'success' || record.publication?.exit_code === 1) && !gate.blocked) {
        record.summary = '> ❌ **Original review workflow did not pass.** Findings below do not override its execution or delivery failure.\n\n' + record.summary;
      }
    } catch (error) {
      if (archived) throw new Error('Archived report could not be faithfully rendered; history was not replaced');
      record.report = null;
      record.protocol = 'diagnostic';
      record.status = 'incomplete';
      record.diagnostic = '可信渲染器未能验证或展示评审证据。请查看原始工作流和 Artifact；此页不提供通过结论。';
    }
  }
  if (record.report === null || record.diagnostic) {
    record.status = 'incomplete';
    record.report = null;
    record.protocol = 'diagnostic';
    record.diagnostic ||= '没有可验证的报告证据。';
    record.summary = '# 🛡️ Aegis Code Review\n\n> ❌ **评审未完成。** 没有可验证的完整证据，不能据此判定代码安全。\n\n' + record.diagnostic + '\n';
    html = renderDiagnostic(record);
  }
  validateRecord(record);
  html = decorateReport(html, record);
  return { record, html };
}

function buildSite(records, { binary, directory, scratch }) {
  if (!Array.isArray(records) || records.length > 200) throw new Error('Invalid report archive');
  fs.mkdirSync(directory);
  fs.mkdirSync(scratch);
  const seen = new Set();
  let totalBytes = 0;
  for (const [index, input] of records.entries()) {
    const target = recordPath(input);
    if (seen.has(target)) throw new Error('Duplicate archive identity');
    seen.add(target);
    const { html } = renderRecord(input, { binary, directory: path.join(scratch, String(index)), archived: true });
    totalBytes += Buffer.byteLength(html, 'utf8');
    if (totalBytes > 512 * 1024 * 1024) throw new Error('Rendered history exceeds 512 MiB; no deployment was prepared');
    const output = path.join(directory, target);
    fs.mkdirSync(output, { recursive: true });
    fs.writeFileSync(path.join(output, 'index.html'), html, { flag: 'wx' });
  }
  fs.writeFileSync(path.join(directory, 'index.html'), renderIndex(records), { flag: 'wx' });
  fs.writeFileSync(path.join(directory, '.nojekyll'), '', { flag: 'wx' });
}

module.exports = { renderRecord, renderIndex, buildSite, classify };
