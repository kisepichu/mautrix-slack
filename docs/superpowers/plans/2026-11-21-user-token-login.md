# User-token app login Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `login app` accept a Slack user token (`xoxp-`) so a person can puppet their own account through an
internal Slack app. Add a catch-up for app logins so that top-level messages and recent thread replies missed while
the bridge was disconnected get bridged.

**Architecture:** User tokens reuse upstream's existing app mode (`IsRealUser == false`: Socket Mode, documented APIs).
The only login change is the token pattern. The catch-up lives in a new file `pkg/connector/catchup.go`:
- a pure planning function and a reconnect-gap tracker, both unit tested;
- a runner that reads `conversations.history`, queues upstream's chat resync (forward backfill) for top-level
  messages, and dispatches missed thread replies through `HandleSlackEvent`.

It is triggered at startup and after a Socket Mode reconnect that followed more than 1 minute of disconnection.

**Tech Stack:** Go (toolchain per `go.mod`), mautrix-go bridgev2 v0.33.0, `github.com/slack-go/slack` (replaced by
`github.com/beeper/slackgo`), testify.

**Spec:** `docs/superpowers/specs/2026-11-21-user-token-login-design.md`

## Global Constraints

- Branch `user-token`, based on upstream `v0.2611.0`. Remote `origin` = `kisepichu/mautrix-slack`, `upstream` = `mautrix/slack`.
- Never write information that identifies a real workspace (workspace name, team or enterprise ID, channels, people)
  in code, docs, commit messages, issues or PRs.
- When testing, write to Slack (send, edit, delete, react) only in a personal test workspace, or only in your own DM in
  any other workspace.
- The login field ID stays `bot_token`.
- Thread catch-up window: 7 days (`threadCatchUpWindow`). Reconnect gap that triggers a catch-up: 1 minute
  (`socketModeCatchUpGap`).
- No retry loop of our own for 429: the Slack client already retries via `slack.OptionRetry(slackMaxRetries)`.
- Compare Slack timestamps numerically (`slackid.ParseSlackTimestamp`), never as strings.
- Format imports like the pre-commit hook: `goimports -local go.mau.fi/mautrix-slack`. No `Msgf` with zerolog.
- Commit with `git commit --no-gpg-sign`. Commit messages follow upstream style (`<area>: <change>`).

## File Structure

| File | Responsibility |
|---|---|
| `AGENTS.md` (create) | Rules for agents working in this fork |
| `app-manifest-user.yaml` (create) | Slack app manifest with user token scopes and Socket Mode |
| `README.md` (modify) | Short "User token login" section |
| `pkg/connector/login-app.go` (modify) | Accept `xoxp-` in `login app` |
| `pkg/connector/login-cookie.go` (modify) | Login flow description mentions user tokens |
| `pkg/connector/catchup.go` (create) | Catch-up planning, reconnect-gap tracking, and the runner |
| `pkg/connector/catchup_test.go` (create) | Tests for `planCatchUp` and `socketModeGapTracker` |
| `pkg/connector/client.go` (modify) | `SlackClient` fields; startup trigger in `connect` |
| `pkg/connector/handleslack.go` (modify) | Reconnect trigger in `HandleSocketModeEvent` |

---

### Task 1: User-token login, manifest, rules and README

**Files:**
- Create: `AGENTS.md`
- Create: `app-manifest-user.yaml`
- Modify: `README.md` (append a section after "Features & Roadmap")
- Modify: `pkg/connector/login-app.go:44-48`
- Modify: `pkg/connector/login-cookie.go:45-47`

**Interfaces:**
- Consumes: nothing.
- Produces: `login app` accepts `^xox[bp]-.+$` in field `bot_token`.

- [ ] **Step 1: Create `AGENTS.md`**

