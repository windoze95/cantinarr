# Store release playbook

`main` is the ongoing development branch. Short feature PRs merge there; one temporary
`release/X.Y.Z` branch freezes a release while development continues. Stable releases promote
artifacts already tested from that frozen commit. The server and native apps can ship independent
patches; a coordinated release records the exact combination rather than assuming equal version
numbers guarantee compatibility.

## Branches and audiences

| Source | Server image | iOS | Android |
|---|---|---|---|
| Green `main`, no release branch | `edge` | Public Beta + existing internal group | Open `beta` + closed `alpha` |
| Green `main`, release branch active | `edge` | Public publishing paused | Public publishing paused |
| Active `release/X.Y.Z` | `X.Y.Z-rc.<run>` | Manual dispatch to Public Beta + existing internal group | Manual dispatch of one AAB to `alpha`, `beta`, and owner `internal` |
| PR | Automatic `pr-N` image | On-demand Internal Only build | On-demand `internal` build |
| Stable `vX.Y.Z` tag | Tested candidate digest → `X.Y.Z`, `X.Y`, `latest` | Separate explicit submission | Separate explicit production promotion |

`latest` follows stable releases. Installing this workflow leaves its existing image in place;
the next deliberate stable release replaces it. Existing catalog installs keep using `latest`
and therefore follow stable releases too. Use `edge` to follow development.

There can be **only one** active release branch. Its existence gives it ownership of public
mobile betas and listing updates. Main keeps running CI and publishing `edge`; mobile/listing
workflows exit without publishing while frozen. Release-branch pushes run CI and build server
candidates, but hold mobile builds and listing changes until a manual workflow dispatch. This
lets a coordinated release verify its server candidate before the matching apps reach testers.
Before each upload, the workflow checks the
branch head and ownership again. TestFlight checks again after Apple processing, immediately
before its external review submission and group assignment. An obsolete queued build cannot overwrite a newer source.
If a freeze starts after a main mobile run begins, its remaining publishing steps are skipped
with a notice and a workflow summary instead of failing the run. A skipped upload creates no
uploaded-build receipt and cannot start TestFlight distribution. Source changes, invalid release
ownership, and unreadable ownership state still fail the publishing check.
After deleting the release branch, main publishing resumes on its next relevant push or manual
dispatch. Public TestFlight and Play opt-in links stay the same.

## Release procedure

### 1. Prepare and freeze

For a coordinated launch, merge preparation changes through normal CI with the current app
version and release notes. A native version bump on unfrozen main starts public beta builds,
so put the launch's version/release-notes PR on the release branch instead.

After fetching and verifying the selected main SHA and its green CI run, a maintainer creates
`release/X.Y.Z` at that SHA and pushes it. Do not create a permanent `dev` branch. Candidate fixes
go through PRs targeting the release branch; merge those fixes back to main through a PR as well.
Every source change invalidates the previous candidate combination and requires new builds and
verification. Keep unrelated features on main.

Merge the version/release-notes PR into the release branch. `app/pubspec.yaml` supplies the iOS
and Android marketing version; build numbers are allocated by each store workflow. The release
branch name supplies the server version. For the first coordinated 1.0 release, use
`release/1.0.0` and native version `1.0.0`. Routine server-only or app-only patches need not bump
the other component. Compatibility floors change only with the breaking change that requires
them and remain warn-only.

For a coordinated release, wait for that final commit's green CI and successful multi-arch
Docker candidate. Install its recorded digest in the test environment and verify server health
and the applicable upgrade checks before starting the phone builds. A later candidate fix
requires this step again; a server image from an older SHA does not prove the new candidate.
If no server build ran, dispatch it explicitly:

```bash
gh workflow run docker.yml --ref release/X.Y.Z
```

After server verification, manually dispatch the matching mobile builds and listings:

```bash
gh workflow run testflight.yml --ref release/X.Y.Z
gh workflow run playstore.yml --ref release/X.Y.Z
gh workflow run storelisting.yml --ref release/X.Y.Z -f platform=both
```

Run only the components being released. Candidate Android builds always go to both public testing
tracks and the owner-only internal track, even if a single-track dispatch option was selected.
Candidates intended for production use `completed`, not `draft`.
Candidate pushes never start these uploads automatically, including after a version bump.
The explicit dispatch is the maintainer's checkpoint for coordinating the components; each
workflow still verifies exact-source CI and current branch ownership before publishing.

