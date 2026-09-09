'use strict';

const { positiveID, validateRecord, recordPath } = require('./report-pages-common.cjs');

const reviewMarker = '<!-- aegis-code-agent-review -->';
const identityPattern = /^<!-- aegis-public-report:([1-9][0-9]*):([1-9][0-9]*):([0-9a-f]{40}) -->$/;
const verdicts = Object.freeze({
  passed: '✅ **评审通过：未触发当前合并门禁。**',
  degraded: '⚠️ **门禁未阻断，但仍有未确认候选或评审降级项，请查看报告。**',
  blocked: '❌ **合并门禁阻断：请查看报告中的阻断原因。**',
  incomplete: '❌ **评审未完成：不能将本次结果视为没有问题。**',
});

function pagesURL(value) {
  if (typeof value !== 'string' || value.length > 2048 || /[\u0000-\u0020\u007f<>"'`\\()[\]{}]/u.test(value)) {
    throw new Error('Unsafe Pages URL');
  }
  let url;
  try {
    url = new URL(value);
    if (/[\u0000-\u0020\u007f<>"'`\\()[\]{}]/u.test(decodeURIComponent(url.pathname))) {
      throw new Error('Unsafe encoded Pages path');
    }
  } catch {
    throw new Error('Invalid Pages URL');
  }
  if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash || url.port) {
    throw new Error('Pages URL must be HTTPS without credentials, query, fragment or custom port');
  }
  if (!url.pathname.endsWith('/')) url.pathname += '/';
  return url;
}

function compareRun(left, right) {
  return left.run_id - right.run_id || left.run_attempt - right.run_attempt;
}

function commentIdentity(body) {
  // Only our dedicated second line is identity metadata. A quoted marker in
  // report text or a code example must never supersede a genuine review.
  const line = String(body).split('\n', 3)[1] || '';
  const match = identityPattern.exec(line);
  if (!match) return null;
  const identity = { run_id: Number(match[1]), run_attempt: Number(match[2]), head: match[3] };
  return positiveID(identity.run_id) && positiveID(identity.run_attempt) ? identity : null;
}

function isOwnedReview(comment) {
  return comment?.user?.login === 'github-actions[bot]' && comment.user.type === 'Bot' &&
    typeof comment.body === 'string' && (comment.body === reviewMarker || comment.body.startsWith(reviewMarker + '\n'));
}

function samePR(pr, record) {
  return pr?.state === 'open' && pr.base?.sha === record.base && pr.head?.sha === record.head;
}

function renderBody(record, page) {
  const url = new URL(recordPath(record) + '/index.html', page).href;
  const links = [`[查看 Workflow 运行](${record.run_url})`];
  if (record.artifact_url) links.unshift(`[下载 HTML / 证据 Artifact](${record.artifact_url})`);
  // The renderer supplies Markdown. Strip hidden identity comments so copied
  // source text cannot masquerade as a later publication in a future update.
  const summary = record.summary.replace(/<!--[\s\S]*?(?:-->|$)/g, '')
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g, '').slice(0, 40000).trim();
  return [reviewMarker,
    `<!-- aegis-public-report:${record.run_id}:${record.run_attempt}:${record.head} -->`, '',
    `## [🌐 查看完整网页报告](${url})`, '', verdicts[record.status],
    ...(record.report === null ? ['', '此页面仅包含失败诊断；完整评审证据未生成，不能据此批准合并。'] : []),
    '', summary, '', '---', links.join(' · '), '',
    `_Review commit \`${record.head}\` · run \`${record.run_id}\` · attempt \`${record.run_attempt}\`._`,
  ].join('\n');
}

async function hasCurrentCompletedRun(github, repo, record) {
  const runs = await github.paginate(github.rest.actions.listWorkflowRuns, {
    ...repo, workflow_id: 'aegis-review.yml', event: 'pull_request', head_sha: record.head, per_page: 100,
  });
  if (!Array.isArray(runs)) throw new Error('Missing Aegis workflow run evidence');
  let found = false;
  for (const run of runs) {
    if (run.head_sha !== record.head || run.event !== 'pull_request' ||
        typeof run.path !== 'string' || run.path.split('@')[0] !== '.github/workflows/aegis-review.yml' ||
        !positiveID(run.id) || !positiveID(run.run_attempt)) {
      throw new Error('Unexpected Aegis workflow run identity');
    }
    const identity = { run_id: run.id, run_attempt: run.run_attempt };
    // Queued/in-progress later runs supersede old feedback as well. Re-running
    // the same workflow ID increments its attempt, not necessarily its ID.
    if (compareRun(identity, record) > 0) return false;
    if (compareRun(identity, record) === 0) found = run.status === 'completed';
  }
  return found;
}

