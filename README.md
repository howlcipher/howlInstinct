# HowlInstinct

The fast semantic decision layer of the [Howl](https://github.com/howlcipher)
ecosystem.

**Documentation site:** https://howlcipher.github.io/howlInstinct/

Given some state and a bounded question, HowlInstinct returns a **typed
judgment with explicit uncertainty and provenance**.

```sh
howlinstinct decide --type noul \
  --state "All payment requests are returning HTTP 500 across every region. Customers cannot check out." \
  --question "Is this an active production incident affecting customers?" \
  --true-meaning "production is broken and customers are affected right now" \
  --false-meaning "routine or cosmetic, with no customer impact"
```

```
noul  [noul]
  answer:     yes
  P(yes):     0.8404
  provider confidence: not reported by this provider
  instinct margin:     0.6807
  outcome:    ACCEPTABLE_CONFIDENCE

provider mock model mock-lexical-baseline-v1 in 0ms, batch dec_0d1024e1dbbe7910d36e183f230cc863

HowlInstinct reports judgments, not permission. Acting on these is the caller's decision.
```

That is real output from the shipped offline mock, which is a word-overlap
baseline rather than a model. Drop the `--true-meaning` and `--false-meaning`
hints and the same command returns P(yes) 0.5060, an almost exact coin flip,
because the question and the state then share no vocabulary. Both numbers are
honest, and the second is the more useful lesson: this is what the default
provider is, and it is why the number to beat is a word counter.

## What it is not

It is not an agent. It does not plan, execute, retry a workflow, grant
permission, or bypass policy. It has no vocabulary for consequences: it cannot
deploy, page, merge, roll back, approve, or delete.

The line it exists to protect:

> **Confidence is not permission.**
>
> HowlInstinct judges. Something else decides whether anything is allowed to
> happen as a result.

```
HowlInstinct:  "This appears to be a low-risk deployment."  probability: high
HowlFrame:     "Policy still requires approval, because production database
                migrations may never self-authorize."
```

Both are correct at the same time. There is no default threshold anywhere in
this codebase, and a test parses the source to keep it that way.

## Why it exists

Most questions a system asks itself are small. Is this an incident? Which team
owns this? How risky is this change? The usual options are a brittle keyword
rule nobody trusts, or a full generative agent that is slow, expensive, and
returns prose someone then has to parse.

The governing principle is **use the least powerful mechanism that reliably
solves the problem**:

```
deterministic code  ->  HowlInstinct  ->  reasoning agent  ->  human
```

The first arrow matters most. If ordinary code can answer the question,
HowlInstinct is the wrong tool.

## Install

```sh
go install github.com/howlcipher/howlinstinct/cmd/howlinstinct@latest
```

Or from a clone:

```sh
make build      # -> build/howlinstinct
make check      # lint, vet, race detector
```

Requires Go 1.26 or newer. Nothing else: no credentials, no network, no
inference hardware.

## Quick start

The default provider is a deterministic offline mock, so this works
immediately after install.

```sh
howlinstinct providers     # what adapters exist
howlinstinct doctor        # resolved config, and anything risky about it
howlinstinct version
```

### One question

```sh
howlinstinct decide --type choice \
  --state "The password reset token is not invalidated after use." \
  --question "Which team owns this?" \
  --options bug,feature,security,operations \
  --json
```

### A batch

Several independent questions about the same state, in one call:

```sh
cat > incident.json <<'JSON'
{
  "state": "All payment requests are returning HTTP 500. Customers cannot check out.",
  "questions": {
    "is_incident": {"type": "noul", "instructions": "Is this an active production incident?"},
    "category": {"type": "classify", "instructions": "Which category?",
                 "options": [{"name": "billing"}, {"name": "outage"}, {"name": "performance"}]},
    "severity": {"type": "score", "instructions": "How severe?",
                 "levels": [{"name": "low"}, {"name": "moderate"}, {"name": "high"}, {"name": "critical"}]}
  },
  "escalation": {"category": {"min_instinct_margin": 0.5}},
  "correlation_id": "incident-4021"
}
JSON

howlinstinct decide --input incident.json --json
cat incident.json | howlinstinct decide --json     # stdin works too
```

Question identifiers are yours and come back exactly as you wrote them.
Duplicate identifiers are rejected rather than silently collapsed.

### See the whole loop

```sh
make demo      # event -> judgments -> receipts -> mock policy consumer
```

This runs `examples/dogfood`, which walks an engineering event through three
primitives into a policy consumer that owns every threshold. It is the
clearest single demonstration of where the boundary sits.

## Decision types

| Type | Question | Returns |
| --- | --- | --- |
| `noul` | bounded yes/no | P(yes). **No separate confidence**: the probability is the uncertainty signal. |
| `choice` | pick one of a finite set | the selection, a distribution, and a confidence |
| `score` | place on an ordinal scale you define | a weighted level index, the legend, a distribution, a confidence |
| `classify` | convenience over `choice` | same as choice, with `compiled_to: choice` recorded |

`classify` is lowered to `choice` before any provider sees it. There is no
fourth provider primitive, deliberately: a primitive the provider cannot
express would have to be faked, and faking it means inventing uncertainty.

## Uncertainty, handled honestly

Two numbers, named for where they came from, never converted into each other:

- **`provider_confidence`** is what the provider reported. **Absent, never
  zero, when it reported none.** A noul has none; emitting `0.0` would assert
  the provider was maximally unconfident when it said nothing at all.
- **`instinct_margin`** is derived here, with a documented formula: how
  decisively the distribution favours the selected value.

Escalation happens only against a rule **you** supply:

```sh
howlinstinct decide ... --min-instinct-margin 0.5
```

Without one, nothing escalates, however uncertain the judgment. See
[docs/PHILOSOPHY.md](docs/PHILOSOPHY.md).

## Evaluation

Whether a decision class is trustworthy enough for your use is an empirical
question.

```sh
howlinstinct eval --dataset evals/routing/cases.yaml
howlinstinct eval --dataset evals/routing/cases.yaml --min-accuracy 0.6   # CI gate
make evals                                                                # all suites
```

The harness reports a metric **only when the data supports it**, and emits
`null` plus a reason otherwise. Four demonstration suites ship, covering
routing, change risk, evidence sufficiency, and agent run triage.

Run against the shipped mock they score at or below chance on the semantic
suites, which is the correct result for a word-overlap baseline and an honest
number to beat. See [docs/EVALUATION.md](docs/EVALUATION.md).

## Providers

| Provider | What it is |
| --- | --- |
| `mock` (default) | deterministic lexical baseline. No network, credentials, or hardware. Not a semantic model. |
| `jev` | any endpoint speaking the Jev-compatible System One contract (`POST /v1/systemone`) |

```toml
# ~/.config/howlinstinct/config.toml
provider    = "jev"
base_url    = "https://api.example.com"
model       = "jev-latest"
api_key_env = "MY_PROVIDER_API_KEY"   # the NAME, never the key
```

No vendor, hostname, model, or hardware assumption is compiled in, and there
is no default endpoint. Local and remote endpoints are both supported;
HowlInstinct ships no weights and embeds no inference engine, because it is
decision infrastructure rather than an inference engine.

See [docs/PROVIDERS.md](docs/PROVIDERS.md).

## Decision receipts

Every judgment is representable as a durable, versioned receipt
(`howlinstinct.decision_receipt/v1`).

State is recorded as a SHA-256 hash, **not as text**, unless you explicitly
opt in with `--retain-state`. Endpoints are rebuilt from scheme, host, and
path so no credential can ride along. Digests are over canonical bytes, so the
same decision always produces the same hash regardless of map ordering.

See [docs/DECISION_RECEIPTS.md](docs/DECISION_RECEIPTS.md).

## Exit codes

The exit status reports whether the tool worked, never what the answer was. A
noul answering "no" exits 0.

| Code | Meaning |
| --- | --- |
| 0 | a decision was produced |
| 2 | bad input |
| 3 | configuration or credential problem |
| 4 | provider unavailable |
| 5 | provider response failed validation |
| 6 | timeout |
| 7 | an evaluation gate was not met |

Codes 4 and 5 are distinct on purpose: an unreachable provider is an
operational problem, a provider returning nonsense is an integrity problem.

## Using it as a library

```go
import (
    "github.com/howlcipher/howlinstinct/pkg/instinct"
    "github.com/howlcipher/howlinstinct/pkg/receipt"
)
```

`pkg/instinct` has no I/O and no vendor vocabulary. `pkg/receipt` is the
cross-repository contract; other components may also vendor
`schemas/decision-receipt.schema.json` and validate against it.

## Documentation

| Document | What it covers |
| --- | --- |
| [docs/PHILOSOPHY.md](docs/PHILOSOPHY.md) | the ideas, stated plainly enough to argue with |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | layers, flow, validation, determinism |
| [docs/DECISION_RECEIPTS.md](docs/DECISION_RECEIPTS.md) | the receipt contract and what hashing does not prove |
| [docs/EVALUATION.md](docs/EVALUATION.md) | metrics, guards, datasets, gating |
| [docs/PROVIDERS.md](docs/PROVIDERS.md) | adapters, the wire contract, licensing boundary |
| [docs/SECURITY.md](docs/SECURITY.md) | threat model and deliberate non-controls |
| [docs/HOWL_INTEGRATION.md](docs/HOWL_INTEGRATION.md) | proposed contracts with sibling components |
| [documentation/TESTING.md](documentation/TESTING.md) | tiers and exact commands |
| [documentation/data_flows.md](documentation/data_flows.md) | every network egress point |
| [documentation/adr/](documentation/adr/) | architecture decision records |
| [change_log.md](change_log.md) | notable changes |

## Status

v0.1.0, and honest about it. What works: the decision core, both providers,
batching, receipts, the evaluation harness, the CLI. What is deferred and
named as deferred: receipt signing, an OpenTelemetry exporter, and everything
in the boundary section of [docs/PHILOSOPHY.md](docs/PHILOSOPHY.md).

The shipped evaluation numbers measure a word-overlap baseline and say nothing
about any real provider's fitness for your traffic. Build your own suite from
your own data before trusting a decision class with anything that matters.

## License

Apache-2.0. See [LICENSE](LICENSE).

**That license covers this source code only.** It grants no rights to any
model, weights, inference endpoint, or hosted service you configure
HowlInstinct to call; those carry their own licenses and terms. See
[NOTICE](NOTICE).
