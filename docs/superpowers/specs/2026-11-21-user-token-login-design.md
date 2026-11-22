# User-token app login — design

- Date: 2026-11-21
- Branch: `user-token`, based on upstream `v0.2611.0`

## Summary

Let `login app` accept a Slack **user token** (`xoxp-`) in addition to a bot token, so that a person can puppet
their own account through an internal Slack app they created. Add a **catch-up** for app logins, which upstream
skips, so that messages and thread replies posted while the bridge was disconnected are bridged after it reconnects.

## Background

Upstream picks one of two client modes based on the token prefix (`IsRealUser` in `pkg/connector/client.go`):

| | Browser session (`xoxc-`/`xoxs-`) | App (`login app`, today only `xoxb-`) |
|---|---|---|
| Events | RTM | Socket Mode (`xapp-` app-level token) |
| Startup | `client.boot`, `client.counts` (undocumented) | `team.info`, `users.info` |
| File upload | web client upload | `files.uploadV2` |
| Read receipts, typing | sent | not sent |
| Catch-up after reconnect | from `client.counts` | none (`getLatestMessageIDs` returns nil) |

Browser session tokens can be unusable:

- A workspace may invalidate a session when it is used from a different host or IP than the browser that created it
  (session anomaly detection).
- In a workspace that requires SSO, carrying a browser session to a server works around the workspace's security
  controls.

An internal Slack app that has user token scopes avoids both problems:

- Workspace admins can see the app in app management and remove it.
- The token is issued to the app by OAuth and is not tied to a browser session.

A manual probe in a real Enterprise Grid workspace confirmed the following:

- An internal app with only user token scopes and Socket Mode could be installed.
- From a server, a user token passed `auth.test`, and `chat.postMessage` to the user's own DM succeeded.
- Socket Mode delivered user events promptly: `message` in public channels, private channels and DMs,
  `message_changed`, `reaction_added`, and `bot_message` from other apps.
- Messages posted this way show no "Sent using" line in the Slack client. The API still marks them: they carry
  `app_id`, `bot_id` and `bot_profile`, and lack `client_msg_id`.

The app mode already uses only documented APIs, and they work with a user token. So the user token goes through the
existing app mode. This design keeps the mode split unchanged.

## Goals

- `login app` accepts `xoxp-` + `xapp-`. With a user token, the login is the authorizing user: their messages from
  other Slack clients show up as their own (double puppeting), and messages sent from Matrix post as them.
- After startup, and after a Socket Mode reconnect that followed a disconnection of more than 1 minute, the bridge
  catches up on:
  - top-level messages missed while it was disconnected, using upstream's forward backfill, and
  - thread replies missed while it was disconnected, when the thread's parent was posted within the last 7 days.
- The catch-up also works for existing bot-token app logins.
- A manifest for a user-token app ships in the repository.

## Non-goals

- Read receipts and typing for app logins. `conversations.mark` needs `channels:write` and the like, which also allow
  creating, renaming and archiving channels. The user-token manifest leaves these scopes out.
- An OAuth flow inside the bridge. The user copies the tokens from the app's settings page.
- The Slack MCP server.
- Catching up on edits, deletions and reactions missed while disconnected.
- Any change to the browser-session mode.

## Design

### 1. Accept user tokens in `login app` (`pkg/connector/login-app.go`)

- Change the first field's pattern from `^xoxb-.+$` to `^xox[bp]-.+$`.
- Rename the field to "Bot or user token" and update its description. The field ID stays `bot_token`, so existing
  provisioning clients keep working.
- `auth.test` with a user token returns the authorizing user. The existing login ID (`team ID` + `user ID`), the own
  ghost and `IsFromMe` therefore refer to that user without further changes.
- `IsRealUser` stays false for `xoxp-`, so the login uses the app mode: Socket Mode, documented APIs and
  `files.uploadV2`.

### 2. Catch-up for app logins (`pkg/connector/client.go`, `pkg/connector/handleslack.go`)

#### Triggers

- **Startup**: when `connect` finishes for an app login, after it starts `syncChannels`.
- **Reconnect**: on `socketmode.EventTypeConnected`, if the connection had been lost for more than 1 minute. The
  gap starts at the first `EventTypeConnecting`, `EventTypeConnectionError` or `EventTypeDisconnect` after a
  `EventTypeConnected`, or, for the very first connection, at the moment `connect` starts the Socket Mode goroutines:
  the gap tracker begins in the lost state so that a first `EventTypeConnected` arriving more than 1 minute after
  `connect` started also counts. This is the same as a reconnect: the startup catch-up covers what came before it,
  and Slack's retries cover only a short gap after it. Slack refreshes Socket Mode connections routinely and the
  client reconnects within seconds; Slack retries unacknowledged events in that window, so these refreshes do not
  trigger a catch-up.