async function refreshFeedback({ github, context, core }, { records, pageURL }) {
  if (!Array.isArray(records) || records.length > 1000) throw new Error('Bounded deployed report records are required');
  const repository = `${context.repo.owner}/${context.repo.repo}`;
  const latestByPR = new Map();
  for (const record of records) {
    validateRecord(record);
    if (record.repository.toLowerCase() !== repository.toLowerCase()) throw new Error('Report belongs to another repository');
    const previous = latestByPR.get(record.pr_number);
    if (previous && compareRun(previous, record) === 0 && (previous.head !== record.head || previous.base !== record.base)) {
      throw new Error('Conflicting deployed report identities');
    }
    if (!previous || compareRun(record, previous) > 0) latestByPR.set(record.pr_number, record);
  }
  const { data: repo } = await github.rest.repos.get(context.repo);
  if (repo.private !== false || repo.visibility !== 'public' || repo.full_name?.toLowerCase() !== repository.toLowerCase()) {
    throw new Error('Public feedback hosting requires this exact public repository');
  }
  const { data: configuredPages } = await github.rest.repos.getPages(context.repo);
  const deployment = pagesURL(pageURL);
  const configured = pagesURL(configuredPages.html_url);
  if (deployment.href !== configured.href) throw new Error('Deployment Pages URL does not match trusted GitHub Pages configuration');

  const result = { created: 0, updated: 0, unchanged: 0, skipped: 0 };
  const skip = (number, reason) => {
    result.skipped++;
    core.notice(`PR #${number}: ${reason}; preserving existing feedback.`);
  };
  for (const record of latestByPR.values()) {
    const number = record.pr_number;
    const { data: current } = await github.rest.pulls.get({ ...context.repo, pull_number: number });
    if (!samePR(current, record)) {
      skip(number, 'closed or changed Base/Head');
      continue;
    }
    if (!await hasCurrentCompletedRun(github, context.repo, record)) {
      skip(number, 'a newer run/attempt exists or this review is not completed');
      continue;
    }
    const comments = await github.paginate(github.rest.issues.listComments, {
      ...context.repo, issue_number: number, per_page: 100,
    });
    const owned = comments.filter(isOwnedReview);
    if (owned.some(comment => {
      const identity = commentIdentity(comment.body);
      return identity && compareRun(identity, record) > 0;
    })) {
      skip(number, 'a newer bot publication already exists');
      continue;
    }
    // Re-read the selected bot comment: another publisher may have refreshed
    // it during list pagination without changing the PR Head.
    let previous = owned[0];
    if (previous) {
      const { data: refreshed } = await github.rest.issues.getComment({ ...context.repo, comment_id: previous.id });
      const identity = commentIdentity(refreshed.body);
      if (!isOwnedReview(refreshed) || (identity && compareRun(identity, record) > 0)) {
        skip(number, 'the bot comment changed during pagination');
        continue;
      }
      previous = refreshed;
    }
    const body = renderBody(record, deployment);
    // Both checks happen after comment pagination and immediately before the
    // write. GitHub has no atomic PR-head-and-comment compare-and-swap API.
    if (!await hasCurrentCompletedRun(github, context.repo, record)) {
      skip(number, 'a newer run/attempt appeared during pagination');
      continue;
    }
    const { data: latest } = await github.rest.pulls.get({ ...context.repo, pull_number: number });
    if (!samePR(latest, record)) {
      skip(number, 'PR Base/Head changed during pagination');
      continue;
    }
    if (previous?.body === body) {
      result.unchanged++;
    } else if (previous) {
      await github.rest.issues.updateComment({ ...context.repo, comment_id: previous.id, body });
      result.updated++;
    } else {
      await github.rest.issues.createComment({ ...context.repo, issue_number: number, body });
      result.created++;
    }
  }
  return result;
}

module.exports = { refreshFeedback };
