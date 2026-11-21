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