```markdown
# Agent rules for this fork

This is a fork of mautrix-slack that adds user-token app login and a catch-up for app logins.
The design is in `docs/superpowers/specs/2026-11-21-user-token-login-design.md`.

- Never write information that identifies a real Slack workspace (workspace name, team or enterprise ID, channel
  names or IDs, people's names or IDs) in code, docs, commit messages, issues or pull requests. Use made-up values in
  tests and examples.
- When testing against Slack, write (send, edit, delete, react) only in a personal test workspace, or only in your own
  DM in any other workspace. Reading is not restricted.
- Keep changes self-contained so they can be proposed upstream. Do not reformat or refactor upstream code that the
  change does not need.
- Format imports with `goimports -local go.mau.fi/mautrix-slack`. Do not use `Msgf` with zerolog.
```

- [ ] **Step 2: Create `app-manifest-user.yaml`**

```yaml
display_information:
  name: Matrix bridge
  description: Bridges your Slack account to Matrix with mautrix-slack
oauth_config:
  scopes:
    user:
      - channels:history
      - channels:read
      - groups:history
      - groups:read
      - im:history
      - im:read
      - mpim:history
      - mpim:read
      - chat:write
      - reactions:read
      - reactions:write
      - files:read
      - files:write
      - users:read
      - emoji:read
      - team:read
settings:
  event_subscriptions:
    user_events:
      - message.channels
      - message.groups
      - message.im
      - message.mpim
      - reaction_added
      - reaction_removed
      - channel_rename
      - group_rename
      - member_joined_channel
      - member_left_channel
  interactivity:
    is_enabled: false
  org_deploy_enabled: false
  socket_mode_enabled: true
  token_rotation_enabled: false
```

- [ ] **Step 3: Append the README section**

Insert after the "Features & Roadmap" paragraph (before `## Discussion`):

```markdown
### User token login (this fork)
To puppet your own account without browser cookies, use an internal Slack app with a user token:

1. Create an app at <https://api.slack.com/apps> from [app-manifest-user.yaml](app-manifest-user.yaml)
   and install it to your workspace.
2. Copy the **User OAuth Token** (`xoxp-`) from *OAuth & Permissions*.
3. Under *Basic Information → App-Level Tokens*, create a token with the `connections:write` scope (`xapp-`).
4. Send `login app` to the bridge bot and paste both tokens.

Messages you send through the bridge are posted as you. The API marks them with the app's `app_id`, and workspace
admins can see and remove the app. Read receipts and typing notifications are not sent with this login.
```

- [ ] **Step 4: Accept user tokens in `pkg/connector/login-app.go`**

Replace lines 44-48 (the `bot_token` field) with:

```go
				Type:        bridgev2.LoginInputFieldTypeToken,
				ID:          "bot_token",
				Name:        "Bot or user token",
				Description: "Slack bot token (starts with `xoxb-`) or user token (starts with `xoxp-`) for the workspace",
				Pattern:     "^xox[bp]-.+$",
```

- [ ] **Step 5: Update the flow description in `pkg/connector/login-cookie.go`**

Replace lines 45-47 with:

```go
		Name:        "Slack app",
		Description: "Log in with a Slack app's bot token, or its user token to puppet your own account",
		ID:          LoginFlowIDApp,
```

- [ ] **Step 6: Build, vet and test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: no output from build and vet; `ok` for every package with tests (no `FAIL`).

- [ ] **Step 7: Commit**

```bash
git add AGENTS.md app-manifest-user.yaml README.md pkg/connector/login-app.go pkg/connector/login-cookie.go
git commit --no-gpg-sign -m "login-app: accept user tokens and add a manifest for user-token apps"
```

---

### Task 2: Catch-up planning and reconnect-gap tracking

**Files:**
- Create: `pkg/connector/catchup.go`
- Create: `pkg/connector/catchup_test.go`

**Interfaces:**
- Consumes: `slackid.ParseSlackTimestamp(string) time.Time`.
- Produces (used by Task 3):
  - `const threadCatchUpWindow = 7 * 24 * time.Hour`
  - `const socketModeCatchUpGap = time.Minute`
  - `type threadCatchUp struct { ParentTS, OldestTS string }`
  - `func slackTimestampAfter(a, b string) bool`
  - `func planCatchUp(history []slack.Message, lastBridgedTS string, windowStart time.Time, lastThreadReply func(parentTS string) (string, bool)) (latestTS string, threads []threadCatchUp)`
  - `type socketModeGapTracker struct` with `func (t *socketModeGapTracker) lost(now time.Time)`,
    `func (t *socketModeGapTracker) reconnected(now time.Time) bool`, `func (t *socketModeGapTracker) reset()`

