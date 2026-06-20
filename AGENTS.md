# Agent Instructions

## Collaboration

- Do not just agree with the user by default. If a request would weaken the project, hurt maintainability, reduce correctness, or make the task outcome worse, push back clearly and suggest the better path.

## Workflow

- Before starting any PR-sized change, fetch `origin/main`, make sure local `main` is even with `origin/main`, and create a feature branch from that fresh base.
- Do not open PRs directly from `main`. If work accidentally happens on `main`, verify `main` is still even with `origin/main`, then move the work to a feature branch before committing.
- Preserve user work. Do not revert or delete unrelated local changes or untracked files.
- When the change is ready, commit on the feature branch, push it, and open a PR with `gh pr create` unless the user explicitly asks not to.

## Verification

- For server changes, run `go test ./...` from `server/`.
- For Flutter app changes, run `flutter analyze` and the relevant `flutter test` targets from `app/` when feasible.
- Mention any tests or checks that could not be run.
