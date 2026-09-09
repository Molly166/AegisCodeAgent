'use strict';

const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { TextDecoder } = require('node:util');

const workflowPath = '.github/workflows/aegis-review.yml';
// These are reviewer-approved producer snapshots, not artifact assertions.
// Update only through a reviewed default-branch change after auditing the
// workflow's execution and gate policy. Never load allow-lists from PR input.
const producerWorkflowSHA256 = Object.freeze({
  legacy: '11dcdd369a4a83eba8dbcffd3ac7cae9870db81570616bb4dcb82cf1f4308d3c',
  // Exact staged examples/aegis-review-v1-migration.yml, copied unchanged to
  // .github/workflows/aegis-review.yml during the separately reviewed activation.
  v1: '5c1ca01490ab638d8ffa3c2835a79542ea48d49618ac79c1d746838c825c1cce',
});
const defaultPolicy = Object.freeze({
  fail_on: 'p1', fail_on_needs_review: 'p0', fail_on_incomplete: true, require_agent: false,
});
const priorities = ['p0', 'p1', 'p2', 'p3', 'none'];
const conclusions = ['success', 'failure', 'neutral', 'cancelled', 'skipped', 'timed_out', 'action_required', 'stale', 'startup_failure'];

function positiveID(value) {
  if (!/^[1-9][0-9]{0,14}$/.test(String(value)) || !Number.isSafeInteger(Number(value))) {
    throw new Error('Expected a positive run, attempt or PR identifier');
  }
  return Number(value);
}

function repositoryName(value) {
  const name = typeof value === 'string' ? value : value && `${value.owner}/${value.repo}`;
  if (typeof name !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9-]{0,38}\/[A-Za-z0-9._-]{1,100}$/.test(name) ||
      ['.', '..'].includes(name.split('/')[1])) {
    throw new Error('Invalid repository identity');
  }
  return name;
}

function sha(value) {
  if (typeof value !== 'string' || !/^[0-9a-f]{40}$/.test(value)) throw new Error('Invalid immutable commit identity');
  return value;
}

function timestamp(value) {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,3})?Z$/.test(value)) {
    throw new Error('Invalid run timestamp');
  }
  const date = new Date(value);
  if (!Number.isFinite(date.getTime()) || date.toISOString().slice(0, 19) !== value.slice(0, 19)) {
    throw new Error('Invalid run timestamp');
  }
  return date.toISOString();
}

function validateRun(run, repository, runID, attempt) {
  if (!run || run.id !== runID || run.run_attempt !== attempt || run.status !== 'completed' ||
      run.event !== 'pull_request' || run.name !== 'Aegis Code Review' ||
      typeof run.path !== 'string' || run.path.split('@')[0] !== workflowPath ||
      run.repository?.full_name?.toLowerCase() !== repository.toLowerCase() ||
      run.repository?.private === true || !conclusions.includes(run.conclusion)) {
    throw new Error('Source is not the selected completed Aegis PR review attempt');
  }
  sha(run.head_sha);
}

function diagnostic(source, reason) {
  return { ...source, protocol: 'diagnostic', artifact_id: 0, artifact_url: '', diagnostic: reason };
}

function validateAssociatedRepository(repository, expected) {
  if (!repository || (!repository.full_name && !repository.url) ||
      (repository.full_name && (typeof repository.full_name !== 'string' || repository.full_name.toLowerCase() !== expected.toLowerCase())) ||
      (repository.url && repository.url !== `https://api.github.com/repos/${expected}`)) {
    throw new Error('Associated PR Base belongs to another repository');
  }
}

async function approvedProducerWorkflow(github, repo, head, protocol) {
  try {
    const { data } = await github.rest.repos.getContent({ ...repo, path: workflowPath, ref: head });
    if (!data || Array.isArray(data) || data.type !== 'file' || data.path !== workflowPath ||
        data.encoding !== 'base64' || !Number.isInteger(data.size) || data.size < 1 || data.size > 512 * 1024 ||
        typeof data.content !== 'string' || data.content.length > 768 * 1024) return false;
    const encoded = data.content.replace(/\s/g, '');
    if (encoded.length % 4 !== 0 || !/^[A-Za-z0-9+/]*={0,2}$/.test(encoded)) return false;
    const bytes = Buffer.from(encoded, 'base64');
    return bytes.length === data.size && crypto.createHash('sha256').update(bytes).digest('hex') === producerWorkflowSHA256[protocol];
  } catch {
    return false;
  }
}