- [ ] **Step 1: Write the failing tests in `pkg/connector/catchup_test.go`**

```go
package connector

import (
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
)

func msg(ts string) slack.Message {
	return slack.Message{Msg: slack.Msg{Timestamp: ts}}
}

func parent(ts string, replies int, latestReply string) slack.Message {
	m := msg(ts)
	m.ThreadTimestamp = ts
	m.ReplyCount = replies
	m.LatestReply = latestReply
	return m
}

func lastReplies(m map[string]string) func(string) (string, bool) {
	return func(parentTS string) (string, bool) {
		ts, ok := m[parentTS]
		return ts, ok
	}
}

func TestPlanCatchUp(t *testing.T) {
	windowStart := time.Unix(1700000000, 0)
	history := []slack.Message{ // newest first, like conversations.history
		msg("1700000500.000100"),
		parent("1700000490.000000", 1, "1700000495.000000"), // newer than lastBridged: left to forward backfill
		parent("1700000400.000000", 2, "1700000450.000000"), // has a reply newer than the last bridged one
		parent("1700000300.000000", 1, "1700000350.000000"), // up to date
		parent("1700000200.000000", 1, "1700000250.000000"), // no bridged message in the thread
		parent("1699999000.000000", 1, "1700000460.000000"), // parent older than the window
	}
	bridged := map[string]string{
		"1700000400.000000": "1700000420.000000",
		"1700000300.000000": "1700000350.000000",
	}

	latest, threads := planCatchUp(history, "1700000480.000000", windowStart, lastReplies(bridged))

	assert.Equal(t, "1700000500.000100", latest)
	assert.Equal(t, []threadCatchUp{
		{ParentTS: "1700000400.000000", OldestTS: "1700000420.000000"},
		{ParentTS: "1700000200.000000", OldestTS: "1700000200.000000"},
	}, threads)
}

func TestPlanCatchUpEmptyHistory(t *testing.T) {
	latest, threads := planCatchUp(nil, "1700000480.000000", time.Unix(1700000000, 0), lastReplies(nil))
	assert.Equal(t, "", latest)
	assert.Empty(t, threads)
}

func TestPlanCatchUpEmptyPortalLeavesThreadsToBackfill(t *testing.T) {
	history := []slack.Message{parent("1700000400.000000", 2, "1700000450.000000")}
	latest, threads := planCatchUp(history, "", time.Unix(1700000000, 0), lastReplies(nil))
	assert.Equal(t, "1700000400.000000", latest)
	assert.Empty(t, threads)
}

func TestPlanCatchUpSkipsBroadcastReplies(t *testing.T) {
	broadcast := parent("1700000460.000000", 3, "1700000470.000000")
	broadcast.ThreadTimestamp = "1700000400.000000" // a reply also sent to the channel, not a thread parent
	latest, threads := planCatchUp([]slack.Message{broadcast}, "1700000480.000000", time.Unix(1700000000, 0), lastReplies(nil))
	assert.Equal(t, "1700000460.000000", latest)
	assert.Empty(t, threads)
}

func TestPlanCatchUpComparesTimestampsNumerically(t *testing.T) {
	// "1000000001.000000" < "999999999.000000" as strings, but it is newer.
	history := []slack.Message{parent("999999990.000000", 2, "1000000001.000000")}
	bridged := map[string]string{"999999990.000000": "999999999.000000"}
	_, threads := planCatchUp(history, "1000000005.000000", time.Unix(0, 0), lastReplies(bridged))
	assert.Equal(t, []threadCatchUp{{ParentTS: "999999990.000000", OldestTS: "999999999.000000"}}, threads)
}

func TestSocketModeGapTracker(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	var tr socketModeGapTracker

	assert.False(t, tr.reconnected(t0), "first connection is not a reconnect")

	tr.lost(t0)
	assert.False(t, tr.reconnected(t0.Add(30*time.Second)), "short gap")

	tr.lost(t0.Add(time.Minute))
	tr.lost(t0.Add(2 * time.Minute)) // later loss events keep the first loss time
	assert.True(t, tr.reconnected(t0.Add(2*time.Minute+30*time.Second)), "gap measured from the first loss")

	assert.False(t, tr.reconnected(t0.Add(10*time.Minute)), "connected again without a loss in between")

	tr.lost(t0.Add(11 * time.Minute))
	tr.reset()
	assert.False(t, tr.reconnected(t0.Add(20*time.Minute)), "reset forgets the loss")
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./pkg/connector -run 'TestPlanCatchUp|TestSocketModeGapTracker'`
Expected: FAIL to build with `undefined: planCatchUp`, `undefined: threadCatchUp` and `undefined: socketModeGapTracker`.

