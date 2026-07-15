# Regression run-copy template

Copy the entire `docs/testing/` tree outside the worktree, or preserve its directory layout in a release/issue artifact. Fill metadata, case checkboxes, vector results, and evidence only in that execution copy. Never overwrite a failure during a retest; append the retest and resolution so the original result remains auditable. The committed catalog must remain blank and run-neutral.

## Run record

| Field | Value |
|---|---|
| Version / commit | |
| Candidate image digest | |
| Catalog files / case IDs in scope | |
| Started / finished | |
| Tester(s) | |
| Fresh-install environment | |
| Upgrade-from version | |
| Web browsers | |
| iOS device / OS / build | |
| Android device / OS / build | |
| Server architecture | |
| Linked external services | |
| Result | NOT RUN |
| Accepted exceptions / issues | |

## Execution evidence log

Append one row for every failure, block, accepted N/A, and any P0/P1 result that needs external proof. Keep evidence free of secrets.

| Case ID | Result | Version / environment | Evidence | Defect / note | Tester / date |
|---|---|---|---|---|---|
| | | | | | |

Apply the release exit criteria from the canonical master-catalog index before declaring the run complete.
