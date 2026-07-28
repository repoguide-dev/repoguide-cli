package config

import (
	"os"
	"testing"
)

// An absent key means "never chosen" and takes the default; an explicit 0 means
// the user turned it off. Collapsing the two would silently re-enable a holdout
// the user had disabled.
func TestHoldoutDefaultVsExplicitOff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if got := HoldoutPct(); got != DefaultHoldoutPct {
		t.Errorf("unset config = %d, want default %d", got, DefaultHoldoutPct)
	}
	if HoldoutPctExplicitlySet() {
		t.Error("unset config must not report as explicitly set")
	}

	if err := SetHoldoutPct(0); err != nil {
		t.Fatalf("SetHoldoutPct(0): %v", err)
	}
	if got := HoldoutPct(); got != 0 {
		t.Errorf("explicit 0 = %d, want 0 (must not fall back to the default)", got)
	}
	if !HoldoutPctExplicitlySet() {
		t.Error("explicit 0 must report as set")
	}
}

func TestSetHoldoutPctRejectsOutOfRange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, v := range []int{-1, 101} {
		if err := SetHoldoutPct(v); err == nil {
			t.Errorf("SetHoldoutPct(%d) must fail", v)
		}
	}
}

func TestHoldoutSurvivesUnrelatedConfigWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := SetHoldoutPct(35); err != nil {
		t.Fatalf("SetHoldoutPct: %v", err)
	}
	if err := SetAutoFeedback(true); err != nil {
		t.Fatalf("SetAutoFeedback: %v", err)
	}
	if got := HoldoutPct(); got != 35 {
		t.Errorf("holdout = %d after an unrelated write, want 35", got)
	}
	_ = os.Remove(configPath())
}
