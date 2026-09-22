# Philosophy

HowlInstinct exists to be boring infrastructure. These are the ideas it is
built on, stated plainly enough to be argued with.

## Use the least powerful mechanism that reliably solves the problem

Most questions a system asks itself are small. Is this an incident? Which team
owns this? How risky is this change? The usual options are a brittle keyword
rule that nobody trusts, or a full generative agent that is slow, expensive,
and returns prose someone then has to parse.

The intended progression is:

```
Can deterministic code answer it?
        |
       YES -> CODE
        |
       NO
        v
Can a bounded semantic judgment answer it?
        |
       YES -> HOWLINSTINCT
        |
       NO / UNCERTAIN
        v
Does this need reasoning, synthesis, planning, or generation?
        |
       YES -> SYSTEM-2 AGENT
        |
 still uncertain, or policy requires it
        v
      HUMAN
```

As shorthand: System 0 is deterministic software, System 1 is HowlInstinct,
System 2 is reasoning and generative agents, System 3 is human authority.

Those names are architectural vocabulary, not a claim about cognition. They
are useful because they make the question "what is the least powerful thing
that would work here?" easy to ask out loud. They are not evidence of
anything, and nothing in this repository depends on them being more than
labels.

The first branch matters most. If deterministic code can answer a question,
HowlInstinct is the wrong tool, and reaching for it is how a system acquires a
probabilistic dependency it did not need.

## Confidence is not permission

This is the line the whole design protects.

HowlInstinct makes judgments. Something else decides whether anything is
allowed to happen as a result.

```
HowlInstinct:  "This appears to be a low-risk deployment."
               probability: high

HowlFrame:     "Policy still requires approval, because production database
                migrations may never self-authorize."
```

Both statements are correct simultaneously. The judgment is about the world;
the decision is about authority. Collapsing them is how a system ends up with
`if confidence > 0.95 { deploy() }` written somewhere nobody is looking.

Consequences that follow from taking this seriously:

- There is no default threshold anywhere in this codebase. Not a constant, not
  a fallback, not a "sensible default" in a config file.
- `needs_escalation` is set only when the caller supplied a rule. Without one,
  nothing escalates, no matter how uncertain the judgment.
- The outcome vocabulary is `ACCEPTABLE_CONFIDENCE`, `LOW_CONFIDENCE`,
  `UNAVAILABLE`, `ERROR`. There is no `APPROVED` and no `DENIED`, because
  HowlInstinct cannot approve or deny anything.
- A test parses this repository's own source and fails if a judgment's
  confidence is ever compared against a hardcoded threshold.

## Deterministic outside, probabilistic inside

The uncertainty lives in the middle and is surrounded on both sides by things
that are not uncertain.

Going in, requests are strictly validated: bounded sizes, known types,
identifiers that match a fixed pattern, duplicate keys rejected. Coming out,
provider responses are validated again before a single value is allowed into a
judgment, and receipts are canonicalized and hashed so the same decision
always produces the same digest.

A caller should be able to depend on HowlInstinct's edges absolutely, and on
its middle only as much as the reported uncertainty warrants.

## Uncertainty must stay visible

Every mechanism that could quietly erase uncertainty has been closed:

- **Absence is not zero.** A noul carries no confidence value. If that were
  stored as a plain number, an absent confidence would serialize as `0.0`,
  asserting that the provider was maximally unconfident when it said nothing
  at all. Every provider-supplied number is a pointer, so absence stays
  absent, in the type system, in the JSON, and in the receipt.
- **Provider confidence and derived metrics never share a name.**
  `provider_confidence` is what the provider said. `instinct_margin` is what
  HowlInstinct computed, with its formula documented. Neither is ever
  converted into the other.
- **A metric is reported only when the data supports it.** The evaluation
  harness emits `null` plus a reason rather than a number it cannot stand
  behind.
- **The words "certain", "safe", "correct", and "guaranteed" do not appear**
  as descriptions of a judgment. A high probability is a high probability.

## Judgment is not action

HowlInstinct has no vocabulary for consequences. It cannot deploy, page,
merge, roll back, approve, or delete. It does not retry a workflow, choose a
different provider, or decide that a question should be asked again.

This is not a limitation to be lifted later. It is the reason the component
can be trusted by things that do have authority.

## Evidence everywhere

Two claims are made about every decision, and both are checkable:

1. **What was decided**, as a durable receipt carrying the judgment, its
   uncertainty, the provider, and hashes of the inputs.
2. **Whether this class of decision is any good**, as an evaluation run
   against version-controlled cases, with metrics that decline to be computed
   when the data cannot support them.

The second is the one people skip. "Is this decision class trustworthy enough
for this use" is an empirical question, and the only honest way to answer it
is to measure. A component that produces confident-looking judgments with no
way to check them is worse than no component, because it converts an open
question into a settled-looking one.

## Simplicity over cleverness

Where these are in tension:

- cleverness against simplicity, choose simplicity;
- autonomous behaviour against explicit policy, choose explicit policy;
- apparent certainty against visible uncertainty, preserve the uncertainty.