- [ ] **Step 3: Create `pkg/connector/catchup.go` with the pure parts**

```go
// mautrix-slack - A Matrix-Slack puppeting bridge.
// Copyright (C) 2026 kisepichu
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package connector

import (
	"sync"
	"time"

	"github.com/slack-go/slack"

	"go.mau.fi/mautrix-slack/pkg/slackid"
)

// threadCatchUpWindow bounds how old a thread parent may be for its missed replies to be caught up.
const threadCatchUpWindow = 7 * 24 * time.Hour

// socketModeCatchUpGap is how long the Socket Mode connection must have been lost before
// a reconnect starts a catch-up. Routine connection refreshes are much shorter.
const socketModeCatchUpGap = time.Minute

// threadCatchUp is a thread whose replies newer than OldestTS have to be fetched.
type threadCatchUp struct {
	ParentTS string
	OldestTS string
}

func slackTimestampAfter(a, b string) bool {
	return slackid.ParseSlackTimestamp(a).After(slackid.ParseSlackTimestamp(b))
}

// planCatchUp decides what a catch-up has to fetch for one channel.
//
// history is conversations.history, newest first. lastBridgedTS is the timestamp of the newest bridged message in
// the portal ("" if the portal has none). lastThreadReply reports the newest bridged message of a thread (the parent
// itself if no reply is bridged), or false if nothing of the thread is bridged.
//
// latestTS is the newest message in history, to be passed to the chat resync so that bridgev2 forward-backfills
// the main timeline. threads lists the threads whose parents are at or before lastBridgedTS and inside the window,
// but have replies newer than what is bridged. Parents newer than lastBridgedTS are covered by the forward backfill.
func planCatchUp(
	history []slack.Message,
	lastBridgedTS string,
	windowStart time.Time,
	lastThreadReply func(parentTS string) (string, bool),
) (latestTS string, threads []threadCatchUp) {
	if len(history) == 0 {
		return "", nil
	}
	latestTS = history[0].Timestamp
	if lastBridgedTS == "" {
		return latestTS, nil
	}
	for _, msg := range history {
		isParent := msg.ReplyCount > 0 && (msg.ThreadTimestamp == "" || msg.ThreadTimestamp == msg.Timestamp)
		if !isParent ||
			slackid.ParseSlackTimestamp(msg.Timestamp).Before(windowStart) ||
			slackTimestampAfter(msg.Timestamp, lastBridgedTS) {
			continue
		}
		anchor, ok := lastThreadReply(msg.Timestamp)
		if !ok {
			anchor = msg.Timestamp
		}
		if slackTimestampAfter(msg.LatestReply, anchor) {
			threads = append(threads, threadCatchUp{ParentTS: msg.Timestamp, OldestTS: anchor})
		}
	}
	return latestTS, threads
}

// socketModeGapTracker measures how long the Socket Mode connection was lost.
type socketModeGapTracker struct {
	lock      sync.Mutex
	connected bool
	lostAt    time.Time
}

// lost records that the connection was lost. Only the first loss after a connection counts.
func (t *socketModeGapTracker) lost(now time.Time) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.connected {
		t.connected = false
		t.lostAt = now
	}
}

// reconnected records a successful connection and reports whether it ended a gap longer than socketModeCatchUpGap.
func (t *socketModeGapTracker) reconnected(now time.Time) bool {
	t.lock.Lock()
	defer t.lock.Unlock()
	endedGap := !t.connected && !t.lostAt.IsZero() && now.Sub(t.lostAt) > socketModeCatchUpGap
	t.connected = true
	t.lostAt = time.Time{}
	return endedGap
}

// reset forgets the connection state, for a fresh connect.
func (t *socketModeGapTracker) reset() {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.connected = false
	t.lostAt = time.Time{}
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./pkg/connector -run 'TestPlanCatchUp|TestSocketModeGapTracker' -v`
Expected: `--- PASS` for all six tests, then `ok  	go.mau.fi/mautrix-slack/pkg/connector`.