### 2. Test and record the exact combination

Wait for **successful workflow completion and actual tester availability**. App Store/Play
processing or review can outlast an upload. Install the candidate image by digest on a test
server, install the recorded mobile builds on real devices, and run the applicable
[release and upgrade cases](testing/catalog/baseline-operations-release.md). Verify old app ↔ new
server, new app ↔ old supported server, and the candidate pair, including login/session retention,
passkeys, push, deep links, requests, and a populated database upgrade. Record failures and
unavailable environments explicitly.

Each successful component run retains a `*-release-record-<attempt>` artifact containing its
exact source SHA, version, build number or multi-architecture digest, run ID/attempt, and both
compatibility minima. Dispatch **Record release candidate** (`release-candidate.yml`) from main,
select those run IDs, and enter the verification actually performed. Omit components unchanged in
an independent patch, identifying the existing supported counterparts in the verification notes.
The workflow rejects failed, preview, mismatched-workflow, mixed-commit, or superseded candidates.
Its `release-candidate-<attempt>` artifact is the release's combined record.

Save this record with the release evidence. Actions artifacts expire after 90 days; a stable
GitHub Release automatically archives both the server receipt as `release.json` and the combined
`release-candidate.json`. The server tag selects the run named in that combined record, so a later
rebuild of the same SHA cannot silently replace the digest that was tested. For a mobile-only release,
retain the combined record with the release issue or other permanent release evidence.

### 3. Promote the selected builds

For a coordinated public launch, finish all candidate acceptance and store declarations first.
Use the selected builds for store review, arrange the stores' available release controls, and
keep the stable server tag unpublished until the apps are ready for the agreed launch window.
Then release the selected apps and promote that same candidate's server digest. Store review
and propagation can delay availability; verify actual installs before announcing completion.

From main, dispatch **Submit App Store Release** (`appstore-release.yml`) with the successful iOS
candidate `build_run` and `confirm=submit`. It syncs listing content from that candidate SHA and
submits its exact version/build with **manual release** and phased rollout enabled. It never
selects the newest TestFlight build. Wait for Apple's approval and then release it in App Store
Connect at the chosen rollout time.

Dispatch **Publish Play Production Release** (`playstore-release.yml`) from main with the Android
candidate `build_run` and `confirm=publish`. It promotes that exact version code from alpha to
production without uploading another AAB. Repeating an already completed promotion is a no-op;
a newer production version prevents an older promotion. Verify Play review/publishing state and
that users can actually install the release.

For a server release, tag the tested candidate SHA:

```bash
git tag vX.Y.Z <candidate-sha>
git push origin vX.Y.Z
```

The Docker workflow requires a recorded candidate with this exact version and SHA, verifies
CI again, and promotes its existing multi-arch digest. It verifies every resulting tag, refuses
to replace an existing numbered image, and refuses to move `latest` backward, including after a
partial release. Only then does it create the GitHub Release. Linux tarballs are extracted from
the promoted image rather than rebuilt. The server receipt and combined candidate record are
attached automatically; verify both appear with the release assets.

Binary extraction resolves each architecture's child manifest from the pinned image index
before pulling it, so classic Docker image stores never have to replace one platform under
the same index digest. It checks both the source revision and platform before packaging.
If packaging needs a workflow repair after publication, leave the tag and image immutable;
dispatch the repaired **Attach Release Binaries** workflow from main with the existing tag.

**Order by compatibility.** Ship additive server support first if old clients continue working.
If a server change raises the minimum app version, make the new app available in both stores
first, including Apple's approval and manual release, then release the server. Approval alone
is insufficient. New clients must continue working with supported old servers during rollout;
otherwise split the change into compatible stages. This keeps a self-hosted server upgrade from
requiring a mobile update users cannot obtain.

### 4. Close the release

Verify the GHCR digests, GitHub Release assets, store availability, and the recorded versions on
real installations. Update the Umbrel catalog's version and `tag@<multi-arch digest>` in a small
PR; the other catalogs either follow stable `latest` or have tag-update bots (details in
`AGENTS.md`). If the release branch gained fixes, merge it back to main through a PR using
**Create a merge commit**. This retains the released commit and its tag in main's history,
so `git describe` on subsequent edge builds recognizes the release. Verify the candidate SHA
is an ancestor of main before deleting the release branch. Then dispatch
main's TestFlight, Play, and listing workflows once to resume immediately, or let the next
relevant push do it. Stable `latest` stays on the released version while `edge` continues forward.

