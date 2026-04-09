# iOS Prototype

This is a small SwiftUI prototype for the native sign-in flow.

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

`MOBILE_APP_REDIRECT_URL` is optional for this prototype because the app passes `weirdstats://auth/strava` as `app_redirect` when it starts the mobile flow. The backend normalizes `BASE_URL=weirdstats.com` to `https://weirdstats.com` automatically.

## Generate the Xcode Project

From the repo root:

```bash
xcodegen generate --spec ios/project.yml
open ios/WeirdStatsPrototype.xcodeproj
```

## Notes

- The sign-in flow asks the backend for signed launch URLs, opens `strava://oauth/mobile/authorize` when possible, and falls back to `ASWebAuthenticationSession` with the Strava web authorize URL.
- The backend still owns the real OAuth callback at `https://weirdstats.com/connect/strava/mobile/callback`, so you do not need universal links or an app-owned HTTPS callback for this prototype.
- `SFSafariViewController` is still not used here.
- The prototype expects the backend to expose:
  - `GET /connect/strava/mobile`
  - `GET /connect/strava/mobile/callback`
  - `POST /api/mobile/session/exchange`
  - `GET /api/mobile/me`
  - `GET /api/mobile/activities`
