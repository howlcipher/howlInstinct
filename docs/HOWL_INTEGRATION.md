# Howl Integration

How other Howl components can consume HowlInstinct, and the boundary every one
of them shares.

**Nothing in this document has been implemented in any sibling repository.**
No sibling repository was modified while building HowlInstinct. These are
proposed contracts, written from this side, to be accepted or rejected by each
component's owner.

## The shared boundary

```
        HowlInstinct                    the consumer
        ------------                    ------------
        judges                          decides
        reports uncertainty             sets thresholds
        emits receipts                  applies policy
        knows nothing of consequences   takes action
```

Every integration below follows the same shape: HowlInstinct answers a bounded
question, the consumer decides what that means and what happens next.

If an integration seems to need HowlInstinct to "just decide", that is the
signal the boundary is being crossed, not that the boundary is inconvenient.

## Consuming receipts

Two supported routes.

**Go consumers** import the contract directly:

```go
import (
    "github.com/howlcipher/howlinstinct/pkg/instinct"
    "github.com/howlcipher/howlinstinct/pkg/receipt"
)
```

**Everyone else** vendors the schema, following the ecosystem's existing
pinned-copy convention:

```
contracts/howlinstinct/
├── SOURCE.md          repo, pinned commit, source path, date, re-vendor steps
└── howlinstinct.decision_receipt.v1.schema.json
```

Two rules for any consumer:

1. **Treat `provider_confidence` as optional.** A noul has none. Do not
   default it to zero; a zero you invented is indistinguishable from a
   provider saying it had no confidence at all.
2. **Treat `UNAVAILABLE` and `ERROR` as neither yes nor no.** A judgment that
   could not be made is not a judgment of "no". Deciding what to do about
   missing information is the consumer's job, and collapsing it into a
   negative silently downgrades every provider outage into "nothing is wrong".

## HowlFrame

**HowlFrame holds authority. HowlInstinct does not.**

This is the integration the rest of the design exists to make safe. The
intended flow is that HowlInstinct emits a receipt, HowlFrame reads it, and
HowlFrame alone decides whether anything may happen.

```
HowlInstinct: "This appears to be a low-risk deployment." margin 0.91
HowlFrame:    "Policy still requires approval, because production database
               migrations may never self-authorize."
```

Both are correct at once. HowlFrame's policy is free to ignore a judgment
entirely, and a high margin must never be sufficient on its own for an
irreversible action.

Suggested use:

- receive decision receipts as policy inputs;
- apply deterministic policy, including thresholds HowlFrame owns;
- require human approval for classes of action regardless of confidence;
- deny actions.

`examples/dogfood/policy` is a miniature stand-in for this role, written to be
read in one sitting.

## HowlPlane

Orchestration. The most natural first consumer, because its questions are
small, frequent, and currently answered by heuristics.

- **Task routing**: which agent or queue should handle this task? (`classify`)
- **Result completeness**: does this run's output actually satisfy what was
  asked? (`noul`)
- **Failure classification**: infrastructure, configuration, or genuine
  failure? (`classify`)
- **Retry versus escalate**: is this failure likely transient? (`noul`)
  HowlInstinct answers the question; HowlPlane decides whether to retry.

The `agent_run_triage` suite is written for this consumer. Its most important
case is the run whose tests pass because the agent deleted the failing
assertions: exit status says success, and collapsing that into `success` is
the expensive mistake.

## HowlProof

Evidence and verification.

- **Evidence sufficiency**: does the supplied evidence establish the claim, as
  opposed to merely being related to it? (`noul`)
- **Test result classification**: genuine failure, flake, or environment?
  (`classify`)
- **Security relevance**: does this change touch a security-sensitive path?
  (`noul`)
- **Review routing**: which kind of reviewer does this need? (`classify`)

The `evidence_sufficiency` suite is written for this consumer, and
deliberately pairs claims with evidence that is lexically near-identical but
semantically opposite, because that is where a pattern matcher fails and a
judge should not.

A caution specific to HowlProof: a judgment that evidence is sufficient is
itself a claim, and one with no evidence behind it beyond a probability. It
belongs in a chain of evidence as an input, never as the terminal link.

## HowlChangeOps

Change execution. **HowlInstinct recommends and judges. It does not execute a
rollback.**

- **Semantic risk classification**: how large is the blast radius? (`score`)
- **Post-deploy signal interpretation**: does this metric shift look like harm?
  (`noul`)
- **Rollback recommendation input**: one signal among several, never the
  trigger.
- **Change categorization**: (`classify`)

The `change_risk` suite is written for this consumer. Note that risk labels
are judgement calls even among experienced engineers, so its ground truth is
one reasonable opinion rather than a fact, and HowlChangeOps should calibrate
against its own history rather than these labels.

## HowlWriter

Drafting and citation.

- **Claim support**: does this source support this claim? (`noul`)
- **Source relevance**: (`score`)
- **Citation sufficiency**: is this claim adequately cited? (`noul`)
- **Semantic classification** of passages: (`classify`)

## HowlBoard

Tickets and planning.

- **Ticket classification**: (`classify`, the `routing` suite applies directly)
- **Priority signal**: (`score`) as an input to prioritization, not as the
  priority itself.
- **Duplicate likelihood**: is this the same issue as an existing ticket?
  (`noul`)
- **Ownership routing**: (`classify`)

## A checklist before integrating

1. **Could deterministic code answer this?** If yes, write that instead. This
   is the first branch of the hierarchy and the one most often skipped.
2. **Is the question genuinely bounded?** If the useful answer is a paragraph,
   this is the wrong layer.
3. **Who owns the threshold?** It must be the consumer. If the answer is
   "HowlInstinct should just know", stop.
4. **What happens when the judgment is unavailable?** Decide that before
   integrating, not during an incident.
5. **How will you know it is working?** Build an eval suite from your own data.
   The shipped suites demonstrate the harness and tell you nothing about your
   traffic.
6. **What is the cost of being wrong?** For anything irreversible, the answer
   is that a judgment is an input to a decision a human or a policy makes, and
   never the decision itself.