Never move/delete a published version tag or replace its artifact. A bad release gets a new patch;
restoring a server requires the pre-upgrade database and matching encryption key when migrations
are not backward compatible. For an urgent patch during another candidate's freeze, finish or
abandon that candidate before opening the patch branch; two competing release branches fail closed.

### Failures and retries

All TestFlight uploads/submissions/listing writes share `testflight-deploy`; all Play writes share
`playstore-deploy`. They do not cancel an in-flight upload. GitHub concurrency keeps one running
and one pending member, so a later request can replace a pending request; inspect the selected
run and dispatch again if it was cancelled. These are publishing locks, not a durable job queue.

A TestFlight distribution failure after upload should use **Re-run failed jobs** so it reuses the
uploaded build. Re-running the build job allocates a new number; test and record that new build.
The same applies to a Play upload that reached alpha before a later promotion failed: a fresh
build run allocates a new version code, and only a fully successful run is eligible for production.
Failed-job retries can use a receipt from an earlier successful job in that same run. Store
submissions never silently substitute a different build. If branch ownership or source changed,
dispatch the current source instead of retrying an obsolete one.

## Test a PR on a phone

Automatic PR checks build the server preview and, when Android plumbing changes, an Android
build-only artifact without store/signing credentials. Signed mobile previews are on demand:

1. Review the PR's **full current head SHA**, including build scripts and dependencies. This is
   deliberate authorization to build that code with signing/upload credentials, including for a
   fork; approving an ordinary fork CI run alone does not authorize a signed build.
2. Wait for CI on its current merge checkout. Dispatch **Test a PR on my phone**
   (`mobile-preview.yml`) from **main** with `pr_number`, `reviewed_sha`, and `platform`.
3. The workflow resolves that exact PR merge SHA, verifies CI's checkout receipt, checks the
   maintainer's repository permission, and rechecks head/merge identity before upload. A head or
   base change needs fresh CI and a new dispatch. A conflicted, closed, or draft PR is refused.
4. Install from the owner-only internal audience. The tester notes identify the PR and SHA;
   connect it to the matching `pr-N` server image when testing coordinated changes.

An iOS preview is exported with `testFlightInternalTestingOnly=true`; Apple prevents it from
reaching external testers or the App Store. The external distribution job is skipped, and
production selection also rejects preview receipts. Android previews upload only to `internal`;
production selection accepts only successful candidate branch runs, never phone-preview runs.

These use the existing app IDs, so each phone has one installed Cantinarr build at a time. After
preview testing, select the desired public/candidate build in TestFlight. Android internal testers
cannot simultaneously receive open/closed test builds; candidates are therefore also copied to
internal. To return the owner to current main outside a freeze, dispatch Play from main with
`track=internal` (a fresh higher version code), or leave the internal test and rejoin the public beta.
Public testers' enrollment is unchanged.

### Owner-only audience setup

- TestFlight: the existing **Cantinarr Testers** internal group contains only the owner and uses
  automatic distribution for Xcode builds. Keep that membership owner-only and leave all external
  groups/public links in place. Do not add another internal group that auto-receives PR builds.
- Play: **Internal testing → Testers** selects only **Cantinarr Owner**, an email list containing
  the owner's Google account. Existing closed/open tester lists remain separate. The owner joins
  at <https://play.google.com/apps/internaltest/4701092996294643927>.
- GitHub: the `mobile-preview` environment allows the `main` branch and carries environment
  variable `MOBILE_PREVIEW_READY=true` after both audiences are verified. The workflows refuse
  previews without that flag. Clear it before altering internal audiences and re-audit membership
  before enabling it again. No personal email addresses or signing material belong in the repo.

Both store pipelines use the SDK in `app/.flutter-version`, install the committed `app/pubspec.lock` with
`flutter pub get --enforce-lockfile` and build with `--no-pub`. Dependency or package
hash drift fails the install instead of changing the versions being shipped.
SDK version and lockfile changes also trigger the Android build-only PR check.

## Android (Google Play)

### Pipeline

`.github/workflows/playstore.yml` runs on `main` and `release/**` pushes that touch Android-relevant
`app/**` paths (web/ios/desktop subdirs, tests, dev tooling, and markdown excluded), and on manual dispatch (inputs: track
`both` (default, open + closed testing)/`beta` (open testing)/`alpha` (closed testing)/`internal`,
release status `completed`/`draft`).
Release-branch push runs stop at source selection; candidate builds require manual dispatch.

