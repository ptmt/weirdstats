Notes
- Strava access tokens are short-lived; use refresh tokens with STRAVA_CLIENT_ID/STRAVA_CLIENT_SECRET and STRAVA_REFRESH_TOKEN. The app stores tokens in SQLite and refreshes when expired.
- Keep secrets in `.env` and do not commit them.
- Token storage currently assumes a single user (user_id = 1).