- [ ] **Step 5: Vet and format check**

Run: `go vet ./pkg/connector && go run golang.org/x/tools/cmd/goimports@latest -local go.mau.fi/mautrix-slack -l pkg/connector`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add pkg/connector/catchup.go pkg/connector/catchup_test.go
git commit --no-gpg-sign -m "connector: plan catch-up of missed messages and thread replies for app logins"
```

---

### Task 3: Catch-up runner and triggers

**Files:**
- Modify: `pkg/connector/catchup.go` (append the runner)
- Modify: `pkg/connector/client.go:154-157` (fields), `pkg/connector/client.go:392-397` (startup trigger)
- Modify: `pkg/connector/handleslack.go:176-183` (reconnect trigger)

**Interfaces:**
- Consumes (Task 2): `threadCatchUpWindow`, `threadCatchUp`, `slackTimestampAfter`, `planCatchUp`,
  `socketModeGapTracker` (`lost`, `reconnected`, `reset`).
- Consumes (upstream): `(*SlackClient).queueCatchUpResync(networkid.PortalKey, map[string]string)`,
  `(*SlackClient).HandleSlackEvent(context.Context, any)`, `slackAPIErrorCode(error) string`,
  `maxSlackPaginationPages`, `DB.UserPortal.GetAllForLogin`,
  `DB.Message.GetLastNonFakePartAtOrBeforeTime(ctx, networkid.PortalKey, time.Time)`,
  `DB.Message.GetLastThreadMessage(ctx, networkid.PortalKey, networkid.MessageID)`.
- Produces: `func (s *SlackClient) triggerAppCatchUp(ctx context.Context)`.

- [ ] **Step 1: Add fields to `SlackClient` in `pkg/connector/client.go`**

After the line `rtmHelloReceived atomic.Bool` (line 157), add:

```go

	socketGap      socketModeGapTracker
	catchUpLock    sync.Mutex
	catchUpRunning bool
	catchUpPending bool
```

- [ ] **Step 2: Append the runner to `pkg/connector/catchup.go`**

Extend the import block to:

```go
import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-slack/pkg/slackid"
)
```

Append:

```go
type appCatchUpStats struct {
	rooms           int
	roomsWithMissed int
	threads         int
	replies         int
}

// triggerAppCatchUp starts a catch-up for an app login. If one is already running, it runs once more afterwards.
func (s *SlackClient) triggerAppCatchUp(ctx context.Context) {
	s.catchUpLock.Lock()
	if s.catchUpRunning {
		s.catchUpPending = true
		s.catchUpLock.Unlock()
		return
	}
	s.catchUpRunning = true
	s.catchUpLock.Unlock()
	go func() {
		for {
			s.runAppCatchUp(ctx)
			s.catchUpLock.Lock()
			if !s.catchUpPending || ctx.Err() != nil {
				s.catchUpRunning = false
				s.catchUpPending = false
				s.catchUpLock.Unlock()
				return
			}
			s.catchUpPending = false
			s.catchUpLock.Unlock()
		}
	}()
}

