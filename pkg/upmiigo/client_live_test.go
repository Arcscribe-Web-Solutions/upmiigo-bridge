package upmiigo

import (
	"context"
	"os"
	"testing"
	"time"
)

// Read-only checks against a running site. Skipped unless UPMIIGO_URL and UPMIIGO_TOKEN are set:
//
//	UPMIIGO_URL=http://localhost:3000 UPMIIGO_TOKEN=umgb_... go test ./pkg/upmiigo -run Live -v
func TestLiveReadOnly(t *testing.T) {
	base, token := os.Getenv("UPMIIGO_URL"), os.Getenv("UPMIIGO_TOKEN")
	if base == "" || token == "" {
		t.Skip("set UPMIIGO_URL and UPMIIGO_TOKEN to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := NewClient(base, token)

	me, err := c.Me(ctx)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	t.Logf("me: verified %v", me.Verified)

	convs, err := c.Conversations(ctx)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	t.Logf("%d conversations", len(convs))
	for _, conv := range convs {
		if conv.Other == nil {
			continue
		}
		msgs, more, err := c.Messages(ctx, conv.ID, nil, 5)
		if err != nil {
			t.Fatalf("messages: %v", err)
		}
		t.Logf("first conversation: %d messages (more: %v)", len(msgs), more)
		for i := 1; i < len(msgs); i++ {
			if msgs[i].CreatedAt.Before(msgs[i-1].CreatedAt) {
				t.Errorf("messages not oldest-first")
			}
		}
		if _, err := c.User(ctx, conv.Other.ID); err != nil {
			t.Fatalf("user: %v", err)
		}
		break
	}

	ev, err := c.Events(ctx, "2026-01-01T00:00:00Z", 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	t.Logf("events: %d messages, %d conversations, %d reads", len(ev.Messages), len(ev.Conversations), len(ev.Reads))

	bad := NewClient(base, "umgb_definitely_not_valid_token_value")
	if _, err := bad.Me(ctx); !IsUnauthorized(err) {
		t.Errorf("bad token: expected unauthorized, got %v", err)
	}
}