// No PR checkout, current-PR-head lookup, or execution of downloaded files.
// A rerun advancing while we inspect metadata is rejected instead of mixing
// old evidence with the new attempt's status or artifact list.
async function resolveSource({ github, repo, runId, attempt, prNumber, eventRun }) {
  const requestedRepository = repositoryName(repo);
  const [owner, name] = requestedRepository.split('/');
  const coordinates = { owner, repo: name };
  const runID = positiveID(runId), runAttempt = positiveID(attempt);
  const { data: repository } = await github.rest.repos.get(coordinates);
  const fullName = repositoryName(repository?.full_name);
  if (fullName.toLowerCase() !== requestedRepository.toLowerCase() ||
      repository.private !== false || repository.visibility !== 'public') {
    throw new Error('Public report hosting requires the requested public repository');
  }
  const { data: current } = await github.rest.actions.getWorkflowRun({ ...coordinates, run_id: runID });
  validateRun(current, fullName, runID, runAttempt);
  const { data: run } = await github.rest.actions.getWorkflowRunAttempt({
    ...coordinates, run_id: runID, attempt_number: runAttempt,
  });
  validateRun(run, fullName, runID, runAttempt);
  if (run.head_sha !== current.head_sha || run.conclusion !== current.conclusion) {
    throw new Error('Workflow attempt identity changed during source resolution');
  }
  // Only the trusted workflow_run event payload may restore an association
  // which GitHub removed after the PR merged while this publisher was queued.
  // Callers must never populate eventRun from an artifact or dispatch input.
  if (!Array.isArray(current.pull_requests) || current.pull_requests.length > 1) {
    throw new Error('Source run must have exactly one GitHub-associated pull request');
  }
  let association = current.pull_requests[0];
  if (eventRun !== undefined) {
    validateRun(eventRun, fullName, runID, runAttempt);
    if (eventRun.head_sha !== run.head_sha || eventRun.conclusion !== run.conclusion ||
        !Array.isArray(eventRun.pull_requests) || eventRun.pull_requests.length !== 1) {
      throw new Error('Trusted completion event does not match the selected review attempt');
    }
    const eventAssociation = eventRun.pull_requests[0];
    validateAssociatedRepository(eventAssociation.base?.repo, fullName);
    if (association && (association.number !== eventAssociation.number ||
        association.base?.sha !== eventAssociation.base?.sha || association.head?.sha !== eventAssociation.head?.sha)) {
      throw new Error('Completion event and current run disagree on PR identity');
    }
    association ||= eventAssociation;
  }
  if (!association) throw new Error('Source run must have exactly one GitHub-associated pull request');
  const number = positiveID(association.number);
  if (prNumber !== undefined && prNumber !== null && prNumber !== '' && positiveID(prNumber) !== number) {
    throw new Error('Source run does not belong to the selected pull request');
  }
  const base = sha(association.base?.sha), head = sha(association.head?.sha);
  if (head !== run.head_sha) throw new Error('Source run Head does not match its PR association');
  // Workflow-run PR associations expose a repository REST URL; full_name is
  // accepted as well for clients which return expanded repository objects.
  validateAssociatedRepository(association.base?.repo, fullName);
  if (Array.isArray(run.pull_requests) && run.pull_requests.length > 0 &&
      (run.pull_requests.length !== 1 || run.pull_requests[0].number !== number ||
       run.pull_requests[0].base?.sha !== base || run.pull_requests[0].head?.sha !== head)) {
    throw new Error('Workflow attempt PR association does not match the selected run');
  }
  const created = timestamp(run.created_at);
  const started = timestamp(run.run_started_at);
  const completed = timestamp(run.updated_at);
  if (Date.parse(created) > Date.parse(started) || Date.parse(started) > Date.parse(completed)) {
    throw new Error('Workflow attempt timestamps are inconsistent');
  }
  const source = {
    schema_version: 1, repository: fullName, pr_number: number, base, head,
    run_id: runID, run_attempt: runAttempt,
    run_url: `https://github.com/${fullName}/actions/runs/${runID}/attempts/${runAttempt}`,
    artifact_url: '', created_at: created, conclusion: run.conclusion,
    protocol: 'diagnostic', artifact_id: 0, diagnostic: '', policy: { ...defaultPolicy },
  };
  const artifacts = await github.paginate(github.rest.actions.listWorkflowRunArtifacts, {
    ...coordinates, run_id: runID, per_page: 100,
  });
  let resolved;
  const named = Array.isArray(artifacts) ? artifacts.filter(item =>
    item.name === `aegis-review-report-${runAttempt}` || item.name === 'aegis-review-report') : [];
  if (named.length !== 1) {
    resolved = diagnostic(source, 'The review report artifact is missing or ambiguous for this run attempt.');
  } else {
    const artifact = named[0];
    const protocol = artifact.name === 'aegis-review-report' ? 'legacy' : 'v1';
    let validTime = false;
    try {
      const uploaded = Date.parse(timestamp(artifact.created_at));
      validTime = uploaded >= Date.parse(started) && uploaded <= Date.parse(completed);
    } catch { /* Malformed artifact metadata is not evidence. */ }
    if (!Number.isSafeInteger(artifact.id) || artifact.id <= 0 || artifact.expired !== false ||
        !Number.isSafeInteger(artifact.size_in_bytes) || artifact.size_in_bytes < 0 ||
        artifact.size_in_bytes > 20 * 1024 * 1024 || !validTime ||
        (artifact.workflow_run && (artifact.workflow_run.id !== runID ||
          artifact.workflow_run.head_sha !== head))) {
      resolved = diagnostic(source, 'The report artifact is expired, oversized, or not bound to this run attempt.');
    } else if (![current, run, ...(eventRun?.referenced_workflows !== undefined ? [eventRun] : [])].every(item =>
      Array.isArray(item.referenced_workflows) && item.referenced_workflows.length === 0)) {
      // A fixed caller file does not prove the identity of every called
      // workflow. Until that chain is attested, do not claim reusable support.
      // The webhook schema makes referenced_workflows optional. Its absence
      // does not replace either mandatory REST topology check above; if the
      // event provides this field, it must agree on an empty direct-call chain.
      resolved = diagnostic(source, 'Reusable or nested report-producer workflow chains are not approved for public evidence hosting.');
    } else if (!await approvedProducerWorkflow(github, coordinates, head, protocol)) {
      resolved = diagnostic(source, 'The report producer workflow is not an approved version for this evidence protocol.');
    } else {
      resolved = { ...source, protocol,
        artifact_id: artifact.id,
        artifact_url: `https://github.com/${fullName}/actions/runs/${runID}/artifacts/${artifact.id}` };
    }
  }
  const { data: latest } = await github.rest.actions.getWorkflowRun({ ...coordinates, run_id: runID });
  validateRun(latest, fullName, runID, runAttempt);
  if (latest.head_sha !== head || latest.conclusion !== run.conclusion) {
    throw new Error('Workflow attempt changed before artifact selection completed');
  }
  return resolved;
}

