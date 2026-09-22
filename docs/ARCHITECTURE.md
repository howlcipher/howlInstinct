# Architecture

## Shape

Four layers, strictly one-directional. Nothing above the adapter boundary may
name a vendor, a model, an endpoint, or a wire format.

```
  pkg/instinct        Public API: types, Provider interface, validation, limits.
        |             No I/O. No vendor vocabulary. No thresholds.
        v
  internal/decision   Orchestrate: validate, lower, ask, evaluate the caller's
        |             rule, build receipts. No policy of its own.
        v
  internal/provider   Adapters. jev speaks HTTP; mock decides offline.
                      Vendor concepts exist ONLY here.

  pkg/receipt         Canonicalization, SHA-256, the published schema.
  internal/eval       Datasets and metrics.
  internal/config     Layered resolution. Never holds a credential.
  internal/logging    Structured output that withholds by default.
```

`examples/dogfood/policy` sits outside all of it. It is the mock policy
consumer, and it holds every threshold and every action, which is the point.

## Why the layers are where they are

**`pkg/instinct` has no I/O.** It is the contract, so it must be importable by
a sibling that wants the types without inheriting an HTTP client, a
configuration system, or a logger.

**`pkg/receipt` is public, `internal/decision` is not.** The receipt is the
cross-repository contract; the orchestration is an implementation detail.
HowlPlane is itself Go, so leaving the receipt type under `internal/` would
have forced every sibling to reparse JSON rather than import the contract.

**Provider adapters are internal.** A caller selects a provider by name
through configuration. Exporting the adapters would invite callers to depend
on a specific one, which is what the `Provider` interface exists to prevent.

## The `Provider` interface

```go
type Provider interface {
    Name() string
    Decide(ctx context.Context, req Request) (Response, error)
}
```

That is the whole of it. An adapter translates a request into whatever its
endpoint speaks, validates what comes back, and returns judgments.

It does not consult a threshold, decide policy, or escalate. Escalation is
evaluated above the provider layer from the caller's own rule, so that no
provider can quietly become the thing that decides what is confident enough.

## Decision types

Three are provider primitives:

| Type | Question | Returns |
| --- | --- | --- |
| `noul` | bounded yes/no | P(yes). No separate confidence: the probability is the uncertainty signal. |
| `choice` | pick one of a finite set | the selection, a distribution, and a confidence |
| `score` | place on an ordinal scale the caller defines | a probability-weighted level index, the legend, a distribution, a confidence |

The fourth, `classify`, is not a provider primitive. It is HowlInstinct
vocabulary, lowered to `choice` by `internal/decision` before any request
reaches an adapter. No adapter has a case for it and none needs one. The
receipt records `decision_type: classify` alongside `compiled_to: choice`, so
provenance survives the lowering and a stored receipt still describes the
question the caller actually asked.

Adding a fourth provider primitive is deliberately out of scope. A primitive
the provider cannot express would have to be faked somewhere, and faking it
means inventing uncertainty.

## Request flow

```
  caller
    |
    |  Request{state, questions, escalation?, correlation_id?}
    v
  Engine.Decide
    |
    +-- validate against limits          reject before any network call
    +-- lower classify -> choice         provider never sees a 4th primitive
    +-- bound with the caller's deadline
    |
    v
  Provider.Decide
    |
    +-- build the wire request           state stays in its own field
    +-- HTTP, with bounded retries
    +-- validate the response            identity first, then values
    |
    v
  Engine
    |
    +-- restore the caller's vocabulary
    +-- evaluate the caller's rule       no rule means no escalation
    +-- build receipts                   hashes, not content
    v
  Result{Response, Receipts, BatchID}
```

The order of the two validations matters. Requests are rejected before a
provider is contacted, so a malformed batch costs nothing. Responses are
validated before a value enters a judgment, so a malformed answer never
reaches a receipt.

## Response validation

Provider output is untrusted input. That a response came from a service
advertising type-safe structured output is not evidence that it is well
formed, in range, internally consistent, or answering the question that was
asked.

The pipeline, in order:

1. HTTP status, mapped to a classified error.
2. Body read through a byte cap.
3. JSON decode, rejecting unknown fields by default.
4. **Identity**: the answered question ids must equal the asked ids exactly.
5. Per-answer shape must match the type that was asked.
6. Every float finite; every probability within `[0,1]`.
7. Distributions sum to 1 within a small tolerance.
8. Scores within the scale the levels define; legends matching the levels asked.

Step 4 does more work than it looks like. Without it, a buggy or hostile
endpoint could answer a different question while the receipt still claimed the
original was the one judged, and it also catches a provider silently dropping
half a batch.

## Errors

Errors are classified by what the caller should understand about them, not by
which function produced them:

| Kind | Meaning | Exit |
| --- | --- | --- |
| `invalid_input` | the request was malformed or out of bounds | 2 |
| `configuration` | endpoint, provider, or credential is wrong | 3 |
| `provider_unavailable` | could not reach or be served | 4 |
| `provider_response_invalid` | answered, but the answer failed validation | 5 |
| `timeout` | the deadline elapsed | 6 |

The last two are kept apart on purpose. An unreachable provider is an
operational problem; a provider returning malformed or mismatched answers is a
correctness problem, and it deserves a different alarm.

## Concurrency

There is none. A decision is one call; a batch is one call carrying several
questions. The evaluation harness runs cases sequentially, because a parallel
harness against a rate-limited service measures the rate limiter rather than
the model.

Batching at the provider is the intended way to do several judgments at once,
which is also what the underlying contract is built for.

## Determinism

Two things are guaranteed to be reproducible, and both are tested:

- **Receipt hashes.** Canonicalization sorts object keys at every depth and
  emits no insignificant whitespace, so a digest never depends on Go's
  randomized map ordering. Ordered data stays ordered: reversing a score's
  levels is a different question and produces a different digest.
- **The mock provider.** Same input, same output, forever. It is the backbone
  of the offline test suite, and if it drifted, every test that depends on it
  would become mysteriously flaky.

Volatile fields, `decision_id`, `timestamp`, and `latency_ms`, are present in
receipts and excluded from every hash.

## Dependencies

Three direct, all in the ecosystem's existing dependency set:

| Module | Why | License |
| --- | --- | --- |
| `github.com/spf13/cobra` | CLI command tree, matching HowlPlane | Apache-2.0 |
| `github.com/pelletier/go-toml/v2` | operator config, matching the ecosystem's TOML convention | MIT |
| `gopkg.in/yaml.v3` | evaluation datasets, which are hand-written and read better as YAML | MIT / Apache-2.0 |

Everything else is the standard library. There is no vendor SDK: `net/http` is
sufficient for the Jev contract, and taking an SDK would have pulled a vendor's
vocabulary into a codebase that exists partly to keep it out.
