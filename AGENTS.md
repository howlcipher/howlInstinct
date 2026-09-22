# Engineering Context for HowlInstinct

Rules for any agent or person working in this repository. This file is
canonical; `CLAUDE.md` and any other per-agent entry point import it.

## What this component is

HowlInstinct answers small bounded questions about supplied state and returns
typed judgments with explicit uncertainty and provenance. It is not an agent.

Read [docs/PHILOSOPHY.md](docs/PHILOSOPHY.md) before changing anything. Most
of the unusual choices here are downstream of it.

## Invariants

These are not style preferences. Violating one is a defect regardless of what
else the change achieves.

1. **Confidence is not permission.** No threshold, default, or fallback cutoff
   exists in this codebase. Escalation is evaluated only against a rule the
   caller supplied. An architecture test parses the source and enforces this.

2. **No action vocabulary.** HowlInstinct cannot deploy, page, merge, approve,
   deny, roll back, retry a workflow, or select a different provider. Anything
   that decides what should happen belongs in the caller.

3. **Absence is not zero.** Every provider-supplied number is a pointer. A
   value the provider did not report stays absent in the type, the JSON, and
   the receipt. Never substitute a probability for a confidence.

4. **Two numbers, two names.** `provider_confidence` is what the provider
   said. `instinct_margin` is what we computed. Never convert one into the
   other, and never let a new derived value borrow either name.

5. **No vendor vocabulary above the adapter boundary.** No model name, vendor,
   hostname, or wire format outside `internal/provider`.

6. **Provider output is untrusted.** Validate structure, identity, types,
   ranges, and distributions. That a response came from a service advertising
   type safety is not evidence about the bytes on the wire.

7. **Metrics decline rather than guess.** Report `null` plus a reason when the
   data cannot support a metric. Zero is a measurement; unknown is not.

8. **The default test suite runs offline.** No network, credentials, or
   inference hardware. Live tests live behind `-tags live` and gate nothing.

9. **No sibling repository is modified from here.** Integration contracts are
   proposals in `docs/HOWL_INTEGRATION.md`, to be accepted by their owners.

## Formatting

Use standard hyphens and dashes where grammatically correct or syntactically
required. Do not use them as decorative punctuation.

(HowlPlane carries a stricter rule forbidding them entirely. That rule
contradicts HowlPlane's own `AGENTS.md`, its own files ignore it, and its bug
backlog records the damage it caused. It is not carried here.)

## Conventions

- Go 1.26+. Three direct dependencies; a fourth needs justification in the
  pull request, including the standard library alternative considered and the
  licence.
- Schema-bound documents follow the ecosystem convention: a versioned `$id`,
  echoed inside every instance as a const-pinned property, with
  `Load` / `Parse` / `Validate` on the Go type, and the schema check first.
- Named string enums with a package-level validity map and `IsValid()`.
- snake_case JSON tags; `omitempty` only on genuinely optional fields.
- Errors wrapped with `%w` and a lowercase context phrase. Classified errors
  only where a caller must branch on the kind.
- Comments explain why. The why is usually a property being protected.
- `os.Exit` only in `main`, and only from a function holding no defers.
- Command output goes through the cobra command's own writers, never
  `os.Stdout` directly, so tests can capture it.

## Before declaring a change complete

Run `make check`. Record a test impact assessment as described in
[documentation/TESTING.md](documentation/TESTING.md): what behaviour changed,
which tests prove it, what coverage is missing, and what you actually ran.

Reported results must correspond to the final state of the working tree. Any
change to source, tests, configuration, or CI after the last run invalidates
that run.

## Grounding

1. If the answer is in this repository, use the file and name it.
2. If it needs live data, check it. Do not estimate.
3. If neither applies, say so rather than guessing.

When live evidence conflicts with a document here, prefer the live evidence,
say so explicitly, and flag the document for correction. That is not a
courtesy; a component whose entire job is reporting uncertainty honestly
cannot have documentation that quietly overstates what it knows.
