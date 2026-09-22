# Data Flows and Network Egress Reference

**Repository:** `howlcipher/howlinstinct`
**Purpose:** Complete map of network egress, local data handling, and
third-party integration points.

## Overview and privacy principles

HowlInstinct is offline by default. The default provider is `mock`, which
makes **zero network requests**, needs no credentials, and runs entirely in
process. A fresh install, the full test suite, the evaluation harness, and the
dogfood example all work with no network access at all.

Egress happens only when an operator explicitly configures a remote provider.
There is no default endpoint and no fallback host: defaulting to a vendor's
hostname would make a hosted service the implicit destination of whatever
state is being judged.

When egress is configured, understand what leaves: **the state being judged is
sent to the provider in full.** That is the material the judgment is about,
and it routinely contains logs, customer records, or credentials. HowlInstinct
bounds it, never logs it, and never stores it, but it does send it, because
sending it is the operation.

## Network egress integration points

| Integration Point | Trigger Condition | Data Sent | Destination | Configuration Gating | Default Status |
| --- | --- | --- | --- | --- | --- |
| **Jev-compatible provider** | Any `decide` or `eval` call while `provider = "jev"` | The raw state, question instructions, option and level names and descriptions, and the model identifier | The operator's configured `base_url` | `provider = "jev"` AND `base_url` set. No default endpoint exists. | **Off.** Default provider is `mock`. |
| **Provider credential** | Same call, when `api_key_env` is set | A bearer token, in an `Authorization` header only | Same endpoint | `api_key_env` names an environment variable that is populated | Off |
| **Mock provider** | Any `decide` or `eval` call while `provider = "mock"` | Nothing. No socket is opened. | None | Default | **On** |
| **Go module proxy** | `go build`, `go test`, `go install` at development time | Module paths and versions | `proxy.golang.org`, or whatever `GOPROXY` names | Go toolchain, not HowlInstinct | Development only |
| **GitHub Actions** | Push or pull request on this repository | Source and CI logs | GitHub | Repository CI | CI only |

There is no telemetry, no analytics, no crash reporting, no update check, and
no usage reporting. HowlInstinct contacts nothing on its own initiative; every
request is the direct result of a call the caller made.

### When zero requests are made

With the default configuration, `howlinstinct decide`, `eval`, `providers`,
`doctor`, and `version` perform **zero network requests**, as does the entire
test suite and `examples/dogfood`. `doctor` reports resolved configuration by
inspecting local state only; it does not probe the configured endpoint.

## Local data stores

HowlInstinct writes nothing to disk on its own. It has no database, no cache,
no state directory, and no log file. Everything below is either read-only or
written by the caller.

1. **Operator configuration** — `~/.config/howlinstinct/config.toml`, or a
   path named by `HOWLINSTINCT_LOCAL_CONFIG`. Read only. Contains no secrets
   by design: it names the environment variable holding a credential, never
   the credential.

2. **Decision receipts** — emitted on stdout. HowlInstinct does not persist
   them; where they end up is the caller's decision. By default they contain a
   SHA-256 hash of the state rather than the state, and an endpoint rebuilt
   from scheme, host, and path so no credential can ride along. Passing
   `--retain-state` puts the raw state into the receipt, which is why it is
   opt-in per request.

3. **Evaluation datasets** — `evals/**/cases.yaml`, read only, committed to
   the repository.

4. **Evaluation reports** — emitted on stdout. Contain metrics, the dataset
   path and digest, the provider, and the model. They do **not** contain case
   states or judgments, only aggregates and failure reasons.

5. **Structured logs** — stderr, when a caller supplies a logger. Credentials
   are redacted and raw state is withheld, both enforced in the log handler
   rather than at call sites.

## Credential flow

```
environment variable (named by api_key_env)
        |
        |  read at call time, never cached
        v
Authorization: Bearer <key>          <- the only place the key ever appears
        |
        v
configured endpoint over TLS (or plaintext, if the operator chose that;
                              doctor warns when the host is remote)
```

The key never enters a config file, a flag, a log line, a receipt, an error
message, or a request URL. Tests assert each of these against a sentinel.

## Operator guidance

- Prefer HTTPS for any remote endpoint. `doctor` warns about plaintext HTTP to
  a remote host, because the state would cross the network in the clear.
- Consider what is in your state before configuring a hosted provider. The
  state is sent in full, and the provider's terms, retention, and jurisdiction
  apply to it, not this repository's license.
- Use `--retain-state` deliberately. It turns every receipt into a copy of
  whatever you judged, wherever you store receipts, for as long as you keep
  them.
- Run `howlinstinct doctor` after changing configuration. Its output is safe to
  paste into a ticket.
