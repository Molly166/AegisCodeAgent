'use strict';

const { TextDecoder } = require('node:util');
const { validateRecord, recordPath } = require('./report-pages-common.cjs');

const BRANCH = 'aegis-report-history';
const MARKER = '.aegis-report-history.json';
const MAX_RECORDS = 200;
const MAX_RECORD_BYTES = 17 * 1024 * 1024;
const MAX_ARCHIVE_BYTES = 64 * 1024 * 1024;
const MAX_ATTEMPTS = 4;
const RECORD_PATH = /^records\/pr-[1-9]\d*\/[0-9a-f]{40}\/[1-9]\d*-[1-9]\d*\.json$/;
const TREE_PATH = /^(?:records|records\/pr-[1-9]\d*|records\/pr-[1-9]\d*\/[0-9a-f]{40})$/;

function repositoryName(repo) {
  if (!repo || typeof repo.owner !== 'string' || typeof repo.repo !== 'string' ||
      !/^[A-Za-z0-9][A-Za-z0-9-]*$/.test(repo.owner) ||
      !/^[A-Za-z0-9_.-]+$/.test(repo.repo) || ['.', '..'].includes(repo.repo)) {
    throw new Error('A valid GitHub repository identity is required');
  }
  return `${repo.owner}/${repo.repo}`;
}

// Never call user-supplied toJSON methods; only JSON data is archived. Sorting
// every object's keys also makes identical records independent of insertion order.
function canonicalJSON(value, ancestors = new Set(), depth = 0) {
  if (depth > 128) throw new Error('Archive JSON exceeds the nesting limit');
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return JSON.stringify(value);
  if (typeof value === 'number' && Number.isFinite(value)) return JSON.stringify(value);
  if (!value || typeof value !== 'object' || ancestors.has(value)) throw new Error('Archive requires acyclic JSON data');
  if (!Array.isArray(value) && ![Object.prototype, null].includes(Object.getPrototypeOf(value))) {
    throw new Error('Archive requires plain JSON objects');
  }
  ancestors.add(value);
  let result;
  if (Array.isArray(value)) {
    const elements = [];
    for (let index = 0; index < value.length; index++) {
      if (!Object.hasOwn(value, index)) throw new Error('Archive JSON arrays cannot contain holes');
      elements.push(canonicalJSON(value[index], ancestors, depth + 1));
    }
    result = '[' + elements.join(',') + ']';
  } else {
    result = '{' + Object.keys(value).sort().map(key => {
      const descriptor = Object.getOwnPropertyDescriptor(value, key);
      if (!descriptor || !Object.hasOwn(descriptor, 'value')) throw new Error('Archive JSON cannot contain accessors');
      return JSON.stringify(key) + ':' + canonicalJSON(descriptor.value, ancestors, depth + 1);
    }).join(',') + '}';
  }
  ancestors.delete(value);
  return result;
}

function storagePath(record) {
  const publicPath = recordPath(record);
  if (typeof publicPath !== 'string' || !publicPath.startsWith('reports/')) throw new Error('Invalid public record path');
  const result = 'records/' + publicPath.slice('reports/'.length) + '.json';
  if (!RECORD_PATH.test(result)) throw new Error('Invalid archive record path');
  return result;
}

function sha(value) {
  if (!/^[0-9a-f]{40}$/.test(value || '')) throw new Error('Invalid Git object identity in report archive');
  return value;
}

function markerFor(repository) {
  return { schema_version: 1, repository, kind: 'aegis-public-report-history' };
}

