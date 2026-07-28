package mcp

import (
	"fmt"
	"strings"
	"testing"
)

func TestHoldoutIsStablePerSession(t *testing.T) {
	// A session that flipped arms between calls would be in both the treatment
	// and control group at once, which is the one failure that silently ruins
	// the experiment rather than just disabling it.
	for _, id := range []string{"abc", "019f6f73-e936-79d2-a6f6-608198412516", "z"} {
		first := isHeldOut(id, 50)
		for i := 0; i < 20; i++ {
			if isHeldOut(id, 50) != first {
				t.Fatalf("session %q changed arms between calls", id)
			}
		}
	}
}

func TestHoldoutEdgeCases(t *testing.T) {
	if isHeldOut("abc", 0) {
		t.Error("pct=0 must never hold out")
	}
	if !isHeldOut("abc", 100) {
		t.Error("pct=100 must always hold out")
	}
	// Without a stable key the assignment can't stay consistent, so withholding
	// is the more damaging of the two possible mistakes.
	if isHeldOut("", 100) {
		t.Error("empty session ID must never be held out")
	}
}

func TestHoldoutRateIsRoughlyTheConfiguredPercent(t *testing.T) {
	const pct, n = 20, 4000
	held := 0
	for i := 0; i < n; i++ {
		if isHeldOut(fmt.Sprintf("session-%d", i), pct) {
			held++
		}
	}
	got := float64(held) / n * 100
	if got < pct-3 || got > pct+3 {
		t.Errorf("holdout rate = %.1f%%, want ~%d%%", got, pct)
	}
}

func TestHoldoutResponseCarriesMarkerAndDiscouragesRetry(t *testing.T) {
	resp := holdoutResponse(20)
	if !strings.Contains(resp, HoldoutMarker) {
		t.Error("response must carry the marker or the parser cannot find the control group")
	}
	if !strings.Contains(resp, "Do not call") {
		t.Error("response must discourage retrying, which would distort the measurement")
	}
}
