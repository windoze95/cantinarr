# Issues, AI, and MCP

Remediation journeys with a real AI provider, real provider account/OAuth truth, and real-client MCP ceremonies. Issue lifecycle contracts, tool authorization, scrubbing, and the per-tool MCP contracts are proven by the hermetic suites.

Use the [run template](../run-template.md) to record executions of these cases.

## Issue remediation with a real provider

- [ ] `ISS-016` · P0 · LIVE — Have the agent ask a reporter-only question; verify Awaiting your reply, requester-safe prompt, bounded reply, same-run resume, and no duplicate run.
- [ ] `ISS-024` · P0 · LIVE — Approve every supported action type; confirmation must repeat exact target, one executor call occurs, durable result appears, and issue/run state advances truthfully.

## AI provider accounts, OAuth, and chat

- [ ] `AI-007` · P0 · LIVE — For each personal/shared provider, save a valid provider+model+key candidate; verify one real tool-free low-reasoning turn completes before atomic activation.
- [ ] `AI-013` · P0 · LIVE/UI — Complete personal OAuth; verify account identity/usage windows, model validation, selected source, restart persistence, and chat.
- [ ] `AI-014` · P0 · LIVE/UI — Repeat the device flow for admin-shared OAuth; verify only admins see shared identity/plan/usage and granted users learn only that access is included.
- [ ] `AI-018` · P0 · UI/LIVE — Start chat and verify ordered SSE frames: conversation ID, text, tool start/end, media results, error if any, then `[DONE]`; app never leaves a permanent typing state.
- [ ] `AI-019` · P0 · API/UI/LIVE — Against disposable current Radarr, Sonarr, and Chaptarr instances, explicitly ask in-app chat to make one scalar/cutoff/custom-format-score profile change; verify the assistant shows the preview and autonomously applies it once in the same authenticated turn without asking the admin to copy a reference. Confirm a different device, later turn, invented reference, replay, or direct arr edit cannot use the one-use handoff. In Settings > Configuration history, verify the applied entry identifies the target and actor, projects before/applied/current differences without raw snapshots, and offers restore only while the live profile, dependencies, and instance binding match. Restore the original values, verify a linked restore entry appears, then make a direct arr edit and confirm stale restore is unavailable/refused. Validation detail must remain useful and redacted. For Radarr/Sonarr, also verify language IDs are fetched from each live catalog rather than reused across services or versions, and language scoring affects future release selection only—not file default tracks or playback language.

## MCP with a real external client

- [ ] `MCP-006` · P0 · SEC/LIVE — Complete authorization-code flow with S256 PKCE, state, exact redirect/resource/scope, and password; verify code is one-time/short-lived and wrong verifier/redirect/resource fails.
- [ ] `MCP-007` · P0 · LIVE — Complete secure-browser passkey authorization and create a first passkey from MCP login for a connect-link-only user; verify correct Cantinarr identity/device.
- [ ] `MCP-008` · P1 · API/LIVE — On disposable current Radarr, Sonarr, and Chaptarr instances, use `upsert_custom_format` to create one native/TRaSH format and repeat by the same exact name with changed rules; verify one record exists, create added it to every profile at score 0, and update preserved profile scores and changed the format definition (stored file matches need not recompute). In Settings > Configuration history, verify both MCP writes are readable with their target, actor/source, outcome, and before/applied/current comparison. Change or remove the disposable format directly in the arr UI/API and verify history reports the drift; confirm no restore is offered for either custom-format entry.
