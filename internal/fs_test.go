package internal

import "testing"

func TestFormatBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		size int64
		want string
	}{
		{name: "bytes", size: 512, want: "512 B"},
		{name: "whole kilobytes", size: 10 * 1024, want: "10 KB"},
		{name: "fractional kilobytes", size: 1536, want: "1.5 KB"},
		{name: "whole megabytes", size: 2 * 1024 * 1024, want: "2 MB"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := FormatBytes(tt.size); got != tt.want {
				t.Fatalf("FormatBytes(%d) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}
