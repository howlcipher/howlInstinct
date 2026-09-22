# Providers

A provider is anything that can answer a bounded question. The core is
provider-neutral: nothing outside `internal/provider` names a vendor, a model,
an endpoint, or a wire format.

```
HowlInstinct
      |
      +-- mock                deterministic, offline, shipped, the default
      |
      +-- jev                 any Jev-compatible /v1/systemone endpoint
      |     +-- OpenJev
      |     +-- a hosted Jev service
      |     +-- a local server
      |
      +-- (future) a local provider
      +-- (future) a structured-LLM fallback
```

## mock

The default. A deterministic lexical baseline that needs no network, no
credentials, and no inference hardware, which is why a fresh checkout does
something useful immediately and why the whole test suite runs offline.

**It is not a semantic model.** Its judgments come from counting shared word
stems between the state and each candidate label. It understands nothing. Any
number produced against it measures the harness, not a model.

It is still worth having as more than a stub. A lexical baseline is a
legitimate control: a real provider that cannot beat word-overlap on your
cases is not doing what you are paying it for. On the shipped suites, the mock
scores at or below chance on the semantic ones, which is the correct result.

It also reproduces one quirk of the real contract deliberately: a noul answer
carries no confidence. Reproducing that absence offline is what keeps the rest
of the system honest about handling it, instead of discovering the gap against
a live endpoint.

## jev

Speaks the Jev-compatible System One contract: `POST /v1/systemone` with
typed noul, choice, and score questions.

The adapter is generic over the endpoint. OpenJev, a hosted Jev service, a
local server, or any future compatible implementation are reached by
configuration alone.

```toml
provider    = "jev"
base_url    = "https://api.example.com"
model       = "jev-latest"
api_key_env = "MY_PROVIDER_API_KEY"
timeout     = "20s"
max_retries = 2
```

There is deliberately **no default endpoint**. Defaulting to a vendor's
hostname would make a hosted service the implicit destination of whatever
state you judge, which is not a decision a library should make on your behalf.

### The wire contract, as implemented

Request:

```json
{
  "state": "...",
  "model": "jev-latest",
  "questions": {
    "urgent":   {"type": "noul",   "instructions": "...", "criteria": {"true": "...", "false": "..."}},
    "category": {"type": "choice", "instructions": "...", "criteria": {"billing": "...", "outage": null}},
    "severity": {"type": "score",  "instructions": "...", "criteria": ["low", "moderate", "high"]}
  }
}
```

`criteria` has a different shape per type, and the adapter matches the
protocol rather than tidying it up: an object of meanings for noul, an object
of option descriptions for choice, and an **ordered array** for score, where
position is the scale.

Response:

```json
{
  "model": "...",
  "answers": {
    "urgent":   {"noul": 0.97},
    "category": {"choice": "outage", "probabilities": {...}, "confidence": 0.96},
    "severity": {"score": 1.7, "legend": [...], "probabilities": [...], "confidence": 0.75}
  },
  "usage": {"input_tokens": 120, "output_tokens": 0}
}
```

Note the asymmetry: choice and score carry `confidence`, and noul does not.
Some implementations emit one for noul anyway. HowlInstinct preserves it if
present and never invents it if absent.

### Response validation

Everything from the endpoint is untrusted. A service advertising type-safe
structured output is still a network peer, and "it came from an AI endpoint"
is not a reason to skip validation. See ARCHITECTURE.md for the ordered
pipeline; the short version is that identity is checked before values, so a
response answering a question nobody asked is rejected before any number is
read.

### Strict schema

By default the adapter rejects responses carrying fields it does not know.

The trade-off is real and goes both ways. Strict means a provider adding a
harmless field breaks this adapter until it is updated. Permissive means a
provider renaming a field we rely on degrades silently into judgments built
from zero values. For a component whose premise is that provider output is
untrusted, failing loudly is the consistent choice, and `strict_schema = false`
is there for operators who need the other trade-off.

### Retries

Only failures that could plausibly succeed on a second attempt are repeated:
rate limiting (429), gateway errors (502, 503, 504), overload (529), and
transport failures where the request may never have arrived.

Never repeated: a rejected request (400, 422), a bad credential (401, 403), an
unqualified 500, or any response that failed validation. Retrying those cannot
succeed and only adds load to a service that is often already struggling.

Backoff is exponential with full jitter, capped, and bounded absolutely by the
caller's context deadline. `Retry-After` is honoured up to five seconds; a
hostile or broken provider cannot park a caller for hours by asking nicely.

## Running a local endpoint

Local inference is supported and not assumed. Commodity hardware may not run a
current open model well, and HowlInstinct ships no weights and embeds no
inference engine on purpose: it is decision infrastructure, not an inference
engine.

Point it at whatever you are running:

```toml
provider = "jev"
base_url = "http://127.0.0.1:8080"
# api_key_env is usually unnecessary for a local server
```

`howlinstinct doctor` will confirm plaintext HTTP on loopback is fine, and
warn if the same setting points somewhere remote.

## Adding a provider

1. Implement `instinct.Provider` in `internal/provider/<name>`.
2. Validate every response as untrusted input. Reuse the ordered checks in
   `internal/provider/jev/validate.go` as the model.
3. Preserve absence. If your provider reports no confidence, leave the pointer
   nil; never substitute a probability.
4. Do not consult a threshold, escalate, or decide policy. That belongs above
   the provider layer.
5. Register it in `pkg/cli/provider.go` and `internal/config`.
6. Test it against `httptest`, not a live service.

## Licensing

HowlInstinct's source is Apache-2.0. **That license covers this source code
only.**

It grants no rights to any model, set of weights, inference endpoint, or
hosted service you configure it to call. Those carry their own licenses and
terms of service, and complying with them is the operator's responsibility.
HowlInstinct ships no weights and embeds no inference engine, so nothing in
this repository gives you permission to use anything it can talk to.

See `NOTICE` for the third-party source dependencies and their licenses.
