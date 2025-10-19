# Deployment Guide

This guide is for self-hosting Ready-to-Review. If you're using the SaaS version, see [SETUP.md](SETUP.md) instead.

## Architecture Overview

Ready-to-Review consists of two services:

1. **slacker** - Main bot server that handles GitHub webhooks and Slack notifications
2. **slacker-registrar** - OAuth-only service for multi-workspace installations

### When to Use Each OAuth Approach

**Single-Workspace (Self-Hosting for Your Organization):**
- Use the OAuth handlers built into the main server
- Deploy only `slacker`, no need for `slacker-registrar`
- Simpler setup with fewer moving parts
- OAuth credentials configured directly in main server

**Multi-Workspace (SaaS / Slack Marketplace Distribution):**
- Use the separate `slacker-registrar` service
- Required for supporting multiple Slack workspaces
- Each workspace gets isolated token storage
- Registrar handles OAuth for all workspaces, main server handles events

**Important:** The main server OAuth is designed ONLY for single-workspace installations. For multi-workspace support, you MUST use the registrar.

---

## Prerequisites

- **Google Cloud Platform** project with Cloud Run enabled
- **Slack App** configured at https://api.slack.com/apps
- **GitHub App** configured with webhook access
- **ko** CLI tool for building and deploying Go apps ([install](https://ko.build/install/))
- **gcloud** CLI configured for your GCP project

---

## Slack App Configuration

### Step 1: Create a Slack App

1. Go to https://api.slack.com/apps
2. Click **"Create New App"**
3. Choose **"From scratch"**
4. Name your app (e.g., "Ready-to-Review")
5. Select your development workspace

### Step 2: Configure OAuth & Permissions

1. In your app settings, go to **"OAuth & Permissions"**
2. Under **"Scopes"**, add these **Bot Token Scopes**:
   - `channels:history` - View messages in public channels
   - `channels:read` - View basic channel information
   - `chat:write` - Send messages as the bot
   - `chat:write.public` - Send messages to channels bot isn't in
   - `commands` - Add slash commands
   - `groups:history` - View messages in private channels (if invited)
   - `groups:read` - View basic private channel information
   - `im:write` - Send direct messages
   - `reactions:write` - Add emoji reactions
   - `team:read` - View workspace name and domain
   - `users:read` - View people in the workspace
   - `users:read.email` - View email addresses

3. Under **"Redirect URLs"**, add your OAuth callback URL:
   ```
   https://your-registrar-url.run.app/oauth/callback
   ```

### Step 3: Find Your OAuth Credentials

You'll need two values from the **"App Credentials"** section:

1. Go to **"Basic Information"** in your app settings
2. Scroll to **"App Credentials"**
3. Copy these values:
   - **Client ID** (e.g., `9426269265270.9443955134789`)
   - **Client Secret** (click "Show" to reveal)

**Important:** Keep your Client Secret secure - never commit it to Git or expose it publicly.

### Step 4: Configure Event Subscriptions (for main server)

1. Go to **"Event Subscriptions"**
2. Enable events
3. Set **Request URL** to: `https://your-server-url.run.app/slack/events`
4. Subscribe to **bot events**:
   - `app_home_opened` - Update user's home tab
   - `message.im` - Receive direct messages
5. Save changes

### Step 5: Enable App Home

1. Go to **"App Home"**
2. Check **"Home Tab"** to enable the dashboard
3. Uncheck **"Messages Tab"** if you don't need it

---

## Environment Variables

### Single-Workspace Setup

For self-hosting in your own workspace, configure only the main server:

#### Main Server (`slacker`)

```bash
# GitHub App credentials
GITHUB_APP_ID=123456
GITHUB_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----..."

# Slack webhook verification (from "App Credentials" → "Signing Secret")
SLACK_SIGNING_SECRET=abc123...

# Sprinkler WebSocket endpoint (for GitHub events)
SPRINKLER_URL=wss://sprinkler.example.com/ws

# OAuth credentials (for single-workspace OAuth)
SLACK_CLIENT_ID=9426269265270.9443955134789
SLACK_CLIENT_SECRET=abc123...

# Optional: Cloud Datastore for cross-instance state coordination
# If unset, uses JSON files only (sufficient for single-instance deployments)
DATASTORE=slacker              # Database ID to use (e.g., "slacker", "(default)")
# GCP_PROJECT=my-project       # Optional: Auto-detected on Cloud Run
```

### Multi-Workspace Setup

For SaaS/Marketplace distribution, configure both services:

#### Main Server (`slacker`)

```bash
# GitHub App credentials
GITHUB_APP_ID=123456
GITHUB_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----..."

# Slack webhook verification (from "App Credentials" → "Signing Secret")
SLACK_SIGNING_SECRET=abc123...

# Sprinkler WebSocket endpoint (for GitHub events)
SPRINKLER_URL=wss://sprinkler.example.com/ws

# Datastore for cross-instance coordination (RECOMMENDED for multi-instance)
DATASTORE=slacker              # Database ID to use
# GCP_PROJECT=my-project       # Optional: Auto-detected on Cloud Run

# NO OAUTH CREDENTIALS - registrar handles OAuth for multi-workspace
```

#### Registrar (`slacker-registrar`)

```bash
# OAuth credentials ONLY (from "App Credentials" in Slack)
SLACK_CLIENT_ID=9426269265270.9443955134789
SLACK_CLIENT_SECRET=abc123...
```

**Note:** The registrar does **NOT** need `SLACK_SIGNING_SECRET` because it only handles OAuth flows, not webhook events.

---

## Google Secret Manager Setup

Both services can fetch secrets from Google Secret Manager instead of environment variables.

### Store OAuth Credentials

```bash
# Store Client ID
echo -n "9426269265270.9443955134789" | \
  gcloud secrets create SLACK_CLIENT_ID --data-file=-

# Store Client Secret (replace with your actual secret)
echo -n "your-client-secret-here" | \
  gcloud secrets create SLACK_CLIENT_SECRET --data-file=-
```

### Grant Access to Service Accounts

```bash
PROJECT=your-gcp-project

# Grant access to main server
gcloud secrets add-iam-policy-binding SLACK_CLIENT_ID \
  --member="serviceAccount:slacker@${PROJECT}.iam.gserviceaccount.com" \
  --role="roles/secretmanager.secretAccessor"

gcloud secrets add-iam-policy-binding SLACK_CLIENT_SECRET \
  --member="serviceAccount:slacker@${PROJECT}.iam.gserviceaccount.com" \
  --role="roles/secretmanager.secretAccessor"

# Grant access to registrar
gcloud secrets add-iam-policy-binding SLACK_CLIENT_ID \
  --member="serviceAccount:slacker-registrar@${PROJECT}.iam.gserviceaccount.com" \
  --role="roles/secretmanager.secretAccessor"

gcloud secrets add-iam-policy-binding SLACK_CLIENT_SECRET \
  --member="serviceAccount:slacker-registrar@${PROJECT}.iam.gserviceaccount.com" \
  --role="roles/secretmanager.secretAccessor"
```

---

## Cloud Datastore Configuration (Optional)

Slacker uses a **hybrid storage approach**:
- **JSON files** (default): Persistent state stored in local filesystem, survives restarts
- **Cloud Datastore** (optional): Adds cross-instance coordination for rolling deployments

### When to Use Datastore

**Use JSON-only (default):**
- Single-instance deployments
- Low traffic workspaces
- Development/testing

**Add Datastore:**
- Multi-instance Cloud Run deployments (autoscaling > 1)
- Rolling deployments with zero downtime
- High availability requirements

### Setup Steps

1. **Create a Datastore database:**
   ```bash
   # Create database in Datastore mode (not Firestore Native mode)
   gcloud firestore databases create \
     --database=slacker \
     --location=us-central \
     --type=datastore-mode
   ```

2. **Grant IAM permissions to service account:**
   ```bash
   PROJECT=your-gcp-project

   # Grant Datastore User role (read/write access)
   gcloud projects add-iam-policy-binding ${PROJECT} \
     --member="serviceAccount:slacker@${PROJECT}.iam.gserviceaccount.com" \
     --role="roles/datastore.user"
   ```

3. **Set environment variable:**
   ```bash
   # In Cloud Run deployment or .env file
   DATASTORE=slacker          # Use the database ID you created
   # GCP_PROJECT is auto-detected on Cloud Run
   ```

### How It Works

The hybrid system works automatically:
- **Writes**: Saved to both JSON and Datastore
- **Reads**: Try Datastore first, fall back to JSON if unavailable
- **Startup**: If Datastore fails to connect, gracefully degrades to JSON-only mode
- **No data loss**: JSON fallback ensures reliability even if Datastore is down

### Verification

After deployment, check logs to confirm Datastore is active:

```bash
gcloud logging read \
  "resource.type=cloud_run_revision AND resource.labels.service_name=slacker" \
  --limit 10 | grep -i datastore
```

You should see:
```
initializing Cloud Datastore for persistent state, project_id=..., database=slacker
```

If Datastore fails, you'll see:
```
using JSON files for state storage, reason=DATASTORE not set
```

---

## Deployment

### Single-Workspace Deployment (Self-Hosting)

For hosting the bot in your own Slack workspace:

```bash
# Deploy only the main server
make deploy-server
```

This will:
1. Build the Go binary with `ko`
2. Create a container image
3. Deploy to Cloud Run as `slacker`
4. Create service account `slacker@PROJECT.iam.gserviceaccount.com`

**Configure OAuth in Slack App:**
- Set redirect URL to: `https://slacker-HASH-uc.a.run.app/slack/oauth/callback`

**No registrar needed** - the main server handles OAuth for single-workspace installs.

### Multi-Workspace Deployment (SaaS / Marketplace)

For supporting multiple Slack workspaces:

```bash
# Deploy both services (server + registrar)
make deploy
```

This will deploy:
1. **Main server** (`slacker`) - handles GitHub webhooks and Slack events
2. **Registrar** (`slacker-registrar`) - handles OAuth for all workspaces

**Configure OAuth in Slack App:**
- Set redirect URL to: `https://slacker-registrar-HASH-uc.a.run.app/oauth/callback`

**Important:** For multi-workspace, all OAuth traffic goes through the registrar, not the main server.

### Manual Deployment

If you prefer manual control:

```bash
# Set your GCP project
export GCP_PROJECT=your-project-id

# Single-workspace: Deploy main server only
APP_NAME=slacker ./hacks/deploy.sh cmd/server

# Multi-workspace: Deploy both
APP_NAME=slacker ./hacks/deploy.sh cmd/server
APP_NAME=slacker-registrar ./hacks/deploy.sh cmd/registrar
```

---

## Post-Deployment Configuration

### Update Slack App URLs

After deployment, update your Slack app configuration based on your deployment type:

#### Single-Workspace Setup

1. Go to https://api.slack.com/apps → Your App
2. Under **"OAuth & Permissions"**, set **Redirect URLs** to:
   ```
   https://slacker-HASH-uc.a.run.app/slack/oauth/callback
   ```

3. Under **"Event Subscriptions"**, set **Request URL** to:
   ```
   https://slacker-HASH-uc.a.run.app/slack/events
   ```

4. Under **"Interactivity & Shortcuts"**, set **Request URL** to:
   ```
   https://slacker-HASH-uc.a.run.app/slack/interactive
   ```

All URLs point to the main server.

#### Multi-Workspace Setup

1. Go to https://api.slack.com/apps → Your App
2. Under **"OAuth & Permissions"**, set **Redirect URLs** to:
   ```
   https://slacker-registrar-HASH-uc.a.run.app/oauth/callback
   ```
   **Note:** OAuth goes to the registrar, not the main server!

3. Under **"Event Subscriptions"**, set **Request URL** to:
   ```
   https://slacker-HASH-uc.a.run.app/slack/events
   ```

4. Under **"Interactivity & Shortcuts"**, set **Request URL** to:
   ```
   https://slacker-HASH-uc.a.run.app/slack/interactive
   ```

OAuth uses the registrar URL; events and interactivity use the main server URL.

### Test OAuth Flow

#### Single-Workspace

1. Visit your main server install URL: `https://slacker-HASH-uc.a.run.app/slack/install`
2. Click "Add to Slack"
3. Authorize the app for your workspace
4. Verify you're redirected to success page
5. Check logs to confirm token was stored:
   ```bash
   gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=slacker" --limit 50
   ```

#### Multi-Workspace

1. Visit your registrar URL: `https://slacker-registrar-HASH-uc.a.run.app/install`
2. Click "Add to Slack"
3. Authorize the app (can be done from any workspace)
4. Verify you're redirected to success page
5. Check registrar logs to confirm token was stored in GSM:
   ```bash
   gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=slacker-registrar" --limit 50
   ```

### Verify Installation

Check that the workspace token was stored:

```bash
# List all slack-token-* secrets
gcloud secrets list --filter="name:slack-token-"
```

You should see a secret named `slack-token-{TEAM_ID}` for each installed workspace.

---

## Multi-Workspace Support

Multi-workspace support is only available when using the **registrar** service (not the main server OAuth).

### How It Works

1. Deploy the `slacker-registrar` service
2. Each workspace that installs the app gets a unique token
3. Tokens are stored in GSM with key pattern: `slack-token-{TEAM_ID}`
4. Workspace metadata stored as: `slack-metadata-{TEAM_ID}`
5. Main server fetches the correct token based on incoming webhook's team ID

### Architecture Benefits

The multi-workspace architecture works automatically:
- **Registrar** handles OAuth for unlimited workspaces
- **Main server** dynamically loads the correct workspace token
- No hardcoded workspace IDs or manual configuration
- Perfect for Slack Marketplace distribution

### Single-Workspace Limitation

If using the main server's built-in OAuth (without registrar), you can only support one workspace. The OAuth callback in `cmd/server/main.go` stores tokens in a way that doesn't scale to multiple workspaces.

---

## Monitoring

### Health Checks

Both services expose health endpoints:

```bash
# Main server
curl https://slacker-HASH-uc.a.run.app/health
curl https://slacker-HASH-uc.a.run.app/healthz

# Registrar
curl https://slacker-registrar-HASH-uc.a.run.app/health
```

### View Logs

```bash
# Main server logs
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=slacker" --limit 50

# Registrar logs
gcloud logging read "resource.type=cloud_run_revision AND resource.labels.service_name=slacker-registrar" --limit 50
```

### Common Issues

**403 Forbidden during OAuth:**
- Check that SLACK_CLIENT_ID and SLACK_CLIENT_SECRET are set correctly
- Verify service account has `secretAccessor` role for both secrets
- Check Cloud Run logs for "failed to fetch secret from GSM" warnings

**Tokens not saving:**
- Ensure service account has permission to create new secrets
- Grant `secretmanager.secretCreator` role:
  ```bash
  gcloud projects add-iam-policy-binding ${PROJECT} \
    --member="serviceAccount:slacker-registrar@${PROJECT}.iam.gserviceaccount.com" \
    --role="roles/secretmanager.secretCreator"
  ```

**Rate limiting errors:**
- OAuth endpoints have rate limit: 10 req/s, burst 20
- This is intentional to prevent abuse
- Normal installations won't hit this limit

---

## Security Best Practices

### Secrets Management

- **Never commit secrets to Git** - Use GSM or environment variables
- **Rotate secrets periodically** - Update Client Secret in Slack app settings
- **Limit service account permissions** - Only grant necessary GSM access
- **Use separate service accounts** - Don't share between server and registrar

### Network Security

- **Enable Cloud Run authentication** if running private registrar
- **Use HTTPS only** - Cloud Run enforces this by default
- **Validate webhook signatures** - Main server verifies all Slack requests
- **Rate limit OAuth endpoints** - Registrar has built-in rate limiting

### Monitoring & Alerts

Set up alerts for:
- 4xx/5xx error rates
- Failed secret access attempts
- Unusual OAuth traffic patterns
- Health check failures

---

## Troubleshooting

### OAuth callback fails with 404

**Problem:** Slack redirects to callback URL but gets 404

**Solution:**
- Verify registrar is deployed and running
- Check the route is `/oauth/callback` (no `/slack/` prefix)
- Ensure the URL in Slack app settings matches exactly

### "Missing required: SLACK_CLIENT_ID" error

**Problem:** Registrar fails to start

**Solution:**
- Set SLACK_CLIENT_ID as environment variable in Cloud Run, OR
- Store in GSM and grant service account access
- Check logs to see if GSM fetch is failing

### Tokens not persisting across workspace installs

**Problem:** Only one workspace token is saved

**Solution:**
- This shouldn't happen - each workspace gets unique token
- Check GSM for multiple `slack-token-*` secrets
- Verify `StoreWorkspace` is using team ID in key name

---

## Development

### Local Testing

Run the registrar locally:

```bash
# Set OAuth credentials
export SLACK_CLIENT_ID=your-client-id
export SLACK_CLIENT_SECRET=your-client-secret

# Build and run
make build-registrar
./bin/slack-registrar
```

Access at http://localhost:9120/install

**Note:** Slack OAuth callback requires HTTPS, so you'll need ngrok or similar for local testing:

```bash
ngrok http 9120
# Update Slack app redirect URL to: https://YOUR-NGROK-URL.ngrok.io/oauth/callback
```

### Building

```bash
# Build all binaries
make build

# Build server only
make build-server

# Build registrar only
make build-registrar
```

### Testing

```bash
# Run all tests
make test

# Run with coverage
go test -v -race -cover ./...
```

---

## Reference

### Makefile Targets

- `make build` - Build both binaries
- `make build-server` - Build main server
- `make build-registrar` - Build registrar
- `make test` - Run tests with race detection
- `make lint` - Run all linters
- `make fmt` - Format code
- `make deploy` - Deploy both server and registrar (multi-workspace)
- `make deploy-server` - Deploy main server only (single-workspace)
- `make deploy-registrar` - Deploy registrar only
- `make clean` - Remove build artifacts

### Cloud Run Services

| Service | Binary | Purpose | Port |
|---------|--------|---------|------|
| `slacker` | cmd/server | Main bot server | 9090 |
| `slacker-registrar` | cmd/registrar | OAuth handler | 9120 |

### Environment Variable Precedence

Both services check for secrets in this order:

1. Environment variable (e.g., `SLACK_CLIENT_ID`)
2. Google Secret Manager (e.g., secret named `SLACK_CLIENT_ID`)
3. Fail if not found

This allows flexibility in how you provide credentials.

---

## Need Help?

- **Documentation:** [github.com/codeGROOVE-dev/slacker](https://github.com/codeGROOVE-dev/slacker)
- **Issues:** [GitHub Issues](https://github.com/codeGROOVE-dev/slacker/issues)
- **Architecture:** See [ARCHITECTURE.md](ARCHITECTURE.md)
- **User Setup:** See [SETUP.md](SETUP.md)
