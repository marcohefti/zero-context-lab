# Feedback And Recommendation Evaluation

This routine exists to keep ZCL decisions broadly useful.
It applies to:
- operator/user feedback
- recommendations proposed by agents (including Codex)

## Non-Negotiable

Do not optimize ZCL for one user's preference unless it also improves the shared operator path.

A change is mergeable only when it improves one or more of:
- mission/task outcomes (`ok`, expectations, completion rate)
- evidence quality (trace completeness, validation integrity, determinism)
- orchestration reliability (fewer infra failures, clearer operator UX)

## Decision Routine

1. Frame the claim.
- Write the claim as a falsifiable sentence.
- Example: "Operators cannot tell whether this failure is infra or task."

2. Anchor on artifacts first.
- Use evidence, not transcript narratives.
- Minimum commands:
  - `zcl report --strict --json <runDir|attemptDir>`
  - `zcl validate --strict --json <runDir|attemptDir>`
  - `zcl attempt explain --json <attemptDir>`
- If evidence is missing or invalid, classify that first as an evidence issue.

3. Classify the request.
- `product_gap`: missing or weak primitive/UX in ZCL.
- `user_error`: misuse, wrong assumption, or skipped workflow step.
- `out_of_scope`: reasonable request, but outside ZCL's benchmark contract.

4. Run the broad-value gate.
- Approve only if at least one is true:
  - Recurs across missions/runs/operators.
  - Improves the default operator flow (faster, clearer, fewer footguns).
  - Reduces typed failure rates or evidence gaps.
  - Strengthens documented invariants (funnel-first, bounded evidence, deterministic artifacts).
- Reject or defer if all are true:
  - Single-user preference only.
  - Runner-specific workaround that hurts runner-agnostic design.
  - No measurable before/after signal.

5. Choose action.
- `accept`: plan a product/docs/test change.
- `accept_later`: backlog with explicit trigger condition.
- `reject_user_error`: respond with corrective usage guidance.
- `reject_out_of_scope`: document rationale and point to scope boundary.

6. Record the decision.
- Store a short operator note in attempt context when applicable:
  - `zcl note --kind operator --message "<decision>" --tags triage,<product_gap|user_error|out_of_scope>,broad_value`
- Keep scoring runner-agnostic: notes are secondary evidence only.

## Recommendation Quality Bar (For Agents)

Every recommendation should include:
- evidence pointer (`attemptDir`/`runDir` + key artifact signal)
- impacted audience (who benefits beyond one user)
- scope statement (in-scope vs out-of-scope and why)
- optimal solution shape (best operator outcome and system coherence, not smallest patch)
  - include rejected alternatives and why they lose on reliability/UX/invariants
- measurable validation plan (how we prove it helped)

Recommendations that fail this bar should not be implemented.

## Scope Boundary Reminder

ZCL is a benchmark harness.
If a suggestion primarily concerns runner transcript behavior, personal prompting style, or non-funnel side behavior, it is usually secondary evidence or out-of-scope for core scoring.
