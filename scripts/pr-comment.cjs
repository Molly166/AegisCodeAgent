'use strict';

const fs = require('node:fs');
const path = require('node:path');
const marker = '<!-- aegis-code-agent-review -->';

async function publishComment({ github, context, core }, env = process.env) {
  const number = context.payload.pull_request.number;
  const { data: current } = await github.rest.pulls.get({ ...context.repo, pull_number: number });
  if (current.state !== 'open' || current.head.sha !== env.AEGIS_HEAD || current.base.sha !== env.AEGIS_BASE) {
    core.notice('PR commits changed or the PR closed; keeping the latest comment intact.');
    return;
  }
  const summary = fs.readFileSync(path.join(env.GITHUB_WORKSPACE, 'artifacts/review-summary.md'), 'utf8');
  const links = [];
  for (const [label, value] of [['Download HTML evidence', env.AEGIS_ARTIFACT_URL], ['Workflow run', env.AEGIS_RUN_URL]]) {
    const url = new URL(value);
    if (url.origin !== (env.GITHUB_SERVER_URL || 'https://github.com')) throw new Error('Untrusted report link origin');
    links.push(`[${label}](${url.href})`);
  }
  // Keep footer and identity intact under GitHub's 65,536-character limit.
  const body = [marker, summary.slice(0, 56000).trim(), '', '---', links.join(' · '), '',
    `_Reviewed commit \`${env.AEGIS_HEAD}\`. Updated after each completed review run._`].join('\n');
  const comments = await github.paginate(github.rest.issues.listComments, {
    ...context.repo, issue_number: number, per_page: 100,
  });
  const previous = comments.find(comment => comment.user?.login === 'github-actions[bot]' &&
    comment.user?.type === 'Bot' && comment.body?.startsWith(marker));
  // Recheck immediately before writing; pagination may have taken time.
  const { data: latest } = await github.rest.pulls.get({ ...context.repo, pull_number: number });
  if (latest.state !== 'open' || latest.head.sha !== env.AEGIS_HEAD || latest.base.sha !== env.AEGIS_BASE) return;
  if (previous) {
    await github.rest.issues.updateComment({ ...context.repo, comment_id: previous.id, body });
  } else {
    await github.rest.issues.createComment({ ...context.repo, issue_number: number, body });
  }
}

module.exports = publishComment;
