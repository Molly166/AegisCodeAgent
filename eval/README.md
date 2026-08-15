# Aegis Eval Corpus

This directory contains the versioned **Golden Regression Corpus** for Aegis review reports and merge-gate behavior. It is designed to make validation reproducible and auditable; it is not presented as a live-model benchmark.

## Corpus composition

The v2 corpus contains exactly 50 cases:

| Case kind | Count | Purpose |
| --- | ---: | --- |
| Bug | 27 | Match recorded P0-P3 findings and verify severity-aware gate behavior |
| Clean | 15 | Measure false blocks on negative controls |
| Needs Review | 5 | Preserve unresolved hypotheses and exercise the independent P0 review threshold |
| Resilience | 3 | Verify partial-stage and degraded-Agent gate semantics |

Finding distribution:

| Priority | Expected findings | Default gate |
| --- | ---: | --- |
| P0 | 9 | Block |
| P1 | 9 | Block |
| P2 | 6 | Report only |
| P3 | 3 | Report only |

The corpus exercises `static`, `semantic`, `agent`, `context`, `mixed`, and `gate` evaluation layers. Four cases are regressions derived from failures observed while building Aegis; 46 are explicitly labeled `curated_synthetic` negative or positive controls.

## Files and provenance

- `catalog.json` is the human-reviewable source of truth for scenario labels and distribution.
- `cases/<id>/case.json` is the generated v2 expectation contract.
- `cases/<id>/report.json` is the generated, versioned Aegis v6 golden report.
- `tools/evalcorpus` validates the catalog and regenerates case artifacts atomically.

Every case declares:

- `kind`: `bug`, `clean`, `needs_review`, or `resilience`;
- `layer`: the part of the review system under evaluation;
- `provenance`: `regression` or `curated_synthetic`;
- the expected finding contract, merge-gate outcome, and unresolved-hypothesis count.

The Harness rejects weak bug labels: each expected finding must include severity, category, path, positive line, rule ID, and title constraint. Clean, Needs Review, and resilience cases have mutually exclusive contracts so they cannot silently drift into one another.

## Run the corpus

```bash
go run ./cmd/aegis eval \
  --corpus ./eval/cases \
  --format html \
  --output ./eval-report.html
```

Strict mode exits non-zero when a finding is missed, an unexpected finding exceeds the allowance, a Needs Review count changes, or the merge-gate result is wrong.

Regenerate checked-in artifacts after reviewing `catalog.json`:

```bash
go run ./tools/evalcorpus \
  --catalog ./eval/catalog.json \
  --output ./eval/cases
```

CI also asserts the exact 50-case composition, priority distribution, provenance split, layer coverage, and expected blocked/passed gate counts.

## Metric interpretation

The HTML and JSON reports include precision, recall, F1, P0-P3 recall, gate accuracy, false-block rate, unresolved hypotheses, latency, and token usage.

The checked-in corpus currently reports 100% replay precision/recall because the reports are golden regression artifacts. This proves that schema migration, matching, reporting, Needs Review handling, and merge-gate policy remain stable. It **does not** prove 100% DeepSeek accuracy.

A publishable model-quality claim requires a separate live evaluation run that replaces golden reports with fresh pipeline outputs, repeats probabilistic cases, and reports variance, Agent partial rate, latency, token use, and confidence intervals. Golden replay and live-model metrics must remain separately labeled.
