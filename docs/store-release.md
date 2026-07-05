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

### App Store release (human, per release)

TestFlight builds are submitted to App Store review from App Store Connect (pick the build,
attach release notes, submit).