func (s *SlackClient) runAppCatchUp(ctx context.Context) {
	log := zerolog.Ctx(ctx).With().Str("action", "app catch-up").Logger()
	ctx = log.WithContext(ctx)
	if s.Client == nil {
		return
	}
	start := time.Now()
	userPortals, err := s.UserLogin.Bridge.DB.UserPortal.GetAllForLogin(ctx, s.UserLogin.UserLogin)
	if err != nil {
		log.Err(err).Msg("Failed to fetch user portals")
		return
	}
	windowStart := start.Add(-threadCatchUpWindow)
	var stats appCatchUpStats
	for _, up := range userPortals {
		if ctx.Err() != nil {
			return
		}
		_, channelID := slackid.ParsePortalID(up.Portal.ID)
		if channelID == "" {
			continue
		}
		stats.rooms++
		err = s.catchUpPortal(ctx, up.Portal, channelID, windowStart, &stats)
		if err != nil {
			switch slackAPIErrorCode(err) {
			case "channel_not_found", "not_in_channel":
				log.Debug().Err(err).Str("channel_id", channelID).Msg("Skipping catch-up of a channel the user is not in")
			default:
				log.Err(err).Str("channel_id", channelID).Msg("Failed to catch up channel")
			}
		}
	}
	log.Info().
		Int("rooms_checked", stats.rooms).
		Int("rooms_with_missed_messages", stats.roomsWithMissed).
		Int("threads_read", stats.threads).
		Int("replies_dispatched", stats.replies).
		Dur("duration", time.Since(start)).
		Msg("Finished catch-up")
}

func (s *SlackClient) catchUpPortal(
	ctx context.Context,
	portalKey networkid.PortalKey,
	channelID string,
	windowStart time.Time,
	stats *appCatchUpStats,
) error {
	history, err := s.fetchHistorySince(ctx, channelID, windowStart)
	if err != nil {
		return err
	}
	msgDB := s.UserLogin.Bridge.DB.Message
	var lastBridgedTS string
	lastBridged, err := msgDB.GetLastNonFakePartAtOrBeforeTime(ctx, portalKey, time.Now())
	if err != nil {
		return fmt.Errorf("failed to get last bridged message: %w", err)
	} else if lastBridged != nil {
		_, _, lastBridgedTS, _ = slackid.ParseMessageID(lastBridged.ID)
	}
	latestTS, threads := planCatchUp(history, lastBridgedTS, windowStart, func(parentTS string) (string, bool) {
		threadMsg, err := msgDB.GetLastThreadMessage(ctx, portalKey, slackid.MakeMessageID(s.TeamID, channelID, parentTS))
		if err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Str("thread_ts", parentTS).Msg("Failed to get last bridged thread message")
			return "", false
		} else if threadMsg == nil {
			return "", false
		}
		_, _, ts, ok := slackid.ParseMessageID(threadMsg.ID)
		return ts, ok
	})
	if latestTS != "" {
		if lastBridgedTS == "" || slackTimestampAfter(latestTS, lastBridgedTS) {
			stats.roomsWithMissed++
		}
		s.queueCatchUpResync(portalKey, map[string]string{channelID: latestTS})
	}
	for _, thread := range threads {
		stats.threads++
		dispatched, err := s.dispatchMissedReplies(ctx, channelID, thread)
		stats.replies += dispatched
		if err != nil {
			return fmt.Errorf("failed to fetch replies of thread %s: %w", thread.ParentTS, err)
		}
	}
	return nil
}

// fetchHistorySince reads conversations.history newest first until a page reaches a message older than windowStart.
// The first page is always returned, so the newest message is known even in quiet channels.
func (s *SlackClient) fetchHistorySince(ctx context.Context, channelID string, windowStart time.Time) ([]slack.Message, error) {
	params := &slack.GetConversationHistoryParameters{ChannelID: channelID, Limit: 200}
	var history []slack.Message
	for range maxSlackPaginationPages {
		resp, err := s.Client.GetConversationHistoryContext(ctx, params)
		if err != nil {
			return nil, err
		}
		history = append(history, resp.Messages...)
		if !resp.HasMore || resp.ResponseMetadata.Cursor == "" || len(resp.Messages) == 0 ||
			slackid.ParseSlackTimestamp(resp.Messages[len(resp.Messages)-1].Timestamp).Before(windowStart) {
			break
		}
		params.Cursor = resp.ResponseMetadata.Cursor
	}
	return history, nil
}