- At most one catch-up runs per login. A trigger that arrives during a run schedules one more run after it.

#### Per-room algorithm

The catch-up runs in a background goroutine. It goes through the login's portals one at a time (from
`UserPortal.GetAllForLogin`) and skips the team portal. For each channel:

1. Read `conversations.history` newest-first (`limit=200`), page by page, until a page reaches a message older than
   7 days or there are no more pages. Keep page 1's newest message even if it is older than 7 days.
2. **Top-level messages.** Pass the newest message's ts as the latest message to the existing
   `queueCatchUpResync`. bridgev2 compares it with the portal's last message and, if newer, runs a forward backfill
   (`FetchMessages` with `Forward`, capped by `max_catchup_messages`), but only when `backfill.enabled` is `true`.
   Configs generated by `bbctl` turn it on; on bridgev2's own default (`false`) this step does nothing and only
   step 3 catches anything up. Threads whose parents fall inside the backfilled range are backfilled by bridgev2 as
   part of it (`ShouldBackfillThread`).
3. **Thread replies.** Before queueing step 2, read the newest message bridged in the portal (`lastBridged`).
   For each parent in the window with `reply_count > 0`, a non-empty `latest_reply`, and `ts <= lastBridged`:
   - The anchor is the last bridged message in that thread (`DB.Message.GetLastThreadMessage`). If the thread has no
     bridged message — no reply to that parent, nor the parent itself, was ever bridged — the anchor is
     `lastBridged` instead of the parent. Any reply posted while the bridge was connected would have been bridged,
     so replies at or before `lastBridged` were not missed during this disconnection; the thread predates the
     portal's bridged history instead.
   - If `latest_reply` is newer than the anchor, read `conversations.replies` (`ts` = parent, `oldest` = anchor,
     paginated). Dispatch each reply newer than the anchor to `HandleSlackEvent` as a `*slack.MessageEvent` with
     `Channel` set, the same path that Socket Mode events take. bridgev2 drops IDs that are already bridged
     (`Ignoring duplicate message`).

   Parents newer than `lastBridged` are left to step 2.

Slack timestamps are compared as numbers (seconds and microseconds), not as strings.

#### Planning function

Steps 1–3 decide what to fetch with a pure function, so that the decision can be tested without Slack:

```go
type threadCatchUp struct {
	ParentTS string // thread to read with conversations.replies
	OldestTS string // anchor; replies newer than this are dispatched
}

// planCatchUp returns the newest top-level ts (empty if history is empty) and the threads that need replies
// fetched. lastThreadReply reports the last bridged reply of a thread, if any.
func planCatchUp(
	history []slack.Message, // newest-first, as returned by conversations.history
	lastBridgedTS string,
	windowStart time.Time,
	lastThreadReply func(parentTS string) (ts string, ok bool),
) (latestTS string, threads []threadCatchUp)
```

#### Rate limits and errors

- `conversations.history` and `conversations.replies` are Tier 3 for internal apps, so they keep 50+ requests per
  minute.
- 429 responses are already retried by the Slack client: `makeSlackClient` sets `slack.OptionRetry(slackMaxRetries)`
  (5), which waits for `Retry-After` before each retry. If a call still fails with `*slack.RateLimitedError`, give up
  on that room as with any other error. The catch-up adds no retry loop of its own.
- `channel_not_found` or `not_in_channel` means the user has left the channel: skip the room at debug level. Any other
  error is logged with the channel ID, and the run continues with the next room.
- At the end, log a summary at info level: rooms checked, rooms with missed top-level messages, threads read,
  replies dispatched, and duration.

### 3. Manifest for a user-token app (`app-manifest-user.yaml`)

A new file. The existing `app-manifest.yaml` (bot) stays unchanged.

- `display_information.name`: `Matrix bridge` (users rename it).
- No `bot_user`. The user token scopes are:
  - `channels:history`, `channels:read`, `groups:history`, `groups:read`
  - `im:history`, `im:read`, `mpim:history`, `mpim:read`
  - `chat:write`, `reactions:read`, `reactions:write`, `files:read`, `files:write`
  - `users:read`, `emoji:read`, `team:read`
