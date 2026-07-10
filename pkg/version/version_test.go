package version

import "testing"

func TestCompare(t *testing.T) {
	tests := []struct {
		ref string
		diff string
		want int
	}{
		// Equal
		{"1.0.0", "1.0.0", 0},
		{"2.5", "2.5", 0},
		{"nightly", "nightly", 0},

		// Greater (diff > ref)
		{"1.0.0", "2.0.0", 1},
		{"1.0", "1.1", 1},
		{"1.0-alpha", "1.0", 1},
		{"1.0", "1.0.1", 1},

		// Less (diff < ref)
		{"2.0.0", "1.0.0", -1},
		{"1.1", "1.0", -1},
		{"1.0", "1.0-alpha", -1},

		// Pre-release handling
		{"1.0.0-alpha", "1.0.0-beta", 1}, // beta > alpha
		{"1.0.0-beta", "1.0.0-rc", 1}, // rc > beta

		// Edge cases
		{"", "", 0},
		{"", "1.0", 1},
		{"1.0", "", -1},
	}

	for _, tt := range tests {
		got := Compare(tt.ref, tt.diff)
		if got != tt.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tt.ref, tt.diff, got, tt.want)
		}
	}
}

func TestIsNightly(t *testing.T) {
	if !IsNightly("nightly") {
		t.Error("expected IsNightly('nightly') to be true")
	}
	if IsNightly("1.0.0") {
		t.Error("expected IsNightly('1.0.0') to be false")
	}
}

func TestFormatNightlyVersion(t *testing.T) {
	result := FormatNightlyVersion("20250710")
	expected := "nightly-20250710"
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}
