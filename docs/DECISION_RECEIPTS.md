# Decision Receipts

A decision receipt is what survives a decision. It is designed to be stored,
attached to an audit trail, and read back long after the state that produced
it is gone.

Schema: `howlinstinct.decision_receipt/v1`, published at
`schemas/decision-receipt.schema.json`.

## Shape

One receipt describes one question's judgment. A batch produces several
receipts sharing a `batch_id`, rather than one receipt describing many
judgments, because receipts are meant to be filed and referenced individually.

```json
{
  "schema": "howlinstinct.decision_receipt/v1",
  "receipt_version": 1,
  "decision_id": "dec_59c03193739635960cf5a43265d2c10b",
  "batch_id": "dec_c8b0f29c7d063d5b02551be46824839a",
  "timestamp": "2026-09-22T04:23:24.313431544Z",
  "question_id": "category",
  "decision_type": "classify",
  "compiled_to": "choice",
  "state_hash": "sha256:004f80c67ffa3e615d8dc37e75a32f1b89046d6b0b844f2a1a183e2c1b8e714a",
  "question_hash": "sha256:d3f3f2f5100ddaf1b7a5a97fc07c95d624ccebaadc0a30d4f69503bf3322fab1",
  "outcome": "ACCEPTABLE_CONFIDENCE",
  "choice": "outage",
  "probabilities": {
    "billing": 0.0545804794520548,
    "outage": 0.834301614481409,
    "performance": 0.05536325831702544,
    "unknown": 0.05575464774951076
  },
  "provider_confidence": 0.834301614481409,
  "instinct_margin": 0.7785469667318983,
  "needs_escalation": false,
  "provider": "mock",
  "provider_model": "mock-lexical-baseline-v1",
  "latency_ms": 0,
  "usage": { "input_tokens": 28, "output_tokens": 0 },
  "correlation_id": "incident-4021"
}
```

That is real output, not an illustration. Note what is *not* there: no
`state`, because it was not retained; no `provider_endpoint`, because the mock
reaches nothing; and, on a noul receipt, no `provider_confidence` at all.

## What is deliberately absent

**The state.** Only `state_hash` is recorded. State is the material being
judged and routinely contains logs, customer records, or credentials, and a
receipt is durable. A caller who genuinely needs the text can opt in per
request:

```sh
howlinstinct decide --retain-state ...
```

Think about that before using it. Retention turns every receipt into a copy of
whatever you judged, in whatever you store receipts in, for as long as you keep
them.

**Credentials.** `provider_endpoint` is rebuilt from scheme, host, and path
only. That is an allow-list rather than a blocklist: a credential in userinfo,
in a query string, or in a component nobody has thought of yet cannot survive
it, because only three components are carried over.

**A confidence nobody reported.** A noul carries no confidence under the
upstream contract, and the field is then absent from the receipt entirely,
never present as `0.0`. Emitting zero would assert that the provider was
maximally unconfident when it said nothing at all. Consumers must treat
`provider_confidence` as optional and must not default it.

## The two numbers, and why they are named differently

| Field | Origin | Meaning |
| --- | --- | --- |
| `provider_confidence` | the provider | whatever the provider reported, preserved exactly, absent if it reported none |
| `instinct_margin` | HowlInstinct | how decisively the distribution favours the selected value |

`instinct_margin` is defined as:

```
noul             |2 * P(yes) - 1|          0 is a coin flip, 1 is decisive
choice/classify  p(top) - p(runner-up)
score            p(modal level) - p(runner-up level)
```

It is a restatement of the probabilities and nothing more. It is not a
calibration result, not an accuracy estimate, and not a stand-in for a
confidence the provider declined to give. It exists so that a caller gating on
decisiveness has something well-defined to gate on for question types that
carry no confidence at all.

Neither value is ever converted into the other.

## Hashing and canonicalization

Digests are labelled SHA-256 over canonical bytes:

```
state_hash    = "sha256:" + hex(SHA-256(state))
question_hash = "sha256:" + hex(SHA-256(canonical(question)))
```

Canonical form sorts object keys at every depth and emits no insignificant
whitespace. Numbers are written exactly as the JSON encoder produced them,
which is the shortest representation that round-trips to the same float64, so
canonicalization is both deterministic and lossless. Non-finite floats are
rejected rather than encoded: NaN and the infinities have no JSON
representation, and inventing one would produce a digest no other
implementation could reproduce.

Two consequences worth knowing:

- Building the same question map in a different insertion order produces the
  same digest, because a map has no order.
- Reversing a score's levels produces a *different* digest, because the order
  of levels is the scale.

`decision_id`, `timestamp`, and `latency_ms` are excluded from the hashes.
They are genuinely volatile, and including them would mean no two runs of the
same decision could ever be compared.

`decision_id` is random rather than derived from content. Two identical
questions asked about identical state at different times are two decisions,
and giving them one identifier would make an audit trail understate how many
judgments were made.

## What hashing does and does not prove

Hashing gives **integrity of content**: if you hold a receipt and the original
state, you can prove the receipt describes that state.

It does **not** give **authenticity**. Anyone who can write a receipt can
write a self-consistent one. Nothing here proves a receipt came from your
HowlInstinct, or that it has not been replaced wholesale.

Signing is deferred, and named as deferred rather than implied. If you need
authenticity today, sign receipts at the boundary where you store them.

## Consuming receipts from another repository

The ecosystem convention is to vendor a byte-for-byte copy of the schema,
pinned to a commit, with a `SOURCE.md` recording where it came from:

```
contracts/howlinstinct/
├── SOURCE.md
└── howlinstinct.decision_receipt.v1.schema.json
```

`SOURCE.md` should record the source repository, the pinned commit, the source
path, the date it was vendored, and how to re-vendor it.

Go consumers can import the type directly:

```go
import "github.com/howlcipher/howlinstinct/pkg/receipt"

r, err := receipt.Parse(reader)   // validates schema id, then fields
```

A test in this repository reflects over the Go struct and fails if it drifts
from the published schema, so the two cannot diverge unnoticed.

## Versioning

The version lives in the `$id` and is echoed inside every instance as a
const-pinned `schema` property, so a receipt found on disk with no surrounding
context still says what it is.

Additive changes that keep existing documents valid stay at `v1`. Anything
that would invalidate a stored `v1` receipt gets a new `$id`, and both are
served until consumers have moved.
