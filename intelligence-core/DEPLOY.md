# Deploying intelligence-core to Railway

## Prerequisites
- WhatsappDaily project already running on Railway with the `whatsapp-bridge` service
- Railway CLI logged in, or access to the Railway dashboard

## Step 1: Create the service

```bash
# If using Railway CLI:
railway link  # Select the WhatsappDaily project
railway service create intelligence-core
```

Or in the dashboard: **"+ New"** → **"GitHub Repo"** → select `Jarred-ZA/WhatsappDaily`

## Step 2: Configure the build

The service must use the Dockerfile at `intelligence-core/Dockerfile` with the **repo root** as build context.

```bash
# CLI:
railway service select intelligence-core
railway settings set dockerfilePath intelligence-core/Dockerfile
```

Or in dashboard: **Settings** → **Build** → set **Dockerfile Path** to `intelligence-core/Dockerfile`. Leave Root Directory empty.

The `intelligence-core/railway.toml` file also declares this config and should be auto-detected.

## Step 3: Set environment variables

```bash
railway service select intelligence-core
railway variables set BRIDGE_URL=http://whatsapp-bridge.railway.internal:8080
railway variables set BRIDGE_API_KEY=<same API key as the whatsapp-bridge service>
railway variables set ANTHROPIC_API_KEY=<your Anthropic API key>
railway variables set RECIPIENT_PHONE=27722066795
railway variables set DATA_DIR=/data
railway variables set MESSAGE_HOURS=48
railway variables set SUMMARY_HOUR=4
```

**Finding the correct BRIDGE_URL:**
- Go to the whatsapp-bridge service → **Settings** → **Networking** → **Private Networking**
- The internal URL format is `http://<service-name>.railway.internal:8080`
- Common values: `http://whatsapp-bridge.railway.internal:8080` or `http://WhatsappDaily.railway.internal:8080`

**Finding the BRIDGE_API_KEY:**
- Go to the whatsapp-bridge service → **Variables** → copy the `API_KEY` value

**ANTHROPIC_API_KEY:**
- Must be a valid Anthropic API key (starts with `sk-ant-`)

## Step 4: Attach a persistent volume

```bash
railway volume create --mount /data
```

Or in dashboard: **Settings** → **Volumes** → Add volume mounted at `/data`

This volume stores:
- `events.db` — unified event store
- `memory/` — persistent knowledge banks (people, projects, organizations)
- `collection_state.json` — tracks last collection timestamp per source

## Step 5: Deploy

If GitHub auto-deploy is connected, pushing to `main` triggers a build automatically.

```bash
# Manual deploy if needed:
railway up
```

## Step 6: Verify

```bash
railway logs -s intelligence-core
```

You should see:
```
=== Intelligence Core V2 ===
Data dir: /data
Bridge URL: http://whatsapp-bridge.railway.internal:8080
Running initial collection...
WhatsApp: stored N new events
Scheduler started. Next synthesis at 04:00 UTC. WhatsApp collection every 30 min.
```

## No public domain needed

intelligence-core is a background worker — it collects data on a schedule and sends briefings via the WhatsApp bridge. It does not serve HTTP traffic and does not need a public domain.

## Service behavior

| Schedule | Action |
|----------|--------|
| Every 30 min | Collect WhatsApp messages via bridge REST API |
| 04:00 UTC (06:00 SAST) | Run daily synthesis → send briefing to WhatsApp |

## Troubleshooting

**"Failed to fetch from bridge API"** — Check BRIDGE_URL and BRIDGE_API_KEY. Ensure the whatsapp-bridge service has private networking enabled.

**"No events to synthesize"** — Collection state may be too recent. Set `MESSAGE_HOURS=48` or delete `/data/collection_state.json` to force a full re-collection.

**Empty briefing** — Check ANTHROPIC_API_KEY is valid. Check `railway logs` for API errors.
