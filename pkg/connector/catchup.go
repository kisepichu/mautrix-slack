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
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/slack-go/slack"
	"maunium.net/go/mautrix/bridgev2/networkid"

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
// history is conversations.history, newest first. lastBridgedTS is the timestamp of the newest bridged message in
// the portal ("" if the portal has none). lastThreadReply reports the newest bridged message of a thread, or false
// if nothing of the thread was ever bridged: neither a reply to that parent nor the parent itself was ever stored,
// which (for a parent at or before lastBridgedTS) means the thread's replies predate the portal's bridged history.
// In that case the anchor is lastBridgedTS instead of the parent, since any reply posted while the bridge was
// connected would have been bridged and stored, so replies at or before lastBridgedTS were not missed during this
// disconnection.
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
			msg.LatestReply == "" ||
			slackid.ParseSlackTimestamp(msg.Timestamp).Before(windowStart) ||
			slackTimestampAfter(msg.Timestamp, lastBridgedTS) {
			continue
		}
		anchor, ok := lastThreadReply(msg.Timestamp)
		if !ok {
			anchor = lastBridgedTS
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

// reset forgets the connection state, for a fresh connect. It starts in the lost state with lostAt = now, so that
// a first EventTypeConnected arriving more than socketModeCatchUpGap after the connect call also triggers a
// catch-up. This is the same as a reconnect: the startup catch-up covers what came before it, and Slack's retries
// cover only a short gap after it.
func (t *socketModeGapTracker) reset(now time.Time) {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.connected = false
	t.lostAt = now
}

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
	cli := s.Client
	if cli == nil {
		return
	}
	start := time.Now()
	userPortals, err := s.UserLogin.Bridge.DB.UserPortal.GetAllForLogin(ctx, s.UserLogin.UserLogin)
	if err != nil {
		if ctx.Err() != nil {
			log.Debug().Err(err).Msg("Catch-up cancelled")
			return
		}
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
		err = s.catchUpPortal(ctx, cli, up.Portal, channelID, windowStart, &stats)
		if err != nil {
			if ctx.Err() != nil {
				log.Debug().Err(err).Str("channel_id", channelID).Msg("Catch-up cancelled")
				return
			}
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
	cli *slack.Client,
	portalKey networkid.PortalKey,
	channelID string,
	windowStart time.Time,
	stats *appCatchUpStats,
) error {
	history, err := s.fetchHistorySince(ctx, cli, channelID, windowStart)
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
			if ctx.Err() == nil {
				zerolog.Ctx(ctx).Warn().Err(err).Str("thread_ts", parentTS).Msg("Failed to get last bridged thread message")
			}
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
		dispatched, err := s.dispatchMissedReplies(ctx, cli, channelID, thread)
		stats.replies += dispatched
		if err != nil {
			return fmt.Errorf("failed to fetch replies of thread %s: %w", thread.ParentTS, err)
		}
	}
	return nil
}

// fetchHistorySince reads conversations.history newest first until a page reaches a message older than windowStart.
// The first page is always returned, so the newest message is known even in quiet channels.
func (s *SlackClient) fetchHistorySince(ctx context.Context, cli *slack.Client, channelID string, windowStart time.Time) ([]slack.Message, error) {
	params := &slack.GetConversationHistoryParameters{ChannelID: channelID, Limit: 200}
	var history []slack.Message
	for range maxSlackPaginationPages {
		resp, err := cli.GetConversationHistoryContext(ctx, params)
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
func (s *SlackClient) dispatchMissedReplies(ctx context.Context, cli *slack.Client, channelID string, thread threadCatchUp) (int, error) {
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
		resp, err := cli.GetConversationRepliesContext(ctx, params)
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
