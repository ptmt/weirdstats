# WeirdStats

A personal Strava analytics app that tracks additional statistics.

## Features

- Sync activities from Strava
- Detect stops during rides/runs
- Identify stops near traffic lights using OpenStreetMap data
- View activity details with route visualization
- Download activity data as JSON for testing

## Running Locally

### Prerequisites

- Go 1.22+
- Strava API credentials ([create an app](https://www.strava.com/settings/api))

### Setup

1. Clone the repository:
   ```bash
   git clone https://github.com/yourusername/weirdstats.git
   cd weirdstats
   ```

2. Create a `.env` file:
   ```bash
   STRAVA_CLIENT_ID=your_client_id
   STRAVA_CLIENT_SECRET=your_client_secret
   DATABASE_PATH=weirdstats.db
   SERVER_ADDR=:8080
   ```

3. Run the app:
   ```bash
   go run ./cmd/weirdstats
   ```

4. Open http://localhost:8080

## Docker

### Build the image

```bash
docker build -t weirdstats .
```

### Run the container

```bash
docker run -p 8080:8080 \
  -v weirdstats-data:/data \
  -e STRAVA_CLIENT_ID=your_client_id \
  -e STRAVA_CLIENT_SECRET=your_client_secret \
  weirdstats
```

Or with an env file:

```bash
docker run -p 8080:8080 \
  -v weirdstats-data:/data \
  --env-file .env \
  weirdstats
```

### Docker Compose (optional)

```yaml
services:
  weirdstats:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - weirdstats-data:/data
    env_file:
      - .env

volumes:
  weirdstats-data:
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_PATH` | Path to SQLite database | `weirdstats.db` |
| `SERVER_ADDR` | Server listen address | `:8080` |
| `STRAVA_CLIENT_ID` | Strava OAuth client ID | required |
| `STRAVA_CLIENT_SECRET` | Strava OAuth client secret | required |
| `STRAVA_REDIRECT_URL` | OAuth callback URL | auto-detected |
| `OVERPASS_URL` | Overpass API endpoint | public servers |
| `OVERPASS_TIMEOUT_SECONDS` | Overpass query timeout | `10` |
| `OVERPASS_CACHE_HOURS` | Cache TTL for map data | `24` |
| `WORKER_POLL_INTERVAL_MS` | Background worker interval | `2000` |

## CI/CD

GitHub Actions workflows are included:

- **Test** (`test.yml`): Runs on every push and PR to `main`
- **Docker** (`docker.yml`): Builds and publishes to GitHub Container Registry on push to `main` or version tags

### Pull the published image

```bash
docker pull ghcr.io/yourusername/weirdstats:main
```

## Development

### Run tests

```bash
go test ./...
```

### Run tests with coverage

```bash
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## License

MIT