function boundedJSON(directory, name, limit) {
  if (typeof directory !== 'string' || !path.isAbsolute(directory) || !fs.lstatSync(directory).isDirectory()) {
    throw new Error('Evidence directory is not a regular absolute directory');
  }
  const file = path.join(directory, name);
  const before = fs.lstatSync(file);
  if (!before.isFile() || before.size < 1 || before.size > limit) throw new Error('Invalid evidence file');
  const descriptor = fs.openSync(file, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW);
  try {
    const stat = fs.fstatSync(descriptor);
    if (!stat.isFile() || stat.size !== before.size || stat.dev !== before.dev || stat.ino !== before.ino) {
      throw new Error('Evidence file changed while being opened');
    }
    // Read a fixed maximum rather than readFileSync on a possibly growing file.
    const buffer = Buffer.alloc(stat.size + 1);
    let count = 0;
    while (count < buffer.length) {
      const read = fs.readSync(descriptor, buffer, count, buffer.length - count, null);
      if (read === 0) break;
      count += read;
    }
    if (count !== stat.size) throw new Error('Evidence file size changed while being read');
    return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(buffer.subarray(0, count)));
  } finally {
    fs.closeSync(descriptor);
  }
}

function policyFromPublication(publication, source) {
  if (!publication || typeof publication !== 'object' || Array.isArray(publication) ||
      publication.schema_version !== 1 || publication.report_valid !== true ||
      publication.base !== source.base || publication.head !== source.head ||
      ![0, 1].includes(publication.exit_code) ||
      !priorities.includes(publication.fail_on) || !priorities.includes(publication.fail_on_needs_review) ||
      typeof publication.fail_on_incomplete !== 'boolean' || typeof publication.require_agent !== 'boolean') {
    throw new Error('Publication metadata did not validate');
  }
  return { fail_on: publication.fail_on, fail_on_needs_review: publication.fail_on_needs_review,
    fail_on_incomplete: publication.fail_on_incomplete, require_agent: publication.require_agent };
}

function createRecord(source, evidenceDir) {
  const { artifact_id: ignoredArtifactID, ...metadata } = source;
  const record = { ...metadata, policy: { ...source.policy }, report: null, publication: null, summary: '' };
  if (source.protocol === 'diagnostic') return record;
  try {
    if (!['legacy', 'v1'].includes(source.protocol)) throw new Error('Unknown report protocol');
    sha(source.base); sha(source.head);
    const report = boundedJSON(evidenceDir, 'review.json', 16 * 1024 * 1024);
    if (!report || typeof report !== 'object' || Array.isArray(report) ||
        report.comparison?.base_commit !== source.base || report.comparison?.head_commit !== source.head) {
      throw new Error('Report does not match the expected commits');
    }
    if (source.protocol === 'v1') {
      const publication = boundedJSON(evidenceDir, 'publication.json', 8192);
      record.policy = policyFromPublication(publication, source);
      // Keep only validated metadata, never attacker-selected extra fields.
      record.publication = { schema_version: 1, base: source.base, head: source.head,
        report_valid: true, exit_code: publication.exit_code, ...record.policy };
    } else {
      // Legacy has no publication manifest. A trusted Go renderer must compute
      // its gate; do not invent exit_code or turn artifact presence into success.
      record.policy = { ...defaultPolicy };
    }
    record.report = report;
    return record;
  } catch {
    return { ...record, protocol: 'diagnostic', report: null, publication: null,
      diagnostic: 'The review evidence is missing, malformed, incomplete, or does not match its source commits.' };
  }
}

module.exports = { resolveSource, createRecord };
