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