0. A `gate` job waits for the `CI` run on that exact commit and fails the workflow if it isn't
   green, so nothing is built or uploaded from an unproven commit. The build-only PR check is
   exempt (it uploads nothing).
1. Version code = max version code across all Play tracks + 1 (`next_build_number` lane in
   `app/android/fastlane/Fastfile`); version name = `pubspec.yaml` version minus the `+` suffix.
2. The AAB is signed with the upload keystore from the `ANDROID_KEYSTORE_*` secrets and attached
   to the run as an artifact — every run, upload or not.
3. With `PLAY_SERVICE_ACCOUNT_JSON` set, the `beta` lane uploads the AAB once to **alpha**
   (closed testing), then copies that exact release to **beta** (open testing) without uploading
   the bundle again. Both groups receive the same version code and release notes. The workflow
   passes its build number as `PLAY_VERSION_CODE` so promotion cannot select another release;
   `draft` stays draft on both tracks. Either publishing step failing fails the workflow; if
   promotion fails, the successful closed-track upload remains in Play.
   Manual dispatch can target a single track: `beta`, `alpha`, or `internal`. Without credentials, an ordinary main
   beta build can remain artifact-only; candidate and phone-preview delivery fail closed.

Runs are serialized (`concurrency: playstore-deploy`) because two concurrent runs would compute
the same version code. The Play workflow also runs
`ruby scripts/tests/test_playstore_release.rb` (from the repo root) against Fastlane with an
in-memory Play client, including on build-only PRs. This verifies distribution to both tracks,
single-track dispatch, release notes, draft status, exact version selection, and failure handling
without store credentials or a live upload.

### Push (Firebase)

Android push needs two Google artifacts, deliberately kept apart:

- **`app/android/app/google-services.json`** — committed in this repo. Firebase project
  identifiers for the registered Android app (`codes.julian.cantinarr`); not a secret. Self-built
  APKs against a different backend swap in their own Firebase app's file.
- **The FCM service-account key** — the send credential. It never lives in this repo: it belongs
  to the push gateway's deploy secrets (see the push-gateway repo's `docs/FCM-SETUP.md`, which
  also covers the Firebase-console walkthrough and the Play-key-vs-FCM-key trap).

Store impact: include the `firebase-messaging` SDK, device and user identifiers, push tokens,
notification content, and the configured gateway in the **Data safety** reassessment below.
The community relay receives delivery data and keeps device registrations and delivery records,
including notification titles. Check the privacy policy against the current relay behavior before
the next console submission.

### Signing material

| Secret | Contents |
|---|---|
| `ANDROID_KEYSTORE_BASE64` | base64 of `upload-keystore.jks` |
| `ANDROID_KEYSTORE_PASSWORD` / `ANDROID_KEY_PASSWORD` | keystore/key password (same value; PKCS12) |
| `ANDROID_KEY_ALIAS` | `upload` |
| `PLAY_SERVICE_ACCOUNT_JSON` | Google Cloud service-account JSON key with Play publish access |

The keystore is only the Play **upload key** — Google Play App Signing holds the actual app
signing key, so a lost upload key is recoverable (Play Console → "Request upload key reset").
The `.jks` and password live outside the repo (dev machine `~/Projects/Cantinarr/release-keys/`;
back them up to a password manager). Never commit either; `app/android/.gitignore` already
ignores `key.properties` and `*.jks`.

### One-time Play Console setup (human)

1. Register a Google Play developer account (one-time $25) at play.google.com/console.
2. Play Console → **Create app** — name `Cantinarr`, app (free). The package name binds as
   `codes.julian.cantinarr` on first upload and can never change.
