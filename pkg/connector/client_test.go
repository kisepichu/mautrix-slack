package connector

import (
	"errors"
	"fmt"
	"testing"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
)

func TestIsSlackCredentialError(t *testing.T) {
	assert.True(t, isSlackCredentialError(fmt.Errorf("failed to open socket: %w", slack.SlackErrorResponse{Err: "token_revoked"})), "wrapped token_revoked")
	assert.True(t, isSlackCredentialError(slack.SlackErrorResponse{Err: "invalid_auth"}), "invalid_auth")
	assert.False(t, isSlackCredentialError(&slack.RateLimitedError{}), "rate limit")
	assert.False(t, isSlackCredentialError(slack.SlackErrorResponse{Err: "internal_error"}), "server error")
	assert.False(t, isSlackCredentialError(errors.New("connection reset")), "non-Slack error")
}
