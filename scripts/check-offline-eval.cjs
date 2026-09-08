'use strict';

const fs = require('node:fs');

function checkOfflineEval(report) {
  const metrics = report?.metrics;
  const runs = report?.runs;
  const healthy = report?.schema_version === 'live-v1' && report.mode === 'live_pipeline' &&
    report.provider === 'none' && report.live_model_requested === false && report.repeats === 1 &&
    metrics?.runs === 12 && metrics.unique_cases === 12 && metrics.tokens === 0 &&
    metrics.incomplete_rate?.numerator === 0 && metrics.incomplete_rate?.denominator === 12 &&
    Array.isArray(runs) && runs.length === 12 && new Set(runs.map(run => run.case_id)).size === 12 &&
    runs.every(run => run.incomplete === false && run.tokens === 0 && run.review && !run.error);
  const number = value => Number.isSafeInteger(value) && value >= 0 ? String(value) : 'unavailable';
  const fraction = value => `${number(value?.numerator)}/${number(value?.denominator)}`;
  const summary = [
    '### Offline executable pipeline baseline', '',
    healthy ? '**Execution health: complete.** No model API calls were requested.' : '**Execution health: failed.** Inspect incomplete or malformed run evidence.',
    '',
    '| Measurement | Observed |', '| --- | --- |',
    `| Completed executions | ${number(metrics?.runs)} |`,
    `| Cases passing the detection rubric | ${number(metrics?.passed_runs)}/12 |`,
    `| Finding recall | ${fraction(metrics?.finding_recall)} |`,
    `| Finding precision | ${fraction(metrics?.finding_precision)} |`,
    `| Gate accuracy | ${fraction(metrics?.gate_accuracy)} |`,
    `| Incomplete executions | ${fraction(metrics?.incomplete_rate)} |`,
    '',
    'This check validates execution health only. Detection misses and false positives remain in the measured results; they are not converted into passing labels. This synthetic, no-model baseline is not an LLM accuracy claim.',
    '',
  ].join('\n');
  return { healthy, summary, detectionPassed: metrics?.passed_runs === 12 };
}

module.exports = { checkOfflineEval };
if (require.main === module) {
  try {
    const result = checkOfflineEval(JSON.parse(fs.readFileSync(process.argv[2], 'utf8')));
    if (process.env.GITHUB_STEP_SUMMARY) fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, result.summary);
    else process.stdout.write(result.summary);
    if (!result.detectionPassed) console.log('::warning title=Aegis detection baseline has misses::Detection quality is below the full corpus rubric. See measured recall, false positives and missed IDs in the retained artifact.');
    process.exitCode = result.healthy ? 0 : 1;
  } catch (error) {
    console.error('Offline eval health assertion failed: missing or invalid JSON evidence.');
    process.exitCode = 1;
  }
}
