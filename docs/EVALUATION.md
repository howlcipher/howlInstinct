# Evaluation

"Is this decision class trustworthy enough for this use?" is an empirical
question. The harness exists because the only honest way to answer it is to
measure, and because a component that produces confident-looking judgments
with no way to check them converts an open question into a settled-looking one.

```sh
howlinstinct eval --dataset evals/routing/cases.yaml
howlinstinct eval --dataset evals/routing/cases.yaml --json
howlinstinct eval --dataset evals/routing/cases.yaml --min-accuracy 0.6
```

## The central rule

**A metric is reported only when the data supports it.** Otherwise it is
`null`, with a reason.

This is the harness's main discipline, and it is a negative one. A Brier score
averaged over the subset of cases that happened to return distributions is not
a slightly worse Brier score; it describes a different population than the
accuracy printed beside it. Emitting `0` for an uncomputable metric is the
single easiest way for an evaluation harness to lie, because zero looks like a
measurement.

```json
{
  "brier": null,
  "brier_unavailable_reason": "only 7 of 14 scored cases returned a probability distribution"
}
```

## Datasets

Version-controlled YAML or JSON. The dataset supplies a default question, and
a case may override it.

```yaml
schema: howlinstinct.eval_dataset/v1
name: routing
version: 1
description: >
  What this suite measures, and what it does not.

question:
  type: classify
  instructions: Classify this report into exactly one category.
  options: [bug, feature, security, documentation, operations, unknown]

cases:
  - id: route-001
    state: "Clicking Save throws a 500 and the change is lost."
    expected: bug
    notes: optional
```

Labels are checked against the question at load time, so a typo is caught
before any provider is called rather than becoming a silently unscorable case
afterwards. Score labels may be written as a level name or a level index;
both resolve to the same expectation.

The dataset's SHA-256 is recorded in every report, over the file's bytes
rather than the parsed structure, so a report names exactly which content
produced it.

## Metrics

| Metric | Reported when | Suppressed when |
| --- | --- | --- |
| Accuracy | at least one case was scored | nothing was scored |
| Confusion counts | always | — |
| Brier score | every scored case has a distribution | any case lacks one |
| Log loss | same | same |
| Calibration buckets | same | same, or no bucket was populated |
| Mean absolute error | score questions | any other type |
| Escalation rate | always | — |
| Latency p50/p95/max | at least one successful call | — |
| Errored cases and failures | always | — |

### Brier score

Multiclass: the summed squared error between the predicted distribution and
the one-hot truth, averaged over cases. Lower is better.

For a two-class question its range is **0 to 2**, not 0 to 1, because both
class terms contribute. A perfect prediction scores 0, an even split scores
0.5, and a confidently wrong two-class prediction scores 2. Those endpoints
are pinned by tests so the definition cannot drift silently.

A noul's single probability is treated as a full two-class distribution, so it
scores with the same machinery as a choice.

### Log loss

The mean negative log probability of the true class, clamped to
`[1e-15, 1-1e-15]`.

The clamp matters. A provider reporting probability zero for the outcome that
actually happened would otherwise make the whole run's log loss infinite,
destroying the information carried by every other case.

### Calibration

Predictions are binned by the probability assigned to the predicted class,
into ten buckets, and each bucket reports how often those predictions turned
out right. A well-calibrated provider that says 0.8 should be right about 80%
of the time.

Empty buckets are omitted rather than reported as zero accuracy. No
predictions landing in a bin is not evidence of anything.

### Mean absolute error

Score questions only: the average distance between the predicted score and the
expected level index.

This is what makes an ordinal scale measurable. Predicting "high" when the
truth is "critical" is a smaller mistake than predicting "negligible", and
plain accuracy cannot see the difference.

### Escalation rate

The share of judgments the **caller's own rule** marked for escalation.

With no rule supplied it is zero by construction, and the report says so
explicitly rather than leaving it to be inferred. Zero means "nothing was
asked for", not "nothing was uncertain". To measure escalation, supply a
threshold:

```sh
howlinstinct eval --dataset evals/routing/cases.yaml --min-instinct-margin 0.3
```

## Reports

Both renderings carry the same data and the same caveat.

```json
{
  "schema": "howlinstinct.eval_run/v1",
  "dataset": {"name": "routing", "version": 1, "sha256": "sha256:3baa...", "cases": 14},
  "provider": "mock",
  "model": "mock-lexical-baseline-v1",
  "started_at": "2026-09-22T04:00:45Z",
  "duration_ms": 3,
  "escalation_rule_supplied": false,
  "metrics": { "...": "..." },
  "failures": [],
  "caveat": "These numbers describe one provider against one dataset at one moment..."
}
```

The caveat travels with the data because evaluation numbers are routinely
quoted away from their context, and a metric without its provenance is not
evidence of anything.

A case that produced no usable judgment is recorded in `failures` with its id
and reason, and still counted in the denominator. Dropping it would quietly
improve the score.

## Gating in CI

```sh
howlinstinct eval --dataset evals/routing/cases.yaml --min-accuracy 0.6
```

Exits 7 if accuracy is below the threshold, and also if accuracy could not be
measured at all: a gate that cannot be evaluated has not passed.

The gate lives in the command, not the harness. The harness measures; whether
a number is good enough is a policy question belonging to whoever runs it.

## The shipped suites

Four demonstration suites, reflecting real Howl use cases:

| Suite | Type | Intended consumer |
| --- | --- | --- |
| `routing` | classify | HowlPlane and HowlBoard |
| `change_risk` | score | HowlChangeOps |
| `evidence_sufficiency` | noul | HowlProof |
| `agent_run_triage` | classify | HowlPlane |

**These are demonstrations, not benchmarks.** They are few, written by one
person, and deliberately not adversarial. Passing them is not evidence that a
provider is fit to route anyone's real traffic.

Run against the shipped mock, they score at or below chance on the semantic
suites. That is the correct result for a word-overlap baseline, and it is the
honest starting point: the number to beat is a word counter, and a provider
that cannot is not doing what you are paying it for.

Two suites are worth reading as design artifacts. `evidence_sufficiency`
pairs claims with evidence that is lexically near-identical but semantically
opposite, which is designed to expose pattern matching. `agent_run_triage`
contains the case where an agent made its tests pass by deleting the failing
assertions: exit status says success, and collapsing that into `success` is
the expensive mistake.

## Writing your own

Use your own data. The shipped suites demonstrate the harness; they cannot
tell you anything about your traffic.

- Label by what the reporter described, not by how it sounds.
- Include cases with genuinely insufficient signal, and label them as such. A
  provider that confidently classifies "hey" is overreaching, and only an
  unclear case will show you that.
- Include near-miss pairs that differ semantically but not lexically.
- Record what a label means in `description` or `notes`. Risk and severity
  labels are judgement calls, and six months later the disagreement will be
  with the labels as much as with the provider.
