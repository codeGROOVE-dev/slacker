# Ready to Review - Slack Marketplace Demo Video Script

**Duration:** 3-5 minutes
**Style:** Professional but approachable, screen recording with voiceover

---

## Scene 1: Introduction (15 seconds)
**Visual:** Landing page at installation URL
**Voiceover:**
"Ready to Review is a Slack bot that helps development teams stay on top of pull request reviews. Let me show you how it works."

---

## Scene 2: Installation Flow (30 seconds)
**Visual:**
- Click "Add to Slack" button
- Slack OAuth permission screen
- Select workspace
- Approve permissions
- Success page

**Voiceover:**
"Installation is simple. Click 'Add to Slack', select your workspace, and approve the necessary permissions. The app needs access to post messages, read channel information, and send direct messages to notify team members."

**On-screen text:** Show scopes being requested

---

## Scene 3: GitHub Configuration (45 seconds)
**Visual:**
- GitHub repository settings
- Create `.codeGROOVE/slack.yaml` file
- Show configuration example:
```yaml
global:
    slack: mycompany.slack.com
    reminder_dm_delay: 60
    daily_reminders: true

channels:
    engineering:
        repos:
            - backend
            - frontend
```

**Voiceover:**
"To configure Ready to Review, add a configuration file to your GitHub repository. The config lets you specify which Slack workspace to use, notification timing, and channel mappings. By default, repositories automatically map to channels with the same name, but you can override this."

**On-screen text:** "Configuration lives in your repo - no web UI needed"

---

## Scene 4: PR Creation and Channel Notification (60 seconds)
**Visual:**
- Create a new pull request on GitHub
- Show webhook event firing (brief)
- Switch to Slack
- Show new thread appearing in #engineering channel
- Highlight emoji prefix: 🧪 (tests running)
- Show PR details: title, PR number, author
- Show assignee notification in thread

**Voiceover:**
"When you create a pull request, Ready to Review posts a thread to the configured Slack channel. The emoji prefix shows the PR state at a glance - test tube means tests are running. The message includes the PR title, number, author, and a direct link. When reviewers are assigned, they're automatically tagged in the thread."

**On-screen text:** Point out:
- Emoji = PR state
- Link goes directly to PR
- Thread keeps discussion organized

---

## Scene 5: PR State Updates (45 seconds)
**Visual:**
- Show PR progression through states:
  - 🧪 Tests running
  - 🪳 Tests broken (blocked on author)
  - ⏳ Waiting on review
  - 🪚 Approved but needs work
  - ✅ Reviewed & approved
  - 🚀 Merged
- Show Slack message emoji updating in real-time

**Voiceover:**
"As your PR progresses, the emoji prefix automatically updates. Tests broken? You get a bug emoji. Ready for review? Hourglass. Approved and ready to merge? Green checkmark. This gives your team instant visibility into PR status without leaving Slack."

**On-screen text:** "State changes update automatically"

---

## Scene 6: Smart DM Notifications (60 seconds)
**Visual:**
- Show reviewer receiving DM from bot
- Highlight DM format matches channel message
- Show settings: `reminder_dm_delay: 60`
- Split screen: User in channel (delayed DM) vs not in channel (immediate DM)

**Voiceover:**
"Ready to Review includes smart direct message notifications. If a reviewer is already in the channel where they were tagged, the bot waits an hour before sending a DM - this prevents notification spam. But if they're not in that channel, they get an immediate DM so nothing falls through the cracks. The delay is fully configurable."

**On-screen text:**
- "In channel = delayed DM"
- "Not in channel = immediate DM"
- "Prevents notification overload"

---

## Scene 7: Daily Reminders (30 seconds)
**Visual:**
- Show DM arriving at 8:30am
- Highlight multiple PRs waiting for review
- Show configuration: `daily_reminders: true`

**Voiceover:**
"If you still have pending reviews after 8 hours, Ready to Review sends a daily reminder between 8 and 9 AM in your local timezone. This ensures important PRs don't get forgotten."

---

## Scene 8: App Home Dashboard (45 seconds)
**Visual:**
- Click on Ready to Review in Slack sidebar
- Show Home tab
- Highlight sections:
  - PRs blocked on you
  - Incoming PRs (awaiting your review)
  - Outgoing PRs (your PRs awaiting others)
- Show clean Block Kit UI

**Voiceover:**
"The app home dashboard gives you a personalized view of all your pull requests. See what's blocked on you, what needs your review, and the status of your own PRs - all in one place."

**On-screen text:** "Also available at dash.ready-to-review.dev"

---

## Scene 9: Multi-Workspace Support (30 seconds)
**Visual:**
- Show multiple workspaces configured
- Show same GitHub org posting to different Slack workspaces
- Highlight configuration file controls which workspace

**Voiceover:**
"Ready to Review supports multiple Slack workspaces. Each repository configuration specifies which workspace to notify, making it perfect for organizations with multiple teams or clients."

---

## Scene 10: Uninstallation (20 seconds)
**Visual:**
- Go to Slack app settings
- Click "Remove App"
- Confirmation dialog
- App removed successfully

**Voiceover:**
"If you ever need to uninstall, simply go to your Slack app settings and click remove. Your data is immediately deleted from our systems."

**On-screen text:** "Data deletion is immediate"

---

## Scene 11: Closing (15 seconds)
**Visual:**
- Show GitHub repo link: github.com/codeGROOVE-dev/slacker
- Show GPL v3 badge
- Show "Add to Slack" button

**Voiceover:**
"Ready to Review is free and open source under GPL v3. Install it today and streamline your PR review workflow."

**On-screen text:**
- "Free & Open Source"
- "GPL v3 License"
- "github.com/codeGROOVE-dev/slacker"

---

## Technical Notes for Recording

**Screen Resolution:** 1920x1080 minimum
**Frame Rate:** 30fps minimum
**Format:** MP4 (H.264)
**Audio:** Clear voiceover, no background music
**Callouts:** Use Slack-style colors for highlighting
**Pacing:** Pause 2-3 seconds on each major feature
**Branding:** Keep Slack and GitHub branding visible but don't overemphasize

**Tools:**
- Screen recording: OBS Studio, ScreenFlow, or Camtasia
- Callouts: Keynote animations or After Effects
- Voiceover: Audacity or Adobe Audition

**Testing Setup:**
- Use a test Slack workspace (not production)
- Use a test GitHub organization
- Pre-create PRs in various states
- Have multiple user accounts ready for demo
