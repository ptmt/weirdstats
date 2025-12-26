Hetzner VPS deployment guide

Prereqs
- Ubuntu 22.04 or newer
- Domain with DNS A/AAAA record
- Go 1.22 installed on server

Setup
1) Create a non-root user and add SSH keys.
2) Install system deps:
   - apt-get update
   - apt-get install -y sqlite3 ufw
3) Create app directory:
   - mkdir -p /opt/weirdstats
   - chown youruser:youruser /opt/weirdstats
4) Copy the repo to /opt/weirdstats and set up `.env`.

Environment
- STRAVA_CLIENT_ID
- STRAVA_CLIENT_SECRET
- STRAVA_ACCESS_TOKEN
- STRAVA_ACCESS_TOKEN_EXPIRES_AT (unix seconds)
- STRAVA_REFRESH_TOKEN
- STRAVA_AUTH_BASE_URL (optional override, default https://www.strava.com)
- STRAVA_WEBHOOK_SECRET
- STRAVA_VERIFY_TOKEN
- MAPS_API_KEY
- DATABASE_PATH (e.g. /opt/weirdstats/weirdstats.db)

Systemd service (example)
- Create `/etc/systemd/system/weirdstats.service`:

[Unit]
Description=Weirdstats
After=network.target

[Service]
User=youruser
WorkingDirectory=/opt/weirdstats
EnvironmentFile=/opt/weirdstats/.env
ExecStart=/opt/weirdstats/bin/weirdstats
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target

Build and run
- go build -o /opt/weirdstats/bin/weirdstats ./cmd/weirdstats
- systemctl daemon-reload
- systemctl enable --now weirdstats

Firewall
- ufw allow OpenSSH
- ufw allow 80/tcp
- ufw allow 443/tcp
- ufw enable

Reverse proxy
- Use Caddy or Nginx to terminate TLS and forward to the app port.

Backups
- Regularly back up the SQLite file in DATABASE_PATH.
