# Cantinarr master regression catalog

This is the canonical, committed behavioral test catalog for Cantinarr. It defines the observable contract that implementation changes must preserve and the release coverage needed to prove it. The committed files are always run-neutral; execute tests from a copy stored outside the worktree or attached to a release or issue.

## Catalog map

| Area | Case prefixes | Active cases |
|---|---|---:|
| [Build, operations, usability, and release](catalog/baseline-operations-release.md) | BASE, OPS, UX, PERF, REL, EXP | 72 |
| [Authentication, navigation, users, and security](catalog/auth-users-security.md) | AUTH, NAV, USER, SEC | 88 |
| [Instances, realtime behavior, and push](catalog/instances-realtime-push.md) | INST, RT, PUSH | 65 |
| [Plex linking, libraries, and invitations](catalog/plex.md) | PLEX | 87 |
| [Discovery and requests](catalog/discovery-requests.md) | DISC, REQ | 69 |
| [Media services and download clients](catalog/media-services.md) | RAD, SON, BOOK, DOWN, TAUT | 91 |
| [Issues, AI, and MCP](catalog/issues-ai-mcp.md) | ISS, AI, MCP | 115 |
| **Total** | | **587** |

The catalog contains **587 active cases**: **374 P0**, **205 P1**, and **8 P2**. Shared setup is in [Required regression fixtures](fixtures.md), execution metadata and evidence belong in the [run-copy template](run-template.md), explicitly unimplemented requirements are governed by the [known-gap ledger](known-gaps.md), and removed behavior is recorded in the permanent [retired-ID ledger](retired-cases.md).

## How to execute it

- Copy the entire `docs/testing/` tree outside the worktree, or preserve its layout in a release/issue artifact, then execute the applicable catalog files with `run-template.md`. Never record an execution in the canonical files and never add run copies to `.gitignore`.
- Checkbox meanings in a run copy: `[ ]` not run and `[x]` passed. For another result, leave the box unchecked and append `— FAIL:`, `— BLOCKED:`, or `— N/A:` with a defect/evidence link and reason.
- Never erase or overwrite a failure in an active run. Append retest evidence and the eventual resolution so the original failure remains auditable.
- A UI toast alone does not prove an external mutation. For Plex, arrs, download clients, push, and AI providers, capture both Cantinarr's result and the external system's resulting state.
- Do not check a parent case with a vector table until every applicable vector passes.
- On every candidate, run BASE plus all P0/P1 cases in changed and dependency-affected areas. Run every P0 before a major release, storage/auth migration, or broad integration rewrite.

Priorities describe impact:

- **P0** blocks a release whose declared scope includes the area.
- **P1** is a serious changed-area or dependency-affected regression.
- **P2** is extended, compatibility, or exploratory coverage.

Tags describe the proof surface: **AUTO** machine-driven repository or CI proof (possibly paired with manual/external assertions), **API** direct contract check, **UI** app behavior, **LIVE** real third-party service/device, **CHAOS** controlled failure/recovery, **SEC** authorization/privacy, **RT** realtime convergence, and **GAP** a required behavior that is explicitly known not to be implemented yet. A GAP needs a linked accepted decision in `known-gaps.md`; it remains a failure, not a waiver. Its ledger row is permanent and becomes resolved or withdrawn when the tag is removed. An in-scope P0 GAP blocks release, while P1 requires the normal recorded exception. Product documentation must still describe shipped reality.

## Catalog maintenance contract

- Before changing user-visible behavior, an API or route, screen, setting, schema, event, permission boundary, integration, deployment path, or workflow, search the catalog for affected case IDs. Reconcile those cases before declaring the change complete.
- Update the catalog in the same PR as the behavior. Update an existing case when its setup, action, or expected result intentionally changes. Add a case for new behavior, for a defect that exposes missing regression coverage, or for a materially different boundary or failure mode.
- For every new or changed behavior, cover every applicable dimension: happy path; empty, minimum, maximum, and invalid input; anonymous, requester, and admin authorization; persistence, restart, and upgrade; cancellation, timeout, retry, idempotency, duplicate submission, concurrency, and partial failure; cache freshness, realtime, and cross-device behavior; external source-of-truth verification; backward compatibility; and secret, privacy, and logging containment. Explain intentionally inapplicable high-risk dimensions in the PR.
- A bug fix must identify an existing case that would have caught the bug, or add/strengthen one when none does. Never weaken an expected result merely to match broken implementation; record the defect or an accepted GAP instead.
- Case IDs are permanent and globally unique. Use `PREFIX-NNN`; a new prefix starts at `001`, otherwise allocate one greater than the highest number ever assigned in that domain, including retired IDs. Never renumber or reuse an ID. When behavior is renamed, keep its ID. Retire a case only when its shipped behavior is removed, and add its ID, substantive reason, replacement or `None`, and PR or release reference to `retired-cases.md`.
- The `AUTO` tag means the case includes machine-driven repository or CI proof; the case names the command, workflow, build, or automated portion and may still require manual/external assertions. Existing automated tests do not replace behavioral cases. When a named test automates a case, append a durable `AUTO: path/to/test::name` annotation; update that annotation in the same PR if the test moves, is renamed, or is removed.
- When a case adds or changes a role, account, data state, upstream service/version, device, failure injection, evidence, or cleanup requirement, update `fixtures.md` and the run cleanup guidance in the same PR.
- Keep active definitions unchecked, vector-result cells empty, run metadata blank, and the dashboard counts accurate. Run `python3 docs/testing/check_catalog.py` after every catalog edit.
- Every PR must report the IDs added, updated, retired, or already verified as accurate. A PR with no catalog impact must give a specific reason; CI rejects blank, contradictory, or malformed catalog declarations.

The repository-wide agent obligations are canonical in [`AGENTS.md`](../../AGENTS.md). Catalog maintenance supplements rather than replaces the owning product-documentation updates required there.

## Release exit criteria

- Every P0 case in the declared scope is executed, with external evidence for LIVE cases; a full or major-release run includes every P0.
- Every P1 case in a changed or dependency-affected area is executed; remaining P1 cases have an explicit accepted scope decision in the run evidence.
- No open defect can cause wrong-user or wrong-instance mutation, silent broadening of library/media scope, secret exposure, data loss, false “Available,” “resolved,” or “invited” state, or destructive action without confirmation.
- CI/build checks are green for the exact commit and image tested, and required iOS, Android, Docker, store, and site workflows for the changed paths completed.
- Documentation and public copy describe the tested shipped behavior, including known external-state limitations.
- Test data and external side effects are cleaned up: temporary Plex shares/libraries, arr media/queue entries, download data, users/devices/tokens, AI OAuth links, webhooks, push tokens, and test issues/actions.
