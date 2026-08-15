# Aegis Eval Corpus

Each case directory contains:

- `case.json`: a versioned expectation contract;
- `report.json`: a saved Aegis `v6` review report to replay.

`case.json` matches expected findings by severity and repository-relative path, with optional category, line, rule ID, and title substring constraints. It also declares the expected merge-gate result, expected number of `Needs Review` hypotheses, and the allowed number of additional findings. A case passes only when all of these contracts hold.

Run the checked-in regression corpus:

```bash
go run ./cmd/aegis eval \
  --corpus ./eval/cases \
  --format html \
  --output ./eval-report.html
```

The command exits non-zero in strict mode when a finding is missed, an unexpected finding exceeds the case allowance, an unresolved hypothesis count changes, or the merge-gate result is wrong. The HTML report includes precision, recall, F1, P0/P1 recall, gate accuracy, false-block rate, unresolved hypotheses, latency, and token usage.

The checked-in corpus is intentionally a seed regression set, not yet a statistically representative benchmark. Add independently labeled Bug and Clean cases before publishing model-quality claims.
