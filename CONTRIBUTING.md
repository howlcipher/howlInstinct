# Contributing

## Before you change anything

Read [docs/PHILOSOPHY.md](docs/PHILOSOPHY.md). Most of this codebase's
unusual choices are downstream of it, and a change that looks like a
simplification often turns out to remove a property that was load-bearing.

## The rules that are not negotiable

These exist because the component is only useful if it can be trusted by
things that hold authority.

1. **No threshold in HowlInstinct.** Not a constant, not a fallback, not a
   default in a config file. Escalation is evaluated only against a rule the
   caller supplied. A test parses the source and will fail you.

2. **No action vocabulary.** HowlInstinct cannot deploy, page, merge, approve,
   retry a workflow, or choose a different provider. If a change gives it any
   of those, it belongs in the caller.

3. **Absence is not zero.** A value the provider did not report must stay
   absent: nil pointer, omitted from JSON, missing from the receipt. Never
   substitute a probability for a confidence, or a zero for either.

4. **No vendor vocabulary above the adapter boundary.** No model name,
   hostname, vendor, or wire format outside `internal/provider`.

5. **Provider output is untrusted.** Validate everything, however type-safe
   the provider claims to be.

6. **Metrics decline rather than guess.** If the data cannot support a metric,
   report it as unavailable with a reason. Never emit zero for unknown.

7. **The default suite runs offline.** No network, credentials, or hardware.

## Working on it

```sh
make check      # lint, vet, race detector. Run this before pushing.
make test       # the suite
make demo       # the end-to-end example
make evals      # every shipped evaluation suite
```

Go 1.26 or newer. Three direct dependencies; adding a fourth needs a reason in
the pull request, including what standard library alternative was considered
and what the licence is.

## Style

The repository follows `AGENTS.md` and, where it is silent, ordinary Go
convention:

- `gofmt` clean, `go vet` clean, `golangci-lint` clean.
- Comments explain **why**, not what. The why here is usually a property being
  protected, and it is worth a sentence.
- Table-driven tests, standard library `testing` only, subtests named for the
  behaviour rather than the input.
- Errors wrapped with `%w` and a lowercase context phrase. Classified errors
  only where a caller must branch.
- Every package has a doc comment that says what it is for and what it
  refuses to do.

## Tests

Every code change needs a test impact assessment. See
[documentation/TESTING.md](documentation/TESTING.md) for what to record and
which tests protect which properties.

If you change behaviour that one of the property tests guards, the fix is
almost never to relax the test.

## Commits

Conventional Commits: `type(scope): description`, with types `feat`, `fix`,
`docs`, `refactor`, `test`, `build`, `chore`.

Write the body for whoever is reading `git log` in a year trying to understand
why. Say what changed, and say why that was the right call, including the
alternative you rejected.

Update `change_log.md` under `[Unreleased]` for anything user-visible.

## Pull requests

Include what changed, why, what you ran, and what you did not cover. If you
found a defect while working, say so even if you fixed it; the fact that it
was possible is usually more interesting than the fix.
