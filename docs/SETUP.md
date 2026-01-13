# Getting Started with reviewGOOSE:Slack

reviewGOOSE:Slack is the Slack integration for [reviewGOOSE](https://codegroove.dev/reviewgoose/) — it keeps your team informed about GitHub pull requests. It creates threads for PRs, tracks their status with emojis, and sends smart notifications so reviewers know when it's their turn.

## Quick Start

1. [Install the Slack app](#installing-the-slack-app)
2. [Configure your repositories](#configuring-your-repositories)
3. [Understand the status emojis](#understanding-pr-status-emojis)
4. [Customize notifications](#customizing-notifications)

---

## Installing the Slack App

<a href="https://slack.com/oauth/v2/authorize?client_id=9426269265270.9443955134789&scope=channels:history,channels:read,chat:write,chat:write.public,commands,im:write,reactions:write,team:read,users:read,users:read.email,groups:read,groups:history&user_scope="><img alt="Add to Slack" height="40" width="139" src="https://platform.slack-edge.com/img/add_to_slack.png" srcSet="https://platform.slack-edge.com/img/add_to_slack.png 1x, https://platform.slack-edge.com/img/add_to_slack@2x.png 2x" /></a>

### What You'll Authorize

When you click "Add to Slack", you'll be asked to authorize these permissions:

- **Post messages** (`chat:write`) - Create PR threads and send notifications
- **Read channel info** (`channels:read`, `groups:read`) - Find channels to post to
- **Read messages** (`channels:history`, `groups:history`) - Detect if users were already notified
- **Send DMs** (`im:write`) - Notify reviewers directly
- **Add reactions** (`reactions:write`) - Update PR status emojis
- **View workspace** (`team:read`) - Validate workspace configuration
- **View users** (`users:read`) - Find Slack users matching GitHub usernames

All permissions are used exclusively for PR notifications and status tracking. The bot is hosted as a SaaS service - you don't need to run any servers.

### After Installation

Once installed, the bot needs to know which GitHub repositories to track. Continue to the next section to configure your repos.

---

## Configuring Your Repositories

reviewGOOSE:Slack reads its configuration from a special GitHub repository in your organization. This lets you version-control your notification settings centrally.

### Step 1: Create the .codeGROOVE Repository

In your GitHub organization, create a repository named `.codeGROOVE` (note the leading dot). This repository can contain multiple configuration files for various codeGROOVE tools.

### Step 2: Create the Slack Configuration File

In the `.codeGROOVE` repository, create a file named `slack.yaml`

### Step 3: Basic Configuration

Here's a minimal configuration to get started:

```yaml
global:
    team_id: T09CJ7X7T7Y
    email_domain: mycompany.com

channels:
    engineering:
        repos:
            - "*"
```

**What this does:**
- Validates that this config is for your Slack workspace (by team ID)
- Maps GitHub users to Slack users using your email domain
- Posts all PRs to the `#engineering` channel

**Finding your Team ID:**
1. In Slack, click your workspace name → Settings & administration → Workspace settings
2. Look at the URL - it contains your team ID (e.g., `T09CJ7X7T7Y`)
3. Or use the Slack API tester: https://api.slack.com/methods/auth.test/test

### Optional: Channel Auto-Discovery

By default, **repositories automatically map to channels with the same name**:

- Repository `backend` → Channel `#backend`
- Repository `mobile-app` → Channel `#mobile-app`
- Repository `docs` → Channel `#docs`

You don't need to configure anything for this to work! The bot automatically discovers channels.

**To override auto-discovery**, explicitly add repos to a channel:

```yaml
global:
    team_id: T09CJ7X7T7Y
    email_domain: mycompany.com

channels:
    # Send backend & api PRs to #platform instead of their own channels
    platform:
        repos:
            - backend
            - api

    # Mute auto-discovered #mobile-app channel
    mobile-app:
        mute: true

    # Catch-all for repos without specific channels
    general-engineering:
        repos:
            - "*"
```

### Step 4: Commit and Push

```bash
git add slack.yaml
git commit -m "Configure reviewGOOSE:Slack"
git push
```

The bot will automatically detect the configuration in your `.codeGROOVE` repository and start posting PR updates for all PRs in your organization!

---

## Understanding PR Status Emojis

The bot updates the emoji at the start of each PR message to reflect its current state:

| Emoji | Status | Meaning | Blocked On |
|-------|--------|---------|------------|
| :test_tube: | **Tests Running** | CI/CD checks are pending or in progress | GitHub Actions |
| :cockroach: | **Tests Broken** | One or more checks have failed | Author |
| :hourglass: | **Awaiting Review** | Ready for review, waiting on reviewers | Reviewers |
| :carpentry_saw: | **Needs Work** | Approved but requested changes | Author |
| :white_check_mark: | **Approved** | Reviewed and approved, ready to merge | Author |
| :rocket: | **Merged** | PR has been merged | None |
| :x: | **Closed** | PR was closed without merging | None |

### Example PR Thread

```
:hourglass: Add user authentication • backend#42 by @alice
```

When tests pass and the PR gets approved, the message updates to:

```
:white_check_mark: Add user authentication • backend#42 by @alice
```

**Pro tip:** The PR URL includes a debug state parameter (e.g., `?st=awaiting_review`) that shows what state the bot detected when it last updated.

---

## Customizing Notifications

### Smart Direct Messages

By default, the bot uses smart logic to avoid spamming DMs:

**If a user is tagged in a channel they're in:**
- Bot waits 65 minutes before sending a DM
- Gives them time to see the channel notification first
- Prevents duplicate notifications

**If a user is NOT in the channel where they're tagged:**
- Bot sends an immediate DM
- Ensures they don't miss the notification

**One DM per user per PR:**
- Even if the PR is posted to multiple channels
- Each user only gets notified once

### Configuring DM Delay

You can customize how long the bot waits before sending DMs:

```yaml
global:
    team_id: T09CJ7X7T7Y
    email_domain: mycompany.com
    reminder_dm_delay: 30  # Wait 30 minutes instead of default 65
```

Set to `0` to disable delayed reminders entirely (DMs sent immediately):

```yaml
global:
    team_id: T09CJ7X7T7Y
    email_domain: mycompany.com
    reminder_dm_delay: 0  # No delays, always DM immediately
```

### Channel-Specific Delays

Override the delay for specific channels:

```yaml
global:
    team_id: T09CJ7X7T7Y
    email_domain: mycompany.com
    reminder_dm_delay: 65  # Default for most channels

channels:
    urgent-fixes:
        repos:
            - backend
        reminder_dm_delay: 5  # Only wait 5 minutes for urgent channel
```

### Daily Reminders

Enable daily reminders sent between 8-9am in each user's local timezone:

```yaml
global:
    team_id: T09CJ7X7T7Y
    email_domain: mycompany.com
    daily_reminders: true  # Default: false
```

Daily reminders only trigger if:
- More than 8 hours have passed since last notification
- The user has PRs waiting on them
- It's between 8-9am in their timezone

### Custom Message Prefix

Add a custom emoji prefix to all PR messages:

```yaml
global:
    team_id: T09CJ7X7T7Y
    email_domain: mycompany.com
    prefix: ":postal_horn:"
```

Messages will appear as:

```
:postal_horn: :hourglass: Add user authentication • backend#42 by @alice
```

---

## Advanced Configuration

### Multiple Channels per Repo

Send PRs from one repo to multiple channels:

```yaml
channels:
    backend-team:
        repos:
            - backend

    all-prs:
        repos:
            - backend
            - frontend
            - mobile
```

Now `backend` PRs appear in both `#backend-team` and `#all-prs`.

### Wildcard Mapping

Use `"*"` to catch all repositories:

```yaml
channels:
    all-engineering:
        repos:
            - "*"  # Catch all repos

    security:
        repos:
            - auth-service  # Also send auth-service here
```

Every PR goes to `#all-engineering`, but `auth-service` also goes to `#security`.

### Muting Auto-Discovered Channels

If a channel auto-discovery is matching a channel you don't want to post to:

```yaml
channels:
    # Don't post to auto-discovered #staging channel
    staging:
        mute: true

    # Send staging repo PRs to #platform instead
    platform:
        repos:
            - staging
```

### Complete Configuration Example

```yaml
global:
    team_id: T09CJ7X7T7Y
    email_domain: acme-corp.com
    prefix: ":bell:"
    reminder_dm_delay: 45
    daily_reminders: true

channels:
    # High-priority repos with immediate notifications
    critical:
        repos:
            - auth-service
            - payment-api
        reminder_dm_delay: 0  # Immediate DMs

    # Backend team repos
    backend:
        repos:
            - user-service
            - data-pipeline
            - analytics

    # Frontend repos (auto-discovery)
    # web-app → #web-app
    # mobile-app → #mobile-app
    # No explicit config needed!

    # Mute auto-discovered internal-tools channel
    internal-tools:
        mute: true

    # Catch-all for repos without specific channels
    general:
        repos:
            - "*"
        reminder_dm_delay: 120  # Wait 2 hours for general channel
```

---

## Viewing Your Dashboard

reviewGOOSE:Slack provides two ways to view your PRs:

### 1. Slack App Home

1. Click "reviewGOOSE" in your Slack sidebar
2. Select the "Home" tab
3. View incoming PRs (waiting on you) and outgoing PRs (waiting on others)

### 2. Web Dashboard

Visit [reviewgoose.dev](https://reviewgoose.dev/) for a comprehensive web view.

---

## Troubleshooting

### PRs aren't appearing in Slack

**Check your configuration:**
1. Do you have a `.codeGROOVE` repository in your GitHub organization?
2. Is `slack.yaml` committed to the `.codeGROOVE` repo's main branch?
3. Does the `team_id` field match your Slack workspace's team ID exactly?
4. Is the `email_domain` set correctly for mapping GitHub users to Slack users?
5. Are you posting to a channel that exists?

**Check channel permissions:**
- The bot needs permission to post in the channel
- Try inviting the bot: `/invite @goose`

### Not receiving DMs

**Check notification settings:**
1. Verify `reminder_dm_delay` isn't set too high
2. Check if you were already notified in a channel you're in
3. Ensure Slack notifications are enabled for DMs

**User mapping:**
- The bot maps GitHub usernames to Slack usernames
- If they don't match, the bot can't find you
- Contact your admin to check the user mapping

### Wrong channel

**If auto-discovery isn't working:**
- Channel name must match repository name exactly
- Use explicit mapping in config if names differ:

```yaml
channels:
    my-channel:
        repos:
            - my-differently-named-repo
```

### Configuration not updating

**After changing `slack.yaml`:**
1. Commit and push your changes
2. Wait up to 5 minutes for the bot to reload
3. Test with a new PR or comment

Still having issues? Check the bot's logs or contact support.

---

## Best Practices

### Start Simple

Begin with a minimal config and add complexity as needed:

```yaml
global:
    team_id: T09CJ7X7T7Y
    email_domain: mycompany.com

channels:
    engineering:
        repos:
            - "*"
```

### Use Auto-Discovery

Let the bot automatically map repos to channels - only override when necessary.

### Test Before Rolling Out

1. Create a test repository
2. Add configuration pointing to a test channel
3. Open a PR and verify notifications work
4. Roll out to production repos

### Version Control Everything

Store `slack.yaml` in Git alongside your code:
- Track configuration changes
- Review changes in PRs
- Roll back if needed

### Coordinate with Your Team

Before enabling:
- Announce the bot to your team
- Explain the emoji meanings
- Share this guide
- Invite feedback on notification timing

---

## Getting Help

- **Documentation:** [github.com/codeGROOVE-dev/slacker](https://github.com/codeGROOVE-dev/slacker)
- **Issues:** [GitHub Issues](https://github.com/codeGROOVE-dev/slacker/issues)
- **Support:** Contact your reviewGOOSE administrator

---

## What's Next?

reviewGOOSE:Slack is part of the codeGROOVE ecosystem of developer tools. Check out:

- **[reviewGOOSE:Desktop](https://github.com/codeGROOVE-dev/goose)** - Desktop app with honk notifications
- **[reviewGOOSE Dashboard](https://reviewgoose.dev/)** - Web-based PR dashboard
- **[codeGROOVE.dev](https://codegroove.dev/)** - Developer acceleration tools

Happy reviewing!
