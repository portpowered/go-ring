# Independent review record

Status: **open; two final independent all-item reviews are required**.

The current [checklist](template-checklist.md) pins shared template
`62cc3cb5a1308dae8700f92052f99b1c455a1d98`.
Earlier scoped documentation approvals remain in Git history and do not approve
this expanded checklist or the current implementation changes.

## Repairs awaiting final verification

- Prove schema ownership through helper calls, callbacks, aliases, named results,
  closed values, and map keys; reject unresolved, recursive, and exhausted paths.
- Exercise the exact default route gate with compile-valid negative and positive
  controls, including field-specific constants and pointer-backed empty strings.
- Prove paired MCS cleanup with peer EOF and no retry or redial after cancellation.
- Reject unsupported authorization fields before network I/O.
- Isolate injected HTTP clients from later caller mutation and reject unsafe
  shared cookie jars without sending requests.
- Check both tracked and untracked generated output in release workflows.

The final reviewers must record separate verdicts and evidence for all sixteen
items at the frozen implementation commit. Any remaining finding keeps the
corresponding item and independent-verification item open. SDK and CLI release,
Pages deployment, and clean consumer installation also require final evidence.
