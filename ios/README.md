# iOS App

This is a small SwiftUI app for the native sign-in flow.

It uses:

- the Strava app first when installed
- `ASWebAuthenticationSession` as the fallback when Strava is unavailable
- the backend mobile endpoints added in this repo
- a custom callback URL scheme: `weirdstats://auth/strava`
- Keychain for the backend bearer token

## Backend

Run the server with a reachable base URL so Strava can call back into:

```bash
BASE_URL=weirdstats.com
STRAVA_CLIENT_ID=...
STRAVA_CLIENT_SECRET=...
go run ./cmd/weirdstats
```

`MOBILE_APP_REDIRECT_URL` is optional because the app passes `weirdstats://auth/strava` as `app_redirect` when it starts the mobile flow. The backend normalizes `BASE_URL=weirdstats.com` to `https://weirdstats.com` automatically.

## Generate the Xcode Project

From the repo root:

```bash
xcodegen generate --spec ios/project.yml
open ios/WeirdStats.xcodeproj
```

## CI and TestFlight

The [iOS workflow](../.github/workflows/ios.yml) uses GitHub's `macos-26` runner
with Xcode 26.3 and regenerates the project from `project.yml` on every run.
It uses native `xcodebuild` signing and upload; Fastlane is not required.

| Trigger | Result |
| --- | --- |
| iOS changes in a pull request or push to `main` | Validate signing configuration logic, build for the simulator, and archive Release without signing |
| Push an `ios/vMAJOR.MINOR.PATCH` tag | Run the build checks, sign a Release archive, and upload to App Store Connect for TestFlight |
| Actions → iOS → Run workflow | Run the build checks; select `publish` to also upload |

Uploads are serialized and are never cancelled by a newer upload. The upload
job uses the `testflight` GitHub environment. PR builds do not receive signing
credentials. Debug symbols are retained as workflow artifacts for 30 days.

### First-time Apple setup