async function readBlob(git, repo, entry, limit) {
  const { data } = await git.getBlob({ ...repo, file_sha: entry.sha });
  if (!data || data.encoding !== 'base64' || !Number.isSafeInteger(data.size) || data.size < 0 ||
      data.size > limit || data.size !== entry.size || typeof data.content !== 'string' ||
      data.content.length > Math.ceil(limit / 3) * 4 + Math.ceil(limit / 40) + 1024) {
    throw new Error('Invalid or oversized JSON blob in report archive');
  }
  if (data.sha !== undefined && sha(data.sha) !== entry.sha) throw new Error('Archive blob identity changed');
  const encoded = data.content.replace(/[\r\n]/g, '');
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(encoded)) {
    throw new Error('Invalid archive blob encoding');
  }
  const bytes = Buffer.from(encoded, 'base64');
  if (bytes.length !== entry.size || bytes.length > limit) throw new Error('Archive blob size does not match its tree');
  try {
    return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
  } catch {
    // Do not include parser snippets from attacker-controlled stored evidence.
    throw new Error('Report archive contains invalid UTF-8 JSON');
  }
}

async function mapBounded(values, concurrency, visit) {
  let next = 0;
  const result = new Array(values.length);
  await Promise.all(Array.from({ length: Math.min(concurrency, values.length) }, async () => {
    while (next < values.length) {
      const index = next++;
      result[index] = await visit(values[index]);
    }
  }));
  return result;
}

async function loadArchive({ github, repo }) {
  const repository = repositoryName(repo);
  const git = github?.rest?.git;
  if (!git) throw new Error('GitHub Git Database API is required');
  let reference;
  try {
    reference = (await git.getRef({ ...repo, ref: `heads/${BRANCH}` })).data;
  } catch (error) {
    if (error.status === 404) return { records: [], branchSha: null, treeSha: null, totalBytes: 0 };
    throw error;
  }
  if (reference?.ref !== `refs/heads/${BRANCH}` || reference.object?.type !== 'commit') {
    throw new Error('Unexpected report archive branch reference');
  }
  const branchSha = sha(reference.object.sha);
  const commit = (await git.getCommit({ ...repo, commit_sha: branchSha })).data;
  const treeSha = sha(commit?.tree?.sha);
  const tree = (await git.getTree({ ...repo, tree_sha: treeSha, recursive: 'true' })).data;
  if (!tree || tree.truncated !== false || !Array.isArray(tree.tree)) throw new Error('Truncated or invalid report archive tree');
  if (tree.sha !== undefined && sha(tree.sha) !== treeSha) throw new Error('Archive tree identity changed');
  const paths = new Set();
  const directories = [];
  const entries = [];
  let marker;
  let totalBytes = 0;
  for (const entry of tree.tree) {
    if (!entry || typeof entry.path !== 'string' || paths.has(entry.path)) throw new Error('Invalid or duplicate path in report archive');
    paths.add(entry.path);
    sha(entry.sha);
    if (entry.type === 'tree' && entry.mode === '040000' && TREE_PATH.test(entry.path)) {
      directories.push(entry.path);
      continue;
    }
    if (entry.type !== 'blob' || entry.mode !== '100644' ||
        !Number.isSafeInteger(entry.size) || entry.size < 0) {
      throw new Error('Archive accepts only regular non-executable JSON files');
    }
    if (entry.path === MARKER) {
      if (entry.size > 1024) throw new Error('Invalid report archive marker');
      marker = entry;
    } else if (RECORD_PATH.test(entry.path)) {
      if (entry.size > MAX_RECORD_BYTES) throw new Error('Archive record exceeds 17 MiB');
      entries.push(entry);
    } else {
      throw new Error('Unknown file in dedicated report archive branch');
    }
    totalBytes += entry.size;
    if (totalBytes > MAX_ARCHIVE_BYTES) throw new Error('Report archive exceeds 64 MiB capacity');
    if (entries.length > MAX_RECORDS) throw new Error('Report archive exceeds 200-record capacity');
  }
  if (!marker) throw new Error('Existing branch is not a dedicated Aegis report archive');
  for (const directory of directories) {
    if (!entries.some(entry => entry.path.startsWith(directory + '/'))) throw new Error('Unknown directory in report archive');
  }
  const markerData = await readBlob(git, repo, marker, 1024);
  if (canonicalJSON(markerData) !== canonicalJSON(markerFor(repository))) throw new Error('Report archive marker does not match this repository');
  entries.sort((left, right) => left.path.localeCompare(right.path, 'en'));
  const records = await mapBounded(entries, 4, async entry => {
    const record = await readBlob(git, repo, entry, MAX_RECORD_BYTES);
    validateRecord(record);
    if (record.repository !== repository || storagePath(record) !== entry.path) throw new Error('Archive record identity does not match its repository and path');
    // Validate the full data shape before exposing it to the trusted renderer.
    canonicalJSON(record);
    return record;
  });
  return { records, branchSha, treeSha, totalBytes };
}

