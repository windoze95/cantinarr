# Store release playbook

How app builds reach TestFlight and Google Play, what signing material exists, and the one-time
console setup each store needs. CI automates everything that can be automated; the remaining
human steps are listed explicitly. Versioning is shared: bump `version:` in `app/pubspec.yaml`
for a user-facing version change — build numbers/version codes are computed per store by CI.

## Android (Google Play)

### Pipeline

`.github/workflows/playstore.yml` runs on every merge to `main` that touches Android-relevant
`app/**` paths (web/ios/desktop subdirs excluded), and on manual dispatch (inputs: track
`beta`/`internal`, release status `completed`/`draft`).

1. Version code = max version code across all Play tracks + 1 (`next_build_number` lane in
   `app/android/fastlane/Fastfile`); version name = `pubspec.yaml` version minus the `+` suffix.
2. The AAB is signed with the upload keystore from the `ANDROID_KEYSTORE_*` secrets and attached
   to the run as an artifact — every run, upload or not.
3. With `PLAY_SERVICE_ACCOUNT_JSON` set, the `beta` lane uploads the AAB to the Play **beta**
   (closed testing) track. Without it, the upload is skipped and the run stays green.

Runs are serialized (`concurrency: playstore-deploy`) because two concurrent runs would compute
the same version code.

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
   → create a track named `beta` → create release → upload the AAB. Accepting Play App Signing
   here enrolls the upload key.
4. Google Cloud console: pick/create a project → IAM & Admin → Service accounts → create
   (e.g. `play-publisher`) → Keys → add a JSON key.
5. Play Console → Users and permissions → Invite new users → the service account's email →
   grant release permissions (releases to testing tracks) or Admin.
6. `gh secret set PLAY_SERVICE_ACCOUNT_JSON < key.json` — from here on, every merge to `main`
   ships to beta automatically.
7. Finish the listing prerequisites in the console before promoting beyond testing: store
   listing (copy + graphics), data safety form, content rating questionnaire, privacy policy URL.
8. Closed testing → Testers: add an email list or Google Group and share the opt-in link.
9. For native Android passkeys on a deployment: after Play App Signing is enrolled, copy the
   **app signing key** SHA-256 from Play Console → App integrity into the server's
   `CANTINARR_ANDROID_CERT_SHA256_FINGERPRINTS` (the upload key's fingerprint is the wrong one —
   Google re-signs distribution builds).

Personal developer accounts created after Nov 13, 2023 must run a closed test with **12+ opted-in
testers for 14 continuous days** before they can apply for production access (the beta track
satisfies this; the console dashboard tracks progress and then offers a production-access
questionnaire).

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
`main` touches those paths (and via manual dispatch with a platform picker). Play sync is skipped
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

The Play 512 icon and the 1024×500 feature graphic derive from the committed 1024px icon art
(`app/ios/.../appicon.png`, `app/assets/splash_icon.png`).

## iOS (TestFlight / App Store)

### Pipeline

`.github/workflows/testflight.yml` auto-deploys to TestFlight on `main` for iOS-relevant `app/**`
changes. Build number = latest TestFlight build + 1 (`next_build_number` lane in
`app/ios/fastlane/Fastfile`); signing is manual via the `IOS_DIST_CERT_*` and
`IOS_PROVISIONING_PROFILE_BASE64` secrets (team `2M54LKDR89`, bundle `codes.julian.cantinarr`).
Capability/entitlement changes invalidate the provisioning profile — regenerate it and update the
secret.

| Secret | Contents |
|---|---|
| `APP_STORE_CONNECT_KEY_ID` / `APP_STORE_CONNECT_ISSUER_ID` / `APP_STORE_CONNECT_API_KEY_B64` | App Store Connect API key (used for build numbers + uploads) |
| `IOS_DIST_CERT_BASE64` / `IOS_DIST_CERT_PASSWORD` | Apple Distribution certificate (.p12) |
| `IOS_PROVISIONING_PROFILE_BASE64` | App Store provisioning profile |

### App Store release

Submitting for review is one workflow run: **Submit App Store Release**
(`.github/workflows/appstore-release.yml`, manual dispatch, type `submit` to confirm). It runs the
`release` lane: finds the latest processed TestFlight build of the current `pubspec.yaml` version
and submits it with phased release on and manual rollout off — release notes and listing content
come from the in-repo metadata. Before the *first* submission, the one-time App Store Connect
steps below must be done in the UI.

### One-time App Store Connect setup (human)

1. App Privacy (App Store Connect → the app → App Privacy): answer "do you collect data" → **No**
   → the label becomes "Data Not Collected". Rationale if asked: the app has no developer backend,
   no analytics/ads/crash SDKs; it talks only to the user's own self-hosted server, and poster art
   comes from the TMDB CDN as plain image requests.
2. Age rating questionnaire: all descriptors None, gambling No, unrestricted web access No
   (the in-app web view is scoped to auth/help flows). Strictly accurate result is 4+; setting
   "Mature/Suggestive Themes: Infrequent/Mild" → **12+** is the conservative choice for an app
   that displays TMDB artwork for arbitrary titles, and is what comparable media managers use.
3. Content rights: the app shows third-party content (TMDB metadata/artwork) → confirm you have
   the rights (TMDB public API terms; attribution included in the listing copy).
4. App availability + price (free), and App Review notes: reviewers need a reachable Cantinarr
   server — paste the demo server URL and a fresh connect link into the review notes before every
   submission.

## Google Play — remaining console forms (human, one-time)

Prepared answers, in console order:

- **Data safety**: "Does your app collect or share any of the required user data types?" → **No**
  (self-hosted client; the developer operates no backend and receives nothing; app↔own-server
  traffic and TMDB CDN image fetches are servicing the user's request, not collection). Ads: No.
  Result shows "No data collected".
- **Content rating (IARC)**: category "Utility, Productivity, Communication, or Other"; no
  violence/sexuality/language/gambling in app content; users can exchange text only with members
  of their own private server (no public UGC, no location sharing). Expected result: Everyone.
- **Target audience**: 18+ (do not tick under-13 age bands — that triggers Families policy).
- **Privacy policy URL**:
  `https://github.com/windoze95/cantinarr/blob/main/docs/privacy-policy.md`
