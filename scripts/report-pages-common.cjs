'use strict';

const priorities = new Set(['p0', 'p1', 'p2', 'p3', 'none']);
const conclusions = new Set(['success', 'failure', 'cancelled', 'timed_out', 'action_required', 'neutral', 'skipped', 'stale', 'startup_failure']);

function positiveID(value) {
  return Number.isSafeInteger(value) && value > 0;
}

function validatePolicy(policy) {
  if (!policy || !priorities.has(policy.fail_on) || !priorities.has(policy.fail_on_needs_review) ||
      typeof policy.fail_on_incomplete !== 'boolean' || typeof policy.require_agent !== 'boolean') {
    throw new Error('Invalid archived merge policy');
  }
  return policy;
}

function validateRecord(record) {
  if (!record || record.schema_version !== 1 ||
      !/^[a-zA-Z0-9][a-zA-Z0-9-]{0,38}\/[a-zA-Z0-9_.-]{1,100}$/.test(record.repository || '') ||
      ['.', '..'].includes(record.repository.split('/')[1]) ||
      !positiveID(record.pr_number) || !positiveID(record.run_id) || !positiveID(record.run_attempt) ||
      !/^[0-9a-f]{40}$/.test(record.base || '') || !/^[0-9a-f]{40}$/.test(record.head || '') ||
      !conclusions.has(record.conclusion) || !['legacy', 'v1', 'diagnostic'].includes(record.protocol) ||
      !['passed', 'degraded', 'blocked', 'incomplete'].includes(record.status)) {
    throw new Error('Invalid public report identity or status');
  }
  const runURL = `https://github.com/${record.repository}/actions/runs/${record.run_id}`;
  if (![runURL, `${runURL}/attempts/${record.run_attempt}`].includes(record.run_url)) {
    throw new Error('Report run URL does not match its identity');
  }
  const artifactPrefix = runURL + '/artifacts/';
  if (typeof record.artifact_url !== 'string' || (record.artifact_url !== '' &&
      (!record.artifact_url.startsWith(artifactPrefix) || !/^[1-9][0-9]*$/.test(record.artifact_url.slice(artifactPrefix.length))))) {
    throw new Error('Report artifact URL does not match its run');
  }
  if (typeof record.created_at !== 'string' || !/^\d{4}-\d{2}-\d{2}T[0-9:.]+Z$/.test(record.created_at) ||
      !Number.isFinite(Date.parse(record.created_at)) || typeof record.summary !== 'string' ||
      record.summary.length > 60000 || typeof record.diagnostic !== 'string' || record.diagnostic.length > 2000) {
    throw new Error('Invalid public report metadata');
  }
  validatePolicy(record.policy);
  if (record.report !== null && (typeof record.report !== 'object' || Array.isArray(record.report) ||
      record.report.comparison?.base_commit !== record.base || record.report.comparison?.head_commit !== record.head)) {
    throw new Error('Archived evidence identity mismatch');
  }
  if (record.report === null && record.status !== 'incomplete') throw new Error('Missing evidence cannot be clean');
  if (record.diagnostic && record.status !== 'incomplete') throw new Error('Diagnostic report must remain incomplete');
  if (record.protocol === 'diagnostic' && (record.report !== null || record.status !== 'incomplete' || !record.diagnostic)) {
    throw new Error('Diagnostic protocol cannot contain a successful evidence report');
  }
  if (record.protocol === 'legacy' && record.publication !== null) throw new Error('Legacy report cannot claim v1 publication provenance');
  if (record.protocol === 'v1' && (!record.publication || !record.publication.report_valid || record.report === null)) {
    throw new Error('v1 report requires valid publication evidence');
  }
  if (['passed', 'degraded'].includes(record.status) && record.conclusion !== 'success') {
    throw new Error('Unsuccessful workflow cannot become a passed report');
  }
  if (record.publication !== null) {
    const publication = record.publication;
    if (!publication || publication.schema_version !== 1 || publication.base !== record.base || publication.head !== record.head ||
        typeof publication.report_valid !== 'boolean' || ![0, 1].includes(publication.exit_code)) {
      throw new Error('Invalid archived publication provenance');
    }
    validatePolicy(publication);
    if (!publication.report_valid && (record.report !== null || record.status !== 'incomplete' || record.protocol !== 'diagnostic')) {
      throw new Error('Invalid original evidence cannot become a successful report');
    }
    for (const key of ['fail_on', 'fail_on_needs_review', 'fail_on_incomplete', 'require_agent']) {
      if (record.policy[key] !== publication[key]) throw new Error('Archived publication policy mismatch');
    }
    if (publication.exit_code === 1 && ['passed', 'degraded'].includes(record.status)) {
      throw new Error('Original blocked result cannot become passed');
    }
  }
  return record;
}

function recordPath(record) {
  if (!positiveID(record.pr_number) || !positiveID(record.run_id) || !positiveID(record.run_attempt) || !/^[0-9a-f]{40}$/.test(record.head || '')) {
    throw new Error('Invalid public report path identity');
  }
  return `reports/pr-${record.pr_number}/${record.head}/${record.run_id}-${record.run_attempt}`;
}

function escapeHTML(value) {
  return String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;').replaceAll("'", '&#39;');
}

module.exports = { positiveID, validatePolicy, validateRecord, recordPath, escapeHTML };