async function saveRecord({ github, repo, record }) {
  const repository = repositoryName(repo);
  const content = canonicalJSON(record) + '\n';
  const bytes = Buffer.byteLength(content);
  if (bytes > MAX_RECORD_BYTES) throw new Error('Archive record exceeds 17 MiB');
  const snapshot = JSON.parse(content);
  validateRecord(snapshot);
  if (snapshot.repository !== repository) throw new Error('Record belongs to a different repository');
  const path = storagePath(snapshot);
  const markerContent = canonicalJSON(markerFor(repository)) + '\n';
  for (let attempt = 0; attempt < MAX_ATTEMPTS; attempt++) {
    try {
      const archive = await loadArchive({ github, repo });
      const existing = archive.records.find(value => storagePath(value) === path);
      if (existing) {
        if (canonicalJSON(existing) !== content.trimEnd()) throw new Error('An immutable archive record cannot be overwritten');
        return { record: existing, path, branchSha: archive.branchSha, created: false };
      }
      const additionalBytes = bytes + (archive.branchSha ? 0 : Buffer.byteLength(markerContent));
      if (archive.records.length >= MAX_RECORDS || archive.totalBytes + additionalBytes > MAX_ARCHIVE_BYTES) {
        throw new Error('Report archive capacity reached; no history was deleted');
      }
      const git = github.rest.git;
      const blob = (await git.createBlob({ ...repo, content, encoding: 'utf-8' })).data;
      const updates = [{ path, mode: '100644', type: 'blob', sha: sha(blob?.sha) }];
      if (!archive.branchSha) {
        const marker = (await git.createBlob({ ...repo, content: markerContent, encoding: 'utf-8' })).data;
        updates.push({ path: MARKER, mode: '100644', type: 'blob', sha: sha(marker?.sha) });
      }
      const createdTree = (await git.createTree({ ...repo, ...(archive.treeSha ? { base_tree: archive.treeSha } : {}), tree: updates })).data;
      const createdCommit = (await git.createCommit({
        ...repo, message: `Archive Aegis report for PR #${snapshot.pr_number} run ${snapshot.run_id}/${snapshot.run_attempt}`,
        tree: sha(createdTree?.sha), parents: archive.branchSha ? [archive.branchSha] : [],
      })).data;
      const commitSha = sha(createdCommit?.sha);
      if (archive.branchSha) {
        // The new commit has exactly the observed tip as its parent. A concurrent
        // append makes this non-fast-forward, so GitHub rejects it atomically.
        await git.updateRef({ ...repo, ref: `heads/${BRANCH}`, sha: commitSha, force: false });
      } else {
        await git.createRef({ ...repo, ref: `refs/heads/${BRANCH}`, sha: commitSha });
      }
      return { record: snapshot, path, branchSha: commitSha, created: true };
    } catch (error) {
      if (![409, 422].includes(error.status)) throw error;
      if (attempt === MAX_ATTEMPTS - 1) throw new Error('Concurrent report archive updates did not settle after four attempts');
      // Re-read and revalidate the winner; never overwrite or force-push it.
    }
  }
  throw new Error('Unable to save report archive');
}

module.exports = { loadArchive, saveRecord };
