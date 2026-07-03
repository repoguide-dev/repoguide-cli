package ai

import (
	"context"
	"testing"
	"time"
)

func TestNewAnthropicRequestContextIgnoresParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	ctx, cancel := newAnthropicRequestContext(parent)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatalf("derived context canceled immediately: %v", ctx.Err())
	default:
	}
}

func TestNewAnthropicRequestContextTimesOutWithinFiveMinutes(t *testing.T) {
	ctx, cancel := newAnthropicRequestContext(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("expected deadline")
	}

	remaining := time.Until(deadline)
	if remaining > anthropicRequestTimeout+time.Second {
		t.Fatalf("remaining timeout too large: %v", remaining)
	}
	if remaining < anthropicRequestTimeout-5*time.Second {
		t.Fatalf("remaining timeout too small: %v", remaining)
	}
}
