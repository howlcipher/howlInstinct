# Change Log

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/), and this
project adheres to semantic versioning.

## [Unreleased]

## [0.1.0] - 2026-09-22

Initial bootstrap: the decision core, two providers, receipts, the evaluation
harness, and the CLI.

### Added

- **Decision core** (`pkg/instinct`). Four decision types, `noul`, `choice`,
  `score`, and `classify`, with strict validation and explicit resource
  limits. Every provider-supplied number is a pointer, so that a value the
  provider never reported serializes as absent rather than as zero.

- **Escalation as a caller concern.** `EscalationRule` is supplied per
  request. There is no default threshold anywhere in the codebase, and nothing
  escalates without one, however uncertain a judgment is. An architecture test
  parses this repository's own source and fails if a judgment's confidence is
  ever compared against a hardcoded literal.

- **Duplicate-key request decoding.** `encoding/json` keeps the last value for
  a repeated key, so two questions sharing an identifier would otherwise
  collapse into one and return fewer answers than were asked for, with no
  error. The decoder walks the token stream and rejects duplicates anywhere in
  the document.

- **Decision receipts** (`pkg/receipt`), schema
  `howlinstinct.decision_receipt/v1`, published at
  `schemas/decision-receipt.schema.json`. Canonicalization sorts object keys
  at every depth so digests never depend on Go's randomized map ordering.
  State is recorded as a SHA-256 hash unless the caller explicitly opts in.
  Endpoints are rebuilt from scheme, host, and path, which is an allow-list no
  credential can survive.

- **Jev-compatible HTTP adapter** (`internal/provider/jev`). Generic over any
  `/v1/systemone` endpoint, with no vendor SDK and no hostname, model, or
  hardware assumption compiled in. Provider output is treated as untrusted:
  the answered question ids must equal the asked ids exactly before any value
  is read. Retryability is carried by an explicit wrapper rather than inferred
  from an error's kind, so a future error cannot silently become retryable.

- **Deterministic offline mock** (`internal/provider/mock`), the default
  provider, needing no network, credentials, or inference hardware. It
  reproduces the contract's asymmetry deliberately: choice and score carry a
  confidence, a noul does not.

- **Evaluation harness** (`internal/eval`) with accuracy, confusion counts,
  multiclass Brier score, clamped log loss, calibration buckets, mean absolute
  error for ordinal scales, escalation rate, and latency percentiles. A metric
  is reported only when the data supports it and is `null` with a reason
  otherwise. Four demonstration suites ship: routing, change risk, evidence
  sufficiency, and agent run triage.

- **CLI** (`howlinstinct`) with `decide`, `eval`, `providers`, `doctor`, and
  `version`, exposed as a mountable subtree for a future umbrella binary. Exit
  codes report whether the tool worked and never what the answer was: a noul
  answering "no" exits 0.

- **Layered configuration** (`internal/config`): defaults, then an operator
  file, then the environment, then flags. A credential is never a
  configuration value; configuration names the environment variable holding
  the key, which is what makes a resolved configuration safe to print.

- **Redacting structured logger** (`internal/logging`). Redaction is enforced
  in the handler rather than at call sites. State is withheld under a distinct
  marker from credentials, so a reader can tell "this was secret" from "this
  was the judged material".

- **Dogfood example** (`examples/dogfood`) walking an event through three
  primitives into a mock policy consumer that owns every threshold and every
  action.

- Documentation: philosophy, architecture, receipts, evaluation, providers,
  security, Howl integration, testing, data flows, and ADR 0001.

- CI running gofmt, vet, build, the race detector, coverage, every shipped
  evaluation suite, and the dogfood example across three operating systems,
  plus pinned golangci-lint, govulncheck, and CodeQL.

### Notes

- No sibling Howl repository was modified. The integration contracts in
  `docs/HOWL_INTEGRATION.md` are proposals written from one side.
- Every evaluation number produced in this repository measures the shipped
  word-overlap baseline, not any real provider.
