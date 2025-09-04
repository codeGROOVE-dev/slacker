# Slack App Setup Guide

## Quick Fix for "missing_scope" Error

If you're seeing this error:
```
WARN failed to get workspace info, config validation disabled error="failed to get team info: missing_scope"
```

**You need to add the `team:read` scope to your Slack app.**

## Step-by-Step Setup

### 1. Go to Slack App Management
1. Visit https://api.slack.com/apps
2. Select your app or create a new one
3. Go to "OAuth & Permissions" in the sidebar

### 2. Add Required Bot Token Scopes
Under "Scopes" → "Bot Token Scopes", add these scopes:

**Essential for basic functionality:**
- `chat:write` - Send messages as the bot
- `chat:write.public` - Send messages to channels the bot isn't a member of
- `reactions:write` - Add emoji reactions to messages
- `team:read` - **Required for workspace validation**

**For user interaction:**
- `commands` - Handle slash commands
- `im:write` - Send direct messages
- `users:read` - Check user presence/activity

**For channel management:**
- `channels:read` - View channel information
- `app_home` - Enable app home tab

### 3. Reinstall App
After adding scopes, you need to reinstall the app to your workspace:
1. Click "Install App to Workspace" button
2. Authorize the new permissions
3. Copy the new `Bot User OAuth Token` (starts with `xoxb-`)
4. Update your `SLACK_BOT_TOKEN` environment variable

### 4. Enable Event Subscriptions (Optional)
If using real-time features:
1. Go to "Event Subscriptions"
2. Enable events
3. Add your bot's endpoint URL: `https://yourdomain.com/slack/events`
4. Subscribe to bot events:
   - `app_home_opened`
   - `app_mention`
   - `message.channels`

### 5. Enable Slash Commands (Optional)
1. Go to "Slash Commands"
2. Create commands like `/r2r dashboard`
3. Set request URL: `https://yourdomain.com/slack/slash`

## Testing the Fix

After adding `team:read` scope and reinstalling, you should see:
```
INFO set workspace name for config validation workspace=myworkspace.slack.com
```

Instead of the missing_scope error.

## Minimal Required Scopes

For basic functionality, you need at minimum:
- `chat:write` - Send messages
- `team:read` - Workspace validation  
- `reactions:write` - PR state emojis

Everything else is optional depending on features used.