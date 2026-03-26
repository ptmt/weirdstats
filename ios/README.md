# iOS Prototype

This is a small SwiftUI prototype for the native sign-in flow.

It uses:

- `ASWebAuthenticationSession` for Strava OAuth
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

- The sign-in flow intentionally uses `ASWebAuthenticationSession`, not `SFSafariViewController`.
- The prototype expects the backend to expose:
  - `GET /connect/strava/mobile`
  - `GET /connect/strava/mobile/callback`
  - `POST /api/mobile/session/exchange`
  - `GET /api/mobile/me`
  - `GET /api/mobile/activities`
