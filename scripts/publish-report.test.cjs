'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { publish } = require('./publish-report.cjs');

const base = 'a'.repeat(40), head = 'b'.repeat(40);
const approvedLogo = fs.readFileSync(path.join(__dirname, '../docs/assets/aegis-pr-gate-harmony.png'));

test('publisher fallback embeds only the approved logo and keeps a blocking offline report', t => {
  for (const evidence of [null, '<img src=x onerror=alert(1)> & <script>bad()</script>']) {
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'aegis-publisher-brand-'));
    t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
    if (evidence !== null) {
      fs.mkdirSync(path.join(directory, 'evidence'));
      fs.writeFileSync(path.join(directory, 'evidence/review.json'), evidence);
    }
    const exitCode = publish({ GITHUB_WORKSPACE: directory, AEGIS_BASE: base, AEGIS_HEAD: head });
    assert.equal(exitCode, 1);
    const html = fs.readFileSync(path.join(directory, 'artifacts/review.html'), 'utf8');
    const images = [...html.matchAll(/<img\b[^>]*>/gi)];
    assert.equal(images.length, 1, 'only the trusted brand image may be emitted');
    const encoded = images[0][0].match(/\bsrc="data:image\/png;base64,([A-Za-z0-9+/=]+)"/)?.[1];
    assert.ok(encoded);
    assert.deepEqual(Buffer.from(encoded, 'base64'), approvedLogo);
    assert.match(images[0][0], /\balt="" width="48" height="48"/);
    assert.match(html, /\.ap-brand-mark img\s*\{[^}]*object-fit:\s*contain/);
    assert.match(html, /<div class="ap-diagnostic-icon" aria-hidden="true">!<\/div>/);
    assert.match(html, /<h1>Review incomplete — merge gate blocked<\/h1>/);
    assert.match(html, /No clean review conclusion is available/);
    assert.match(html, /Content-Security-Policy/);
    assert.ok(html.includes("default-src 'none'; style-src 'unsafe-inline'; img-src data:"));
    assert.doesNotMatch(html, /<svg\b|<script\b|<link\b|@import\b|\bon(?:error|load)\s*=/i);
    assert.doesNotMatch(html, /\b(?:src|srcset)\s*=\s*["'](?:https?:)?\/\/|url\(\s*["']?(?:https?:)?\/\//i);
    if (evidence !== null) assert.equal(html.includes(evidence), false, 'parser failures must not echo attacker-controlled evidence');
    const publication = JSON.parse(fs.readFileSync(path.join(directory, 'artifacts/publication.json')));
    assert.equal(publication.report_valid, false);
    assert.equal(publication.exit_code, 1);
  }
});