1. In your paid Apple Developer account, register the explicit App ID
   `com.ptmt.weirdstats`, then create an iOS app record with that bundle ID in
   [App Store Connect](https://appstoreconnect.apple.com/). Keep the bundle ID
   consistent with `project.yml` and `scripts/signing_metadata.py`.
2. Create an **Apple Distribution** certificate. Export the certificate **with
   its private key** from Keychain Access as a password-protected `.p12` file.
   The Apple Development certificate used for local device builds is insufficient.
3. Create an **App Store Connect distribution provisioning profile** for
   `com.ptmt.weirdstats` and that distribution certificate. Download the
   `.mobileprovision` file. CI checks its app ID, team, type, and expiry.
   See [Apple's manual signing instructions](https://help.apple.com/xcode/mac/current/en.lproj/devcac6ab5b3.html).
4. In App Store Connect → Users and Access → Integrations → App Store Connect
   API → Team Keys, generate a **team API key** with **App Manager** access.
   Download its `.p8` private key and note the key ID and issuer ID.
   This workflow expects a team key with an issuer ID, not an individual key.
   See [Apple's API key setup](https://developer.apple.com/help/app-store-connect/get-started/app-store-connect-api/).
5. In the app's TestFlight tab, create an internal testing group and enable
   **automatic distribution**. Add your internal testers there. After an upload,
   Apple must process the build before it appears in TestFlight. External testing
   needs its own tester group, beta information, and Apple's beta review.
   See [internal testing setup](https://developer.apple.com/help/app-store-connect/test-a-beta-version/add-internal-testers/).

### GitHub configuration

Create an environment named `testflight` in repository Settings → Environments.
Add the following environment variable and secrets (repository-level values also
work). Never commit the signing files or paste private keys into workflow YAML.
The temporary keychain password is generated during each run.

| Name | Kind | Value |
| --- | --- | --- |
| `APPLE_TEAM_ID` | Variable | Your Apple Developer team ID |
| `APPLE_DISTRIBUTION_CERTIFICATE_BASE64` | Secret | Base64 of the exported distribution `.p12`, including its private key |
| `APPLE_DISTRIBUTION_CERTIFICATE_PASSWORD` | Secret | Password used when exporting the `.p12` |
| `APPLE_PROVISIONING_PROFILE_BASE64` | Secret | Base64 of the matching App Store Connect `.mobileprovision` |
| `APP_STORE_CONNECT_KEY_ID` | Secret | Team API key ID |
| `APP_STORE_CONNECT_ISSUER_ID` | Secret | Team API issuer ID |
| `APP_STORE_CONNECT_PRIVATE_KEY` | Secret | Entire raw `.p8` file, including BEGIN/END lines; do not base64 this value |

For example, on macOS, after creating the environment:

```bash
gh variable set APPLE_TEAM_ID --repo ptmt/weirdstats --env testflight --body YOUR_TEAM_ID
base64 -i /path/to/distribution.p12 | gh secret set APPLE_DISTRIBUTION_CERTIFICATE_BASE64 --repo ptmt/weirdstats --env testflight
base64 -i /path/to/app-store.mobileprovision | gh secret set APPLE_PROVISIONING_PROFILE_BASE64 --repo ptmt/weirdstats --env testflight
gh secret set APP_STORE_CONNECT_PRIVATE_KEY --repo ptmt/weirdstats --env testflight < /path/to/AuthKey.p8
gh secret set APPLE_DISTRIBUTION_CERTIFICATE_PASSWORD --repo ptmt/weirdstats --env testflight
gh secret set APP_STORE_CONNECT_KEY_ID --repo ptmt/weirdstats --env testflight
gh secret set APP_STORE_CONNECT_ISSUER_ID --repo ptmt/weirdstats --env testflight
```

The last three commands prompt for values so they need not appear in shell
history. Renew expired certificates/profiles in Apple Developer and replace the
corresponding secrets. The credential import follows
[GitHub's macOS signing guidance](https://docs.github.com/en/actions/how-tos/deploy/deploy-to-third-party-platforms/sign-xcode-applications).

### Publish a build

After merging the workflow into `main` and configuring the credentials:

```bash
# Upload the version configured in ios/project.yml:
gh workflow run ios.yml --repo ptmt/weirdstats --ref main -f publish=true

# Or publish a particular version from the current commit:
git tag ios/v0.1.0
git push origin ios/v0.1.0
```

Tag releases take their marketing version from the tag; manual runs use
`MARKETING_VERSION` in `project.yml`. Every upload uses
`GITHUB_RUN_NUMBER.GITHUB_RUN_ATTEMPT` as its build number, so rerunning a workflow
gets a new number. Xcode's automatic version rewriting is disabled. If the app
already has builds uploaded by another process, the next build number must be
higher than the existing builds for that version.

The script uploads the archive but does not wait for Apple's processing or submit
an App Store release. A successful workflow means the upload completed; inspect
App Store Connect for processing failures. Internal testers receive the build
automatically only when their group has automatic distribution enabled.

### Distribution metadata and local checks

`Info.plist` takes both version numbers from Xcode build settings. The app includes
a 1024×1024 opaque AppIcon, iPad orientation declarations, and a privacy manifest
declaring app-local UserDefaults access (`CA92.1`) for the saved backend URL.
`ITSAppUsesNonExemptEncryption` is false because the client uses only Apple's
HTTPS/Keychain facilities; revisit that value if custom encryption is introduced.
See Apple's [required API reasons](https://developer.apple.com/documentation/bundleresources/app-privacy-configuration/nsprivacyaccessedapitypes/nsprivacyaccessedapitypereasons)
and [encryption export guidance](https://developer.apple.com/documentation/security/complying-with-encryption-export-regulations).

The AppIcon was generated with the built-in imagegen tool using the existing
wordmark as a reference, then resized to 1024×1024. Saved asset:
`WeirdStats/Assets.xcassets/AppIcon.appiconset/AppIcon.png`.
Generation prompt: “Use case: logo-brand. Asset type: iOS App Store icon, 1024x1024
square opaque PNG. Reference image: the existing WeirdStats wordmark; preserve
its bold condensed letter style and vivid purple/cyan/green/magenta gradient
identity. Create a simple icon with one large centered capital W in that gradient
on a solid near-black background, clearly legible at small sizes. The W occupies
roughly 65% of the square width and height. Flat graphic, crisp edges. Full bleed
opaque background, no rounded corners baked in, no transparency, no additional
letters, no text captions, no border, no mockup, no shadows.”

From the repo root, without signing credentials:

```bash
xcodegen generate --spec ios/project.yml
python3 -m unittest discover -s ios/scripts/tests -v
bash -n ios/scripts/testflight.sh
xcodebuild -project ios/WeirdStats.xcodeproj -scheme WeirdStats \
  -configuration Release -destination 'generic/platform=iOS' \
  -archivePath /tmp/WeirdStats.xcarchive CODE_SIGNING_ALLOWED=NO archive
```

`scripts/testflight.sh` is intended for the disposable GitHub-hosted runner; it
changes the runner's keychain search list. Apple signing and upload can only be
verified with real distribution credentials. These build checks do not exercise
Strava sign-in: the backend must remain reachable from testers' devices, and the
client session JSON decoding issue identified in the iOS review still needs a
separate fix before testers can sign in successfully.

## Notes

- The sign-in flow asks the backend for signed launch URLs, opens `strava://oauth/mobile/authorize` when possible, and falls back to `ASWebAuthenticationSession` with the Strava web authorize URL.
- The backend still owns the real OAuth callback at `https://weirdstats.com/connect/strava/mobile/callback`, so you do not need universal links or an app-owned HTTPS callback for this app.
- `SFSafariViewController` is still not used here.
- The app expects the backend to expose:
  - `GET /connect/strava/mobile`
  - `GET /connect/strava/mobile/callback`
  - `POST /api/mobile/session/exchange`
  - `GET /api/mobile/me`
  - `GET /api/mobile/activities`