// dispatchMissedReplies fetches the replies of a thread newer than thread.OldestTS and handles them like
// Socket Mode message events. bridgev2 ignores replies that are already bridged.
func (s *SlackClient) dispatchMissedReplies(ctx context.Context, channelID string, thread threadCatchUp) (int, error) {
	params := &slack.GetConversationRepliesParameters{
		GetConversationHistoryParameters: slack.GetConversationHistoryParameters{
			ChannelID: channelID,
			Oldest:    thread.OldestTS,
			Limit:     200,
		},
		Timestamp: thread.ParentTS,
	}
	dispatched := 0
	for range maxSlackPaginationPages {
		resp, err := s.Client.GetConversationRepliesContext(ctx, params)
		if err != nil {
			return dispatched, err
		}
		for i := range resp.Messages {
			reply := &resp.Messages[i]
			if reply.Timestamp == thread.ParentTS || !slackTimestampAfter(reply.Timestamp, thread.OldestTS) {
				continue
			}
			reply.Channel = channelID
			s.HandleSlackEvent(ctx, (*slack.MessageEvent)(reply))
			dispatched++
		}
		if !resp.HasMore || resp.ResponseMetadata.Cursor == "" {
			break
		}
		params.Cursor = resp.ResponseMetadata.Cursor
	}
	return dispatched, nil
}
```

- [ ] **Step 3: Trigger at startup in `connect` (`pkg/connector/client.go`)**

Replace the `else` branch at lines 392-395:

```go
	} else {
		go s.consumeSocketModeEvents(ctx)
		go s.runSocketMode(ctx, cancel)
	}
```

with:

```go
	} else {
		s.socketGap.reset()
		go s.consumeSocketModeEvents(ctx)
		go s.runSocketMode(ctx, cancel)
	}
```

and replace the last lines of `connect` (lines 396-397):

```go
	go s.SyncEmojis(ctx)
	go s.syncChannels(ctx, prefetchedChannels, prefetched)
```

with:

```go
	go s.SyncEmojis(ctx)
	go s.syncChannels(ctx, prefetchedChannels, prefetched)
	if !s.IsRealUser {
		s.triggerAppCatchUp(ctx)
	}
```

- [ ] **Step 4: Trigger on reconnect in `HandleSocketModeEvent` (`pkg/connector/handleslack.go`)**

Replace lines 178-183:

```go
	case socketmode.EventTypeConnecting:
		s.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnecting})
	case socketmode.EventTypeConnectionError:
		s.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: "slack-socketmode-connection-error"})
	case socketmode.EventTypeConnected:
		s.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
```

with:

```go
	case socketmode.EventTypeConnecting:
		s.socketGap.lost(time.Now())
		s.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnecting})
	case socketmode.EventTypeConnectionError:
		s.socketGap.lost(time.Now())
		s.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateTransientDisconnect, Error: "slack-socketmode-connection-error"})
	case socketmode.EventTypeDisconnect:
		s.socketGap.lost(time.Now())
	case socketmode.EventTypeConnected:
		s.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected})
		if s.socketGap.reconnected(time.Now()) {
			s.UserLogin.Log.Info().Msg("Socket Mode reconnected after a long disconnection, catching up")
			s.triggerAppCatchUp(ctx)
		}
