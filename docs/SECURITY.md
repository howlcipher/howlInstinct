# Security

HowlInstinct sits between a caller's state and a remote service that judges
it. That places it on the path of two things worth protecting: the material
being judged, which is often sensitive, and the credential used to reach the
provider.

Reporting a vulnerability: open an issue at
https://github.com/howlcipher/howlinstinct/issues, or contact the repository
owner privately if the issue should not be public first.

## Threat model

| Threat | Control |
| --- | --- |
| Prompt or state injection | State travels in its own wire field and is never concatenated into a question's instructions. Caller-supplied criteria are bounded in count and length and cannot introduce a new primitive. |
| Malicious caller-supplied criteria | Strict validation before any network call: known types, bounded sizes, unique option and level names, identifiers matching a fixed pattern. |
| Oversized input | Explicit caps on state bytes, question count, instruction length, option count, level count, and total request document size. |
| Provider response spoofing | Answered question ids must equal asked ids exactly. A response answering a question nobody asked is rejected before any value is read. |
| Credential leakage | The key exists only as an environment variable name in configuration, and only in an `Authorization` header at call time. Redaction is enforced in the log handler, and receipt endpoints are rebuilt from scheme, host, and path. |
| Receipt tampering | Digests over canonical bytes give integrity of content. They do **not** give authenticity. See below. |
| Untrusted endpoint configuration | `doctor` warns on plaintext HTTP to a remote host, credentials embedded in a URL, and unset credential variables. |
| Denial of service via batch size | Question count is capped, and a batch is one provider call rather than a fan-out. |
| Retry amplification | Only plausibly transient failures are retried, capped, with full jitter, and bounded absolutely by the caller's deadline. `Retry-After` is honoured only up to five seconds. |
| Sensitive state persistence | State is hashed, never stored, unless the caller explicitly opts in per request. |
| Malformed probability distributions | Every value checked finite and within `[0,1]`; distributions must sum to 1 within `1e-6`; distribution keys must exactly match the options asked. |
| NaN and infinity | Rejected wherever they can enter: validation, canonicalization, and escalation thresholds. |
| Out-of-range scores | Rejected against the scale the caller's own levels define. |
| Schema drift | Unknown response fields are rejected by default. A test fails if the Go receipt struct drifts from the published schema. |
| Log injection from provider text | Provider error text is stripped of control characters and truncated before entering an error string. |

## Provider output is untrusted input

That a response came from a service advertising type-safe structured output is
not evidence that it is well formed, in range, internally consistent, or
answering the question that was asked. Type safety is a property of the
producer's implementation, not of the bytes on the wire, and nothing about a
response's origin makes it authentic.

Everything is validated. See ARCHITECTURE.md for the ordered pipeline.

## Credential handling

The rule is that **a credential is never a configuration value**.
Configuration names the environment variable that holds the key.

```toml
api_key_env = "MY_PROVIDER_API_KEY"   # the NAME, never the key
```

This is what makes a resolved configuration safe to print in a log, a bug
report, or a support ticket. `howlinstinct doctor` prints the variable's name
and whether it is populated, never its value.

The key is read at call time, placed directly into an `Authorization` header,
and never written to a config file, accepted as a flag, logged, or stored in a
receipt. Tests assert all of this against a known sentinel value.

`provider_endpoint` in a receipt is rebuilt from scheme, host, and path. That
is an allow-list, not a blocklist: a credential in userinfo, a query string,
or a URL component nobody has thought of yet cannot survive it, because only
those three components are carried forward.

## Logging

Redaction is enforced in the log handler rather than at call sites, because a
rule enforced at call sites survives exactly until someone adds a new one.

Two classes of value are withheld regardless of how they are passed:

- anything whose attribute name looks like a credential (`api_key`,
  `authorization`, `token`, `secret`, `password`, `cookie`, and similar);
- raw state, which is marked `[WITHHELD: state is not logged]` rather than
  `[REDACTED]`, so a reader can tell "this was a secret" from "this was the
  judged material, which we do not persist".

Group nesting does not provide a way around the filter.

## Resource bounds

Upstream documents a cap on choice options and score levels, and documents no
cap at all on question count or state size. Relying on a remote service to
bound local resource use would mean an unbounded local footprint whenever that
service is permissive, misconfigured, or hostile, so the limits are Howl's
own and explicit:

| Limit | Default |
| --- | --- |
| State | 128 KiB |
| Questions per request | 32 |
| Instructions per question | 4 KiB |
| Option or level label | 256 B |
| Question identifier | 64 B |
| Choice options | 255 |
| Score levels | 2 to 10 |
| Response body | 4 MiB |
| Request document | state limit plus 256 KiB |
| Per-call deadline | 20s (config), 30s (library default) |
| Retries | 2 |

## What hashing does not give you

Receipt digests prove **integrity of content**: given a receipt and the
original state, you can prove the receipt describes that state.

They do not prove **authenticity**. Anyone able to write a receipt can write a
self-consistent one, and nothing here proves a receipt came from your
HowlInstinct or has not been replaced wholesale.

Receipt signing is deferred. It is named as deferred rather than implied,
because a hash that looks like a security property invites people to rely on
one it does not have. If you need authenticity now, sign receipts at the
boundary where you store them.

## Deliberate non-controls

Stated so nobody assumes otherwise:

- **No authentication or authorization of callers.** HowlInstinct is a library
  and a CLI, not a service. Anyone who can run the binary can ask it anything.
- **No rate limiting of callers.** Bounds are per request.
- **No encryption at rest.** Receipts are written wherever the caller puts them.
- **No egress allow-list.** The configured endpoint is called. `doctor` warns
  about risky endpoints; it does not refuse them.
- **No sandboxing of provider responses beyond validation.** Responses are
  parsed and validated, never executed, but they are not otherwise confined.

## Test coverage for these controls

The claims above are tested, not asserted:

- a URL with embedded credentials and a query-string token, checked against
  receipt JSON, log output, and the `doctor` and `Describe` renderings;
- a provider answering a question nobody asked, dropping half a batch,
  mixing answer types, returning distributions that do not sum to 1, naming
  options that do not exist, and returning out-of-range and non-finite values;
- oversized bodies, oversized documents, and oversized states;
- retry counts per status code, and a deadline bounding the retry loop;
- a noul whose absent confidence must serialize as absent rather than zero;
- duplicate question identifiers in a JSON request;
- control characters in provider error text.