- User events (`settings.event_subscriptions.user_events`):
  - `message.channels`, `message.groups`, `message.im`, `message.mpim`
  - `reaction_added`, `reaction_removed`
  - `channel_rename`, `group_rename`
  - `member_joined_channel`, `member_left_channel`
- Socket Mode on. Interactivity, org deployment and token rotation off.

### 4. Repository rules and README

- `AGENTS.md`:
  - Do not write information that identifies a real workspace (workspace name, team or enterprise ID, channels,
    people) in code, docs, commit messages, issues or PRs.
  - When testing, write to Slack (send, edit, delete, react) only in a personal test workspace, or only in your own
    DM in any other workspace. Reading is not restricted.
- `README.md`: a short section on user-token login. Create an app from `app-manifest-user.yaml`, install it, create
  an app-level token with `connections:write`, send `login app`, and paste both tokens.

## Error handling

- **Revoked or invalid token** (`invalid_auth`, `not_authed`, `token_revoked`, `token_expired`, `account_inactive`,
  `team_disabled`, `user_removed_from_team`, also when an admin removes the app): the login is invalidated (or
  disconnected until restart, for the account and team codes) and a bad-credentials bridge state is sent. No
  automatic re-login. Upstream does this only when connecting fails, so running app logins also detect revocation:
  - A credential error reported by Socket Mode (returned by the connection or sent as a connection error) ends the
    connection, instead of retrying with a transient-disconnect state.
  - An `auth.test` runs on every Socket Mode connection, because Socket Mode uses only the app-level token. A
    revoked bot or user token is noticed at the next connection at the latest; Slack refreshes Socket Mode
    connections periodically. Other `auth.test` errors are logged and the login keeps running.
  - Both use `isSlackCredentialError` (`pkg/connector/client.go`) and `handleConnectError`.
- **Logout**: upstream does not revoke app tokens. This stays as it is. Users revoke the tokens or uninstall the app
  in Slack.
- **Send failures**: unchanged. They are reported through the bridge's message status and error notices.

## Testing

### Automated

- `planCatchUp` table tests:
  - A parent whose `latest_reply` is newer than the last bridged reply yields a thread with `OldestTS` = that reply.
  - An equal `latest_reply` yields nothing.
  - A parent whose thread has no bridged messages yields `OldestTS` = `lastBridgedTS` if `latest_reply` is newer
    than `lastBridgedTS`, and nothing otherwise.
  - A parent with an empty `latest_reply` is skipped.
  - A parent newer than `lastBridgedTS` is skipped.
  - A parent older than the window is skipped.
  - Empty history gives an empty `latestTS`.
  - Timestamps with different digit counts compare numerically.
- `go test ./...` passes, including upstream's tests.

### Manual (personal test workspace)

Install an app from `app-manifest-user.yaml` in a personal test workspace and run the bridge as a development bridge.
Check:

1. `login app` with `xoxp-` + `xapp-` succeeds, and the login survives a restart.
2. Channels (muted when `mute_channels_by_default` is on), DMs and group DMs get rooms, with history and threads.
3. Slack to Matrix: message, edit, delete, reaction, thread reply and file arrive.
4. Matrix to Slack: the same operations and mentions arrive. Messages sent from Matrix are not duplicated. Messages
   the user sends in the Slack client appear as the user's own.
5. Catch-up (needs `backfill.enabled: true` for the top-level part; thread replies are caught up either way): stop
   the bridge for more than 1 minute. Meanwhile, post a top-level message and reply to an existing thread. After the
   bridge starts, both are bridged. Routine Socket Mode refreshes do not start a catch-up (check the logs).
6. Revoking the token in Slack produces a bridge state notice.

## Known limitations

- Missed edits, deletions and reactions are not caught up.
- Missed replies to threads whose parent is older than 7 days are not caught up.
- If a thread fetch fails in the middle of a room, after the forward backfill or replies of another thread have
  moved `lastBridged` forward, and nothing of the failed thread was bridged, the next run anchors that thread at the
  new `lastBridged`. Its missed replies at or before that point are then not caught up.
- A catch-up makes at least one API call per room. With hundreds of rooms it takes minutes.
- No read receipts or typing notifications are sent.

## Upstreaming

The changes are self-contained:

- the login pattern,
- the catch-up behind the app mode,
- a new manifest file.

They are written so that they can be proposed upstream as they are. `AGENTS.md` is specific to this fork.
