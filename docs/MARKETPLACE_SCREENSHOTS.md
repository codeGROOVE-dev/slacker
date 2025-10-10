# Ready to Review - Slack Marketplace Screenshot Guide

For the Slack App Directory submission, you need **3-5 high-quality screenshots** that showcase your app's functionality.

---

## Screenshot Requirements

- **Resolution:** 1600x900px minimum (16:9 ratio preferred)
- **Format:** PNG or JPEG
- **Quality:** High-resolution, no compression artifacts
- **Branding:** Include Slack and GitHub UI elements naturally
- **Annotations:** Optional callout boxes or arrows for key features
- **Consistency:** Use the same Slack theme across all screenshots

---

## Screenshot 1: PR Channel Notification (HERO IMAGE)

**Filename:** `01-pr-notification-channel.png`

**What to capture:**
- Slack channel view (#engineering or similar)
- Thread with PR notification showing:
  - Emoji prefix (⏳ hourglass = awaiting review)
  - PR title: "Add user authentication"
  - PR info: "backend#142 by @username"
  - Direct link to GitHub PR
  - Timestamp
- Follow-up message in thread tagging reviewers
- Clean, professional channel with 2-3 other messages for context

**Callouts to add:**
- Arrow pointing to emoji: "Real-time status updates"
- Arrow pointing to link: "Direct link to PR"
- Arrow pointing to thread: "Organized discussion"

**Why this matters:** This is your hero image - shows the core functionality immediately.

---

## Screenshot 2: PR State Progression

**Filename:** `02-pr-state-emojis.png`

**What to capture:**
- Slack channel showing multiple PR threads with different state emojis:
  - 🧪 "Run integration tests" (tests running)
  - 🪳 "Fix authentication bug" (tests broken)
  - ⏳ "Add user dashboard" (awaiting review)
  - 🪚 "Update dependencies" (approved but needs work)
  - ✅ "Refactor API client" (approved)
  - 🚀 "Add logging middleware" (merged)
- Each thread should be visible but not expanded
- Shows at-a-glance PR pipeline status

**Callouts to add:**
- Legend box showing emoji meanings:
  - 🧪 Tests running
  - 🪳 Tests broken
  - ⏳ Awaiting review
  - 🪚 Approved, needs work
  - ✅ Approved
  - 🚀 Merged

**Why this matters:** Shows how teams get instant visibility into PR status.

---

## Screenshot 3: Smart DM Notification

**Filename:** `03-dm-notification.png`

**What to capture:**
- Direct message view from Ready to Review bot
- DM showing PR notification:
  - Same format as channel message
  - ⏳ emoji prefix
  - PR title and details
  - Link to PR
  - Friendly tone
- Message timestamp showing it arrived at intelligent time
- Maybe 2-3 DM notifications in the conversation showing it's not spammy

**Callouts to add:**
- "Smart timing - won't spam you"
- "Same format as channel messages"
- "Direct link to take action"

**Why this matters:** Shows notification system without being overwhelming.

---

## Screenshot 4: App Home Dashboard

**Filename:** `04-app-home-dashboard.png`

**What to capture:**
- Slack App Home tab for Ready to Review
- Clean Block Kit UI showing:
  - **Blocked on You** section:
    - 2-3 PRs with clear action items
    - Red or orange indicators
  - **Incoming PRs** section:
    - PRs awaiting your review
    - ⏳ emoji indicators
  - **Outgoing PRs** section:
    - Your PRs awaiting others
    - Various state emojis
- Each section should have 2-3 items for demonstration
- Include clickable buttons/links

**Callouts to add:**
- "Personalized dashboard"
- "See what needs your attention"
- "Track your own PRs"

**Why this matters:** Shows the dashboard feature and personalized experience.

---

## Screenshot 5: Configuration Example

**Filename:** `05-configuration-yaml.png`

**What to capture:**
- GitHub repository view showing `.codeGROOVE/slack.yaml` file
- Clean, well-formatted YAML:
```yaml
global:
    slack: acme-corp.slack.com
    reminder_dm_delay: 60
    daily_reminders: true

channels:
    # Main engineering channel
    engineering:
        repos:
            - backend
            - frontend
            - mobile

    # Data team PRs
    data-science:
        repos:
            - ml-pipeline
            - data-warehouse

    # Catch-all for other repos
    general-dev:
        repos:
            - "*"
```
- GitHub UI showing file location in repo
- Maybe include a PR showing config change

**Callouts to add:**
- "Configuration lives in your repo"
- "Auto-discovery or explicit mapping"
- "No web UI to manage"

**Why this matters:** Shows how easy configuration is and that it's GitOps-friendly.

---

## Optional Screenshot 6: Multi-Workspace Support

**Filename:** `06-multi-workspace.png`

**What to capture:**
- Side-by-side view of two different Slack workspaces
- Same GitHub org posting to different workspaces
- Configuration files showing different workspace settings
- Or: OAuth installation page showing "Install to another workspace"

**Callouts to add:**
- "Support multiple teams"
- "One GitHub org, many Slack workspaces"
- "Perfect for agencies or enterprises"

**Why this matters:** Shows enterprise-ready multi-tenancy.

---

## Alternative Screenshot Ideas

### Installation Success Page
- Shows OAuth completion
- Links to privacy policy and terms
- Professional, clean design

### Daily Reminder Example
- Morning DM showing multiple pending PRs
- Timestamp showing 8:30 AM
- Non-intrusive format

### Channel Auto-Discovery
- Diagram showing:
  - `backend` repo → `#backend` channel
  - `frontend` repo → `#frontend` channel
  - Automatic mapping

---

## Photography Tips

### Preparation
1. **Clean up your Slack workspace:**
   - Use professional channel names
   - Remove any sensitive/internal data
   - Use realistic but sanitized PR titles
   - Professional user avatars

2. **Create test data:**
   - Multiple PRs in various states
   - Realistic PR titles and descriptions
   - Multiple reviewers and authors
   - Timestamps that make sense

3. **Consistent theme:**
   - Use same Slack theme (light or dark) across all screenshots
   - Slack's default theme is safest for marketplace

4. **Professional context:**
   - Other channels visible in sidebar (but blurred if needed)
   - Realistic number of unread messages
   - Professional workspace name

### Capturing Screenshots

**Tools:**
- macOS: Cmd+Shift+4 for precise selection
- Windows: Snipping Tool or Windows+Shift+S
- Chrome: Full page screenshot extension for tall pages

**Editing:**
- Use Figma, Sketch, or Photoshop for callouts
- Slack brand colors: #611f69 (purple), #ECB22E (yellow), #2EB67D (green)
- Use drop shadows on callout boxes for depth
- Keep annotations minimal and clear

**Retina displays:**
- Capture at 2x resolution if possible
- Downscale to required size for crisp text

---

## Marketplace Listing Copy

### Short Description (80 characters max)
"Get notified when PRs are ready for review with smart Slack notifications"

### Long Description (500-1000 words)

**Streamline Your PR Review Workflow**

Ready to Review is a Slack bot that keeps your development team synchronized with GitHub pull request activity. No more missed reviews, no more notification overload - just the right information at the right time.

**Key Features:**

🔔 **Smart Notifications**
- Channel threads for team visibility
- Intelligent DM timing to prevent spam
- Daily reminders for pending reviews

📊 **Real-Time Status Updates**
- Emoji prefixes show PR state at a glance
- Automatic updates as PRs progress
- See test status, review status, and merge readiness

🏠 **Personal Dashboard**
- Native Slack App Home integration
- See PRs blocked on you
- Track your own PR status
- Review incoming requests

⚙️ **GitOps Configuration**
- Configuration lives in your repository
- Channel auto-discovery by repo name
- Override mappings as needed
- No web UI to manage

🏢 **Enterprise Ready**
- Multi-workspace support
- Multi-organization support
- Secure OAuth installation
- Open source (GPL v3)

**How It Works:**

1. Install Ready to Review to your Slack workspace
2. Add a configuration file to your GitHub repos
3. Receive notifications when PRs need attention
4. Never miss a review again

**Perfect For:**
- Engineering teams practicing continuous integration
- Organizations with multiple GitHub repositories
- Teams wanting better PR visibility without leaving Slack
- Companies that value open source transparency

**Privacy & Security:**
Ready to Review is committed to your privacy. We only collect the minimum data necessary to function: GitHub usernames, PR metadata, and your Slack workspace ID. We never sell or share your data. Read our full privacy policy at [link].

**Open Source:**
This app is free and open source under GPL v3. View the code, contribute, or self-host at github.com/codeGROOVE-dev/slacker

**Support:**
Questions? Contact us at root@codeGROOVE.dev or visit our documentation.

---

## Submission Checklist

- [ ] 3-5 screenshots captured (1600x900px minimum)
- [ ] All screenshots show consistent Slack theme
- [ ] Callouts added to highlight key features
- [ ] No sensitive/internal data visible
- [ ] Professional, clean workspace appearance
- [ ] Screenshots saved in PNG format
- [ ] Video demo recorded (3-5 minutes)
- [ ] Short description written (80 chars)
- [ ] Long description written (500-1000 words)
- [ ] Privacy policy URL configured in Slack app
- [ ] Terms of service URL configured in Slack app
- [ ] Support email configured (root@codeGROOVE.dev)
- [ ] App icon uploaded (512x512px)
- [ ] OAuth redirect URLs configured
- [ ] App installed on 5+ active workspaces
- [ ] Test account credentials prepared for Slack review team
