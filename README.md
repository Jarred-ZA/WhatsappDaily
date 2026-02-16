# WhatsApp Daily Summary

Automated daily WhatsApp action-item summary delivered at 6am SAST. Reads your recent WhatsApp conversations, extracts commitments, requests, and follow-ups using Claude AI, and sends a categorized summary back to you on WhatsApp.

## Architecture

Two services deployed to [Railway](https://railway.app):

- **whatsapp-bridge/** - Go service (always-on) that maintains a WhatsApp Web connection, stores messages in SQLite, and exposes a REST API.
- **daily-summary/** - Python cron job (`0 4 * * *` UTC = 6am SAST) that fetches recent messages from the bridge, analyzes them with Claude Sonnet, and sends the summary via WhatsApp.

Communication between services uses Railway private networking, secured with a shared API key.

## Setup

### Prerequisites

- [Railway](https://railway.app) account
- [Anthropic API key](https://console.anthropic.com)
- WhatsApp account to link

### Deploy to Railway

1. Create a new Railway project linked to this repo
2. Create the **whatsapp-bridge** service:
   - Root directory: `/`
   - Dockerfile: `whatsapp-bridge/Dockerfile`
   - Attach a volume mounted at `/app/store`
   - Set environment variables (see below)
3. Once deployed, open the public URL to scan the QR code and link WhatsApp
4. Create the **daily-summary** cron service:
   - Root directory: `/`
   - Dockerfile: `daily-summary/Dockerfile`
   - Set environment variables (see below)

### Environment Variables

**whatsapp-bridge:**

| Variable | Required | Description |
|---|---|---|
| `BRIDGE_API_KEY` | Yes | Shared secret for API auth |
| `STORE_DIR` | No | SQLite store path (default: `store`) |
| `PORT` | No | HTTP port (auto-set by Railway) |
| `PAIR_PHONE` | No | Phone number for pair-code auth (alternative to QR) |

**daily-summary:**

| Variable | Required | Description |
|---|---|---|
| `BRIDGE_INTERNAL_URL` | Yes | Bridge URL (e.g. `http://whatsapp-bridge.railway.internal:8080`) |
| `BRIDGE_API_KEY` | Yes | Must match the bridge service key |
| `ANTHROPIC_API_KEY` | Yes | Anthropic API key for Claude |
| `RECIPIENT_PHONE` | No | WhatsApp number to receive summary |
| `MESSAGE_HOURS` | No | Hours of history to analyze (default: `48`) |
| `DRY_RUN` | No | Set to `true` to print summary without sending |

## API Endpoints

| Endpoint | Auth | Description |
|---|---|---|
| `GET /` | No | Web UI for QR authentication |
| `GET /api/health` | No | Connection status |
| `GET /api/auth/status` | No | Auth state (connected/QR/pair code) |
| `POST /api/auth/start` | Yes | Start new QR auth flow |
| `POST /api/auth/logout` | Yes | Disconnect and logout |
| `GET /api/messages/recent?hours=48` | Yes | Fetch recent messages |
| `POST /api/send` | Yes | Send a WhatsApp message |
| `POST /api/download` | Yes | Download media from a message |
| `POST /api/transcribe` | Yes | Transcribe a voice note |

## Security

- All mutating and data endpoints require `X-API-Key` header
- Health and auth status are read-only and unauthenticated
- Auth start/logout are protected by API key to prevent session hijacking
- Inter-service communication via Railway private networking (not internet-routable)
- All secrets stored as Railway environment variables
- SQLite database on a private Railway volume
- No secrets in source code

## License

Private repository.
