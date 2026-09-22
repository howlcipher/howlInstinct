# Testing

## The rule that shapes everything

**The entire default suite runs offline.** No network, no credentials, no
inference hardware, no GPU. If a test ever needs a secret, something has gone
wrong with the mock provider rather than with the test.

That is not a convenience. A test suite that needs a live service tests the
service's availability as much as the code, and it fails for reasons that have
nothing to do with the change under review.

## Commands

| Goal | Command |
| --- | --- |
| Fast feedback while editing | `go test ./<package>/` |
| The whole suite | `make test` |
| Concurrency safety | `make test-race` |
| Coverage | `make test-coverage` |
| Formatting, vet, and linters | `make lint` |
| Everything CI runs, in order | `make check` |
| Every shipped eval suite | `make evals` |
| The end-to-end demonstration | `make demo` |

`make check` is the gate to run before pushing. It is what CI runs.

## Tiers

There is no marker system, because the suite is small and fast enough not to
need one. Tests are separated by what they touch:

| Tier | Where | What it touches |
| --- | --- | --- |
| Unit | every package | pure logic: validation, canonicalization, metrics, escalation |
| Adapter | `internal/provider/jev` | a real HTTP server via `httptest`, never a remote one |
| Command | `pkg/cli` | the full command tree with captured output and real exit codes |
| Architecture | `examples/dogfood/policy` | parses this repository's own source to enforce a boundary |
| Live | `-tags live` | a real provider. Never part of any gate. |

## Live tests

Gated behind a build tag, so they cannot run by accident:

```sh
export HOWLINSTINCT_LIVE_BASE_URL="https://api.example.com"
export HOWLINSTINCT_LIVE_API_KEY_ENV="MY_PROVIDER_API_KEY"
export MY_PROVIDER_API_KEY="..."

go test -tags live ./internal/provider/jev/
```

A build tag rather than an environment check, because a tag cannot be
satisfied accidentally by a variable that happens to be set in someone's
shell. They are excluded from `make check` and from CI.

## What is deliberately tested

Some tests exist to protect a property rather than a function, and are worth
knowing about before changing the code they guard:

- **Absent confidence stays absent.** A noul judgment must serialize with no
  `provider_confidence` field at all, never as `0.0`. There is a matching test
  that an explicit zero from a provider *is* preserved, so the two cases stay
  distinguishable.
- **Nothing escalates without a caller rule.** Asserted at the type level, the
  engine level, and the CLI level, with deliberately maximally uncertain
  judgments.
- **No threshold lives inside HowlInstinct.** `policy_test.go` parses the
  `pkg/instinct`, `internal/decision`, and `internal/provider/jev` sources and
  fails if a judgment's confidence, probability, or margin is compared against
  a hardcoded literal. It caught a real line on its first run.
- **Receipt digests are stable.** Golden digests are pinned. If they change,
  every previously stored receipt just became unverifiable, and that should
  never happen quietly.
- **Credentials do not leak.** A sentinel key is pushed through logs,
  receipts, `doctor`, config descriptions, and request URLs, and asserted
  absent from all of them.
- **A spoofed response is rejected.** A provider answering a question nobody
  asked, or dropping half a batch, must fail validation rather than produce a
  judgment.
- **Exit codes are a contract.** The table is pinned, including that a noul
  answering "no" still exits 0.
- **The schema matches the struct.** Reflection over JSON tags against the
  published schema, in both directions.

## Test impact assessment

Before declaring a code change complete, record:

- the observable behaviour that changed;
- which existing tests prove it, and whether they still express intended
  behaviour;
- missing happy-path, failure, edge, or authority coverage;
- obsolete or duplicated contracts;
- the smallest tier that can protect the behaviour;
- the tests actually run, and their results.

Treat tests as production infrastructure. Add tests for unprotected
behaviour, remove or rewrite obsolete ones only with evidence, and consolidate
overlapping ones only when equivalent or stronger protection remains.

Final reported results must correspond to the final state of the working tree.
Any change to source, tests, configuration, or CI after the last run
invalidates that run.

## Conventions

- Standard library `testing` only. No assertion framework.
- Table-driven, with `t.Run` subtests named for the behaviour rather than the
  input.
- `t.TempDir()` and `t.Setenv()` for isolation. Configuration tests point
  `HOME` at a temporary directory so a developer's real `~/.config` cannot
  influence a run.
- Failure messages state got and want, and say why it matters when that is
  not obvious.
- `httptest` for anything HTTP. Handlers that block use a bounded timer as
  well as the request context, because `httptest.Server.Close` waits for
  outstanding handlers and a handler that only unblocks on client disconnect
  will hang the suite when that signal is delayed.