3. Download the `.aab` artifact from any **Deploy to Play Store** run. The very first bundle of a
   new app must be uploaded by hand (the publisher API can't create it): Testing → Closed testing
   → Manage the pre-made "Closed testing - Alpha" track → create release → upload the AAB. Accepting Play App Signing
   here enrolls the upload key.
4. Google Cloud console: pick/create a project → IAM & Admin → Service accounts → create
   (e.g. `play-publisher`) → Keys → add a JSON key.
5. Play Console → Users and permissions → Invite new users → the service account's email →
   grant release permissions (releases to testing tracks) or Admin.
6. `gh secret set PLAY_SERVICE_ACCOUNT_JSON < key.json` enables automatic uploads. The pipeline
   defaults to both open and closed testing; a new app still completing its closed test must
   explicitly target `alpha` until production access is approved and its open testing track is ready.
7. Finish the listing prerequisites in the console before promoting beyond testing: store
   listing (copy + graphics), data safety form, content rating questionnaire, privacy policy URL.
8. Closed testing → Testers: add an email list or Google Group and share the opt-in link.
9. For native Android passkeys on a deployment: after Play App Signing is enrolled, copy the
   **app signing key** SHA-256 from Play Console → App integrity into the server's
   `CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS` (the upload key's fingerprint is the wrong one —
   Google re-signs distribution builds).

Personal developer accounts created after Nov 13, 2023 must run a closed test with **12+ opted-in
testers for 14 continuous days** before they can apply for production access (the closed alpha track
satisfies this; the console dashboard tracks progress and then offers a production-access
questionnaire). Cantinarr has completed this requirement and received production access.

### Open beta

Google's [open-testing track ID is `beta`](https://developers.google.com/android-publisher/tracks).
After production access is approved, Play Console → Testing → Open testing needs its own country
selection and tester settings. Add the latest signed, CI-green bundle from the library, preview
and confirm the release, then send the changes for review from Publishing overview. A draft or
an upload alone does not make the open test available; verify the release is available to testers
after Google's review (and publish approved changes if managed publishing is enabled).
If the track is paused, choose **Resume track** and send its activation from Publishing overview
for review too. Confirm the track is **Active** and the release is available before advertising
the beta; a paused track does not deliver installs or updates.

The public opt-in link is <https://play.google.com/apps/testing/codes.julian.cantinarr>. Open
testers join there without an email-list or Google Group invitation. Eligible public-beta-owner
builds go to both open and closed testing automatically. Keep the existing **alpha** track
active during the transition: existing closed testers continue receiving the same builds
without needing to change enrollment. Retiring closed testing is a separate deliberate change
after the transition. Production releases remain a separate decision.

## Store listings (both stores)

Listing copy, graphics, and screenshots are code, managed with fastlane's layouts:

- Play: `app/android/fastlane/metadata/android/en-US/` — `title.txt` (30 chars max),
  `short_description.txt` (80), `full_description.txt` (4000), `changelogs/default.txt`
  ("what's new", rides along with every AAB upload), `images/icon.png` (512×512),
  `images/featureGraphic.png` (1024×500), `images/phoneScreenshots/`, `images/tenInchScreenshots/`.
- App Store: `app/ios/fastlane/metadata/en-US/` — `name.txt` (30), `subtitle.txt` (30),
  `description.txt` (4000), `keywords.txt` (100), `promotional_text.txt` (170),
  `release_notes.txt`, `support_url.txt`, `marketing_url.txt`, `privacy_url.txt`,
  `copyright.txt`; categories in `app/ios/fastlane/metadata/{primary,secondary}_category.txt`;
  screenshots in `app/ios/fastlane/screenshots/en-US/` (device class inferred from pixel size:
  1320×2868 = iPhone 6.9", 2064×2752 = iPad 13").

`.github/workflows/storelisting.yml` pushes the listings to both consoles whenever a merge to
unfrozen `main` touches those paths, or via manual dispatch with a platform picker. Changes on
the active `release/X.Y.Z` require manual dispatch.
The same branch-ownership and exact CI gates apply, and writes share each store's publishing lock.
Main listing changes wait during a candidate freeze. Play sync is skipped
gracefully until `PLAY_SERVICE_ACCOUNT_JSON` exists; App Store sync uses the existing
`APP_STORE_CONNECT_*` secrets (`fastlane listing` in `app/android`, `fastlane metadata` in
`app/ios`).

### Screenshots

Store screenshots are generated, not hand-taken:

1. `app/test/preview/screenshot_main.dart` boots the real app with a stubbed backend that returns
   rich demo data (same pattern as `preview_main.dart`, never shipped).
2. `cd app && flutter build web --release -t test/preview/screenshot_main.dart -o build/web_screens`
3. Serve `build/web_screens` (e.g. `python3 -m http.server 8787 -d build/web_screens`) and run
   `cd app/tool/screenshots && npm install && node shoot.js http://localhost:8787 out`.
   `shoot.js` drives system Chrome via Playwright at exact store pixel sizes (viewport ×
   deviceScaleFactor): iPhone 6.9" 1320×2868, iPad 13" 2064×2752, Play phone 1080×2400,
   Play 10" tablet 1600×2560. Routes and per-shot interactions live in `routes.js`.
4. Copy the outputs into the two fastlane screenshot directories above and commit; the merge
   syncs them to the consoles.

The stores cap what they will show: **10 screenshots per App Store device size, 8 per Play
device type**. A shot's `skip` list in `routes.js` is how the two sets diverge — the numbering is
assigned per device after skips, so each store gets a contiguous run. Demo dates (calendars,
import times) are anchored to the run date rather than written down, because the Releases and
Recently Added screens filter by recency: a hard-coded date eventually shoots an empty screen
that looks like a real answer.

The Play 512 icon and the 1024×500 feature graphic derive from the committed 1024px icon art
(`app/ios/.../appicon.png`, `app/assets/splash_icon.png`).

## iOS (TestFlight / App Store)

### Pipeline

`.github/workflows/testflight.yml` auto-deploys to TestFlight from the public-beta owner for iOS-relevant `app/**`
changes (tests, dev tooling, and markdown excluded). It opens with a `gate` job that waits for the
`CI` run on that exact commit and fails if it isn't green, so no IPA is even built from a commit
the suite hasn't passed — an upload is irreversible and burns a build number permanently.
Build number = latest TestFlight build + 1 (`next_build_number` lane in
`app/ios/fastlane/Fastfile`); signing is manual via the `IOS_DIST_CERT_*` and
`IOS_PROVISIONING_PROFILE_BASE64` secrets (team `2M54LKDR89`, bundle `codes.julian.cantinarr`).
Capability/entitlement changes invalidate the provisioning profile — regenerate it and update the
secret.

### Reaching testers

Uploading a build reaches nobody outside the team on its own. The internal group **Cantinarr
Testers** was created with "Enable automatic distribution", so it picks up every upload by
itself; external groups have no such setting, and a build sits in App Store Connect until
somebody adds it to one.

**There is no checkbox to flip on an existing group, so don't go looking for one.** The
underlying attribute is `hasAccessToAllBuilds`, and the API is explicit that it is create-only:
a `PATCH` naming it answers `ENTITY_ERROR.ATTRIBUTE.NOT_ALLOWED` ("can not be included in a
'UPDATE' operation"), while the same attribute passes schema validation on `POST /v1/betaGroups`.
Turning it on for Public Beta would mean deleting and recreating the group, which destroys the
public link every tester joined through and the README advertises — and Apple documents the
setting only for internal testers, so it may not apply to external groups at all. The
`distribute` job is the supported path, not a workaround.

The `distribute` job closes that gap for **Public Beta** — the group behind the public
[TestFlight link](https://testflight.apple.com/join/bCPDwCsD) in the README — so every build cut
from the public-beta owner reaches those testers without anyone opening App Store Connect;
Internal Only PR builds skip this job. It runs
`scripts/testflight_distribute.py` on a Linux runner rather than distributing inline from
fastlane, because `distribute_external` requires `skip_waiting_for_build_processing: false` and
would hold a 10x-billed macOS runner through Apple's processing. The script waits for the build
to leave processing, submits it for Beta App Review if it still needs that, then adds it to the
group; testers are notified, since `autoNotifyEnabled` is on.

Notes on the shape of that job:

- The group is addressed by **id**, and its name is asserted against the id before anything is
  distributed. A renamed or recreated group fails the run rather than pushing a build at an
  audience nobody chose. Both live in the job's `env:` block.
- Adding a build re-submits nothing when the marketing version is unchanged: Apple auto-approves
  those (`betaReviewState: APPROVED`, `submittedDate: null`). The **first** build of a new
  `pubspec.yaml` version does go through Beta App Review, so it reaches Public Beta whenever
  Apple approves it, not at the end of the workflow run.
- The wait is capped at 45 minutes. `testflight-deploy` concurrency holds for the whole
  workflow, so an Apple-side ingestion stall fails this job instead of blocking the next merge's
  build indefinitely; the build is still there and can be pushed by hand.
- Re-running the failed distribution job is safe — a build already in the group is a no-op,
  so testers are not notified twice. Re-running all jobs builds another binary.
- Adding another external group is one more `distribute` job (or a matrix over group id/name).
  Internal groups are rejected on purpose: they answer `422 Cannot add internal group to a
  build`.

| Secret | Contents |
|---|---|
| `APP_STORE_CONNECT_KEY_ID` / `APP_STORE_CONNECT_ISSUER_ID` / `APP_STORE_CONNECT_API_KEY_B64` | App Store Connect API key (used for build numbers, uploads, and external distribution) |
| `IOS_DIST_CERT_BASE64` / `IOS_DIST_CERT_PASSWORD` | Apple Distribution certificate (.p12) |
| `IOS_PROVISIONING_PROFILE_BASE64` | App Store provisioning profile |

### App Store release

Use the explicit candidate `build_run` procedure above. `APP_VERSION` and `APP_BUILD_NUMBER`
come from that verified build receipt, not the workflow checkout's pubspec or the latest uploaded
build. The release lane submits without a binary upload, with `automatic_release: false` and
`phased_release: true`. Listing content is synced from the selected candidate SHA. The one-time
App Store Connect steps below must be complete before the first submission; the account holder
must also accept any pending Apple developer agreement in the console.

### One-time App Store Connect setup (human)

1. App Review contact: App Store Connect → the app → App Information / the version's App Review
   section → set contact first/last name, email, and **phone number** (Apple requires the phone;
   that's why it isn't committed to the repo — creating review details via the API without it
   fails, see fastlane#20538, so the listing sync deliberately leaves review details to the
   console). Paste the standing note: the app is an open-source client for a self-hosted server,
   and a demo server URL + connect link are provided before each submission.
2. App Privacy (App Store Connect → the app → App Privacy): **reassess this form before the next
   submission** against Apple's current [App privacy details](https://developer.apple.com/app-store/app-privacy-details/)
   definitions. Account for optional AI prompts/context, the self-hosted server's retained data,
   and the community push relay's stored identifiers, tokens, notification titles, and delivery
   records. Review **User ID**, **Device ID**, and applicable **User Content** categories and their
   **App Functionality** purpose against the actual data flow. An optional feature does not
   automatically qualify for Apple's optional-disclosure exception; ongoing collection after
   permission is granted still needs review. Confirm linked-to-user answers and the live console
   selections before submission. Do not reuse the previous categorical "Data Not Collected"
   answer without that review. See [the privacy policy](privacy-policy.md).
3. Age rating questionnaire: all descriptors None, gambling No, unrestricted web access No
   (the in-app web view is scoped to auth/help flows). Strictly accurate result is 4+; setting
   "Mature/Suggestive Themes: Infrequent/Mild" → **12+** is the conservative choice for an app
   that displays TMDB artwork for arbitrary titles, and is what comparable media managers use.
4. Content rights: the app shows third-party content (TMDB metadata/artwork) → confirm you have
   the rights (TMDB public API terms; attribution included in the listing copy).
5. App availability + price (free), and App Review notes: reviewers need a reachable Cantinarr
   server — paste the demo server URL and a fresh connect link into the review notes before every
   submission.

## Google Play — remaining console forms (human, one-time)

Prepared answers, in console order:

- **Data safety**: **reassess before the next submission** against Google's current
  [Data safety definitions](https://support.google.com/googleplay/android-developer/answer/10787469).
  Google defines collection around off-device transmission, not only data received by the app
  developer. Review optional AI prompts/context, server-side retention, the native FCM SDK,
  and push gateway registration and delivery. The community relay stores identifiers and tokens
  plus delivery records containing notification titles. Assess **User IDs**, **Device or other
  IDs**, applicable **Other user-generated content**, and the **App functionality** purpose.
  Confirm whether collection is optional and whether a sharing exception applies to each actual
  flow; an optional feature alone does not establish an exception. Do not reuse the previous
  categorical "No data collected" answer without this review. Verify the live console selections
  and [privacy policy](privacy-policy.md) agree before submission. Ads remain No.
- **Content rating (IARC)**: category "Utility, Productivity, Communication, or Other"; no
  violence/sexuality/language/gambling in app content; users can exchange text only with members
  of their own private server (no public UGC, no location sharing). Expected result: Everyone.
- **Target audience**: 18+ (do not tick under-13 age bands — that triggers Families policy).
- **Privacy policy URL**:
  `https://github.com/windoze95/cantinarr/blob/main/docs/privacy-policy.md`
