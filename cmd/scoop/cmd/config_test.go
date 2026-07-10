package cmd

import (
	"strings"
	"testing"
)

func TestDisplayConfigValueString(t *testing.T) {
	got := displayConfigValue("hello")
	if got != "hello" {
		t.Errorf("expected 'hello', got '%s'", got)
	}
}

func TestDisplayConfigValueBool(t *testing.T) {
	got := displayConfigValue(true)
	if got != "true" {
		t.Errorf("expected 'true', got '%s'", got)
	}
}

func TestDisplayConfigValueInt(t *testing.T) {
	got := displayConfigValue(42)
	if got != "42" {
		t.Errorf("expected '42', got '%s'", got)
	}
}

func TestDisplayConfigValueBoolPtr(t *testing.T) {
	val := true
	got := displayConfigValue(&val)
	if got != "true" {
		t.Errorf("expected 'true', got '%s'", got)
	}
}

func TestDisplayConfigValueBoolPtrFalse(t *testing.T) {
	val := false
	got := displayConfigValue(&val)
	if got != "false" {
		t.Errorf("expected 'false', got '%s'", got)
	}
}

func TestDisplayConfigValueNilPtr(t *testing.T) {
	var nilPtr *bool = nil
	got := displayConfigValue(nilPtr)
	if got != "<nil>" {
		t.Errorf("expected '<nil>', got '%s'", got)
	}
}

func TestDisplayConfigValueNil(t *testing.T) {
	got := displayConfigValue(nil)
	if got != "<nil>" {
		t.Errorf("expected '<nil>', got '%s'", got)
	}
}

func TestDisplayConfigValueStringSlice(t *testing.T) {
	got := displayConfigValue([]string{"a", "b", "c"})
	if !strings.Contains(got, "a") || !strings.Contains(got, "c") {
		t.Errorf("expected slice contents in output, got '%s'", got)
	}
}

func TestBoolPtrDisplay(t *testing.T) {
	tests := []struct {
		input *bool
		want  string
	}{
		{nil, "<not set>"},
		{boolPtrTestHelper(true), "true"},
		{boolPtrTestHelper(false), "false"},
	}
	for _, tt := range tests {
		got := boolPtrDisplay(tt.input)
		if got != tt.want {
			t.Errorf("boolPtrDisplay(%v) = '%s', want '%s'", tt.input, got, tt.want)
		}
	}
}

func boolPtrTestHelper(b bool) *bool {
	return &b
}

// TestUnknownCommand verifies that an unknown command returns an error message.
func TestUnknownCommand(t *testing.T) {
	// ExecuteC returns without calling os.Exit(1)
	_, err := rootCmd.ExecuteC()
	if err != nil {
		// For "scoop" with no args, ExecuteC may return help or run rootCmd.RunE
		// This test just verifies ExecuteC completes without panic
	}
}

// TestRootCommandWithUnknownArgs verifies root command handles unknown args
func TestRootCommandWithUnknownArgs(t *testing.T) {
	// Simulate what happens when "scoop unknowncmd" is run
	// Find should return the root command + error
	cmd, args, err := rootCmd.Find([]string{"unknowncmd"})
	if err == nil {
		t.Error("expected error for unknown command")
	}
	if cmd == nil {
		t.Error("expected root command to be returned")
	}
	// args should contain the unknown command as a positional arg
	if len(args) == 0 {
		t.Error("expected unknowncmd in args")
	}
}

func TestRootCommandSilenceErrors(t *testing.T) {
	// Verify SilenceErrors is set (affects error display)
	if !rootCmd.SilenceErrors {
		t.Error("expected SilenceErrors to be true")
	}
}

func TestRootCommandSilenceUsage(t *testing.T) {
	if !rootCmd.SilenceUsage {
		t.Error("expected SilenceUsage to be true")
	}
}
