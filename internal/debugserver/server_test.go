package debugserver

import "testing"

func TestLoopbackAddrDefaultsAndRejectsNonLoopback(t *testing.T) {
	got, err := loopbackAddr(":9090")
	if err != nil {
		t.Fatalf("loopbackAddr(:9090): %v", err)
	}
	if got != "127.0.0.1:9090" {
		t.Fatalf("addr = %q, want 127.0.0.1:9090", got)
	}

	if _, err := loopbackAddr("0.0.0.0:9090"); err == nil {
		t.Fatal("expected non-loopback bind to be rejected")
	}
}
