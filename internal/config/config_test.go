package config

import (
	"math"
	"testing"
)

func TestParseMemoryLimit(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{"T-1 GB", "4GB", 4294967296, false},
		{"T-2 MB", "512MB", 536870912, false},
		{"T-3 KB", "256KB", 262144, false},
		{"T-4 B", "1024B", 1024, false},
		{"T-5 empty", "", math.MaxInt64, false},
		{"T-6 zero", "0", math.MaxInt64, false},
		{"T-7 invalid", "abc", 0, true},
		{"T-8 decimal", "1.5GB", 1610612736, false},
		{"T-9 lowercase", "2gb", 2147483648, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMemoryLimit(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMemoryLimit(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseMemoryLimit(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
