# ADR 0001 — A Bounded Semantic Judgment Layer, Separated From Authority

## Status

Accepted. Implemented in the v0.1.0 bootstrap of `howlcipher/howlinstinct`.

## Context

The Howl ecosystem has orchestration (HowlPlane), a governance and execution
runtime (HowlFrame), and evidence and change components (HowlProof,
HowlChangeOps). It has no layer for small semantic judgments.

Those judgments are being made anyway, in two unsatisfactory ways:

- **As deterministic heuristics.** Keyword rules for routing, regex matching
  for classification, hand-tuned scores for risk. Cheap, auditable, and wrong
  often enough that nobody fully trusts them.
- **By escalating to a generative agent.** Correct more often, but slow,
  expensive, and returning prose that then has to be parsed back into a
  decision, which reintroduces the fragility that motivated the escalation.

Neither produces a typed value with a probability attached, so neither can be
reasoned about statistically, evaluated against labelled data, or recorded in
a way that makes "how often is this right?" an answerable question.

A third option now exists: System One models, which return typed probabilistic
decisions rather than text. The Jev-compatible `/v1/systemone` contract
defines three primitives — a yes/no probability, a selection from a finite
set, and a bounded ordinal score.

The risk in adopting this is not technical. It is that a component which emits
confident-looking numbers becomes, by convention rather than by design, the
thing that decides what is allowed to happen. Once one call site writes
`if confidence > 0.95 { deploy() }`, the boundary is gone and no amount of
documentation brings it back.

## Evidence

| Observation | Source |
| --- | --- |
| The upstream contract defines three primitives, and no fourth | Cross-checked against four independent Jev/OpenJev implementations and write-ups |
| A `noul` answer carries **no** confidence field; `choice` and `score` do | Same, and corroborated in prose: "the probability is already the certainty" |
| At least one implementation emits a noul confidence regardless | Same |
| Upstream documents limits for choice options (1–255) and score levels (2–10) | Same |
| Upstream documents **no** limit on question count or state size | Same |
| Confidence gating appears in every consumer library surveyed, always in the *caller* | e.g. `confidence_gate(answer, threshold=0.6, below="review")` |
| No Howl repository on this machine mentions HowlInstinct, Jev, or System One | Full-tree search of `howlcipher/howlplane` |
| The ecosystem has no LICENSE file or SPDX headers anywhere | Same |

The third and sixth rows drove most of the design. A field that is sometimes
present and sometimes absent, in a system whose whole job is reporting
uncertainty, is exactly where a silent default does the most damage. And the
fact that every surveyed consumer puts the threshold in the caller is evidence
that the boundary this ADR draws is the one practitioners already use.

## Gap analysis

| New abstraction | Extends | Why an existing representation is insufficient |
| --- | --- | --- |
| `Judgment` | nothing in the ecosystem | No existing type carries a typed value with its probability, its provenance, and the possibility that a confidence is genuinely absent. |
| `DecisionReceipt` | the ecosystem's schema-bound document convention | Follows `ai.run_result/v1` and its siblings in structure and versioning, but records a probabilistic judgment and hashes its inputs rather than storing them. |
| `EscalationRule` | nothing | The mechanism by which a caller's threshold enters, is evaluated, and is recorded, without HowlInstinct ever owning one. |
| `instinct_margin` | nothing | A well-defined decisiveness measure for question types that carry no provider confidence, named separately so it can never be mistaken for one. |

## Rejection of speculative abstractions

- **No fourth provider primitive.** `classify` is sugar over `choice`, lowered
  before any adapter sees it. A primitive the provider cannot express would
  have to be faked, and faking it means inventing uncertainty.
- **No agent, planner, or workflow engine.** HowlPlane owns orchestration.
- **No authority mechanism.** HowlFrame owns governance. HowlInstinct has no
  vocabulary for approval, denial, or action.
- **No embedded inference.** No weights ship, no engine is embedded. This is
  decision infrastructure, not an inference engine, and bundling one would
  make the component's footprint dominated by a concern it does not own.
- **No vendor SDK.** `net/http` is sufficient for the contract, and an SDK
  would pull vendor vocabulary into a codebase built to keep it out.
- **No caller authentication, rate limiting, or persistence.** It is a library
  and a CLI, not a service.

## Decision

1. Build HowlInstinct in Go, matching HowlPlane's toolchain and conventions.
2. Support four decision types: `noul`, `choice`, `score`, and `classify`,
   where `classify` lowers to `choice` before reaching any provider, with the
   lowering recorded in the receipt.
3. Keep the core provider-neutral. No vendor, model, endpoint, or wire format
   may be named above the adapter boundary.
4. Ship two adapters: a Jev-compatible HTTP adapter generic over any
   `/v1/systemone` endpoint, and a deterministic offline mock that is the
   default, so that a fresh install works and the whole suite runs offline.
5. Represent every provider-supplied number as a pointer, so that an absent
   value serializes as absent rather than as a zero nobody reported.
6. Keep `provider_confidence` and `instinct_margin` as separately named
   fields, and never convert one into the other.
7. Own no threshold. Evaluate escalation only against a rule the caller
   supplied, and enforce that with a test that parses this repository's source.
8. Emit versioned decision receipts that hash their inputs rather than storing
   them, with state retention as an explicit per-request opt-in.
9. Ship an evaluation harness that reports a metric only when the data
   supports it, and reports `null` with a reason otherwise.
10. Impose Howl's own resource limits rather than relying on a provider's,
    since upstream documents none for question count or state size.
11. License the source Apache-2.0, and document separately that this grants no
    rights to any model or hosted service.

## Consequences

**Accepted costs.**

- Pointer-heavy judgment types are more verbose at every call site than plain
  floats would be. This is the cost of making absence representable, and it is
  paid deliberately.
- Strict response validation means a provider that adds a harmless field
  breaks this adapter until it is updated. `strict_schema = false` exists for
  operators who need the other trade-off.
- Sequential evaluation is slower than a parallel harness. A parallel harness
  against a rate-limited service measures the rate limiter.
- Receipts prove integrity of content, not authenticity. Signing is deferred
  and named as deferred rather than implied.

**Known limitations.**

- The shipped mock is a word-overlap baseline. Every evaluation number in this
  repository measures the harness, not a model.
- The four golden suites are demonstrations written by one person, few in
  number, and not adversarial. They are not benchmarks.
- No sibling repository has agreed to any of the integration contracts in
  `docs/HOWL_INTEGRATION.md`. They are proposals written from one side.
- The architecture guard test catches a literal threshold. It cannot catch one
  hidden behind a named constant or arriving from configuration.

## Evidence this milestone must emit

Before HowlInstinct is used for any decision that matters:

1. An evaluation suite built from the consumer's own traffic, not the shipped
   demonstrations.
2. A measured baseline for that suite against the mock, establishing what
   word overlap alone achieves.
3. A measured result for a real provider on the same suite, with calibration
   buckets, so that "how often is a 0.8 actually right?" has an answer.
4. A recorded decision, by the consumer, about what threshold they are setting
   and what happens below it.