```

(`time` is already imported in `handleslack.go`.)

- [ ] **Step 5: Build, vet, test, format and staticcheck**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: no build or vet output; `ok` for every package with tests.

Run: `go run golang.org/x/tools/cmd/goimports@latest -local go.mau.fi/mautrix-slack -l . && go run honnef.co/go/tools/cmd/staticcheck@latest ./...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add pkg/connector/catchup.go pkg/connector/client.go pkg/connector/handleslack.go
git commit --no-gpg-sign -m "connector: catch up on missed messages and thread replies for app logins"
```

---

### Task 4: Manual verification in a personal test workspace, then push

This task needs the user. The agent builds and runs the development bridge and watches its logs. The user creates
the test workspace and the app, logs in through an encrypted DM, and performs the Slack and Matrix actions. All
writes go to the personal test workspace only.

**Files:** none (no code changes unless a check fails; fixes go back through Task 2 or 3 with a test where the
behavior is testable).

- [ ] **Step 1: Build the development binary**

Run: `umask 077 && mkdir -p /tmp/sh-slackdev && go build -tags goolm -o /tmp/sh-slackdev/mautrix-slack ./cmd/mautrix-slack`
Expected: exit 0, the binary exists. (`goolm` avoids needing libolm on the development machine. leafeon builds with
libolm through nixpkgs.)

- [ ] **Step 2: Generate the development bridge config**

```bash
umask 077
bbctl config --type slack -o /tmp/sh-slackdev/config.yaml sh-slackdev
yq() { nix run nixpkgs#yq-go -- "$@"; }
yq -i '
  .logging.min_level = "debug" |
  .logging.writers = [{"type": "stdout", "format": "pretty"}] |
  .appservice.hostname = "127.0.0.1" |
  .appservice.port = 29336 |
  .matrix.message_error_notices = true |
  .bridge.bridge_status_notices = "errors" |
  .network.mute_channels_by_default = true
' /tmp/sh-slackdev/config.yaml
```

Expected: the file exists with mode 600.

- [ ] **Step 3: Run the bridge as a named service**

Run (cwd `/tmp/sh-slackdev`): `./mautrix-slack -c config.yaml`
Expected log: the bridge connects to Beeper over the websocket and starts without errors.

- [ ] **Step 4: User creates the test workspace and logs in**

The user:
1. creates a free personal Slack workspace,
2. creates an app there from `app-manifest-user.yaml`, installs it, and creates an app-level token with
   `connections:write`,
3. opens an encrypted DM with `@sh-slackdevbot:beeper.local` from a cross-signed device, sends `login app`, pastes
   the `xoxp-` and `xapp-` tokens, and redacts the messages if the bridge did not.

Expected: `Successfully logged into <test workspace> as <user>`. The log shows a `Finished catch-up` line with
`rooms_checked=0` (first login).

- [ ] **Step 5: Run the checklist from the spec**

In the test workspace, with a second test account or Slackbot as the other party:

1. Restart the bridge. The login survives, and the log shows `Finished catch-up` with `rooms_checked` > 0.
2. Channels (muted), DMs and group DMs have rooms, with history and threads.
3. Slack → Matrix: message, edit, delete, reaction, thread reply and file arrive.
4. Matrix → Slack: message, edit, redaction, reaction, thread reply, file and mention arrive. Messages sent from
   Matrix are not duplicated. Messages the user sends in the Slack client show up as the user's own.
5. Catch-up:
   - Stop the bridge for more than 1 minute. Meanwhile, post a top-level message, and reply to a thread whose parent
     was bridged before the stop.
   - Start the bridge. Both arrive. The log shows `rooms_with_missed_messages` ≥ 1, `threads_read` ≥ 1 and
     `replies_dispatched` ≥ 1.
   - Over the next hour, no `Socket Mode reconnected after a long disconnection` line appears for routine refreshes.
6. Revoke the user token in the app settings. The bot DM shows a bad-credentials notice.

Expected: every item passes. A failure is fixed, then the affected items are rerun.

- [ ] **Step 6: Clean up the development bridge**

```bash
# stop the named service first
bbctl delete sh-slackdev
rm -rf /tmp/sh-slackdev
```

The user deletes the test app (Basic Information → Delete App) or the test workspace.

- [ ] **Step 7: Final check and push**

Run: `go test ./... && git status --short`
Expected: all `ok`; a clean working tree.

Run: `git push origin user-token`
Expected: the branch is updated on `kisepichu/mautrix-slack`.
