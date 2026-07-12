package main

import (
	"strings"
	"testing"
)

func TestExpandWindowsEnv(t *testing.T) {
	t.Setenv("TEST_VAR", "hello")
	t.Setenv("SystemRoot", "C:\\Windows")

	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"before %TEST_VAR% after", "before hello after"},
		{"%TEST_VAR%", "hello"},
		{"no vars here", "no vars here"},
		{"%%", "%"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := expandWindowsEnv(tt.input)
			if got != tt.want {
				t.Errorf("expandWindowsEnv(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExpandEnvVarsBothStyles(t *testing.T) {
	t.Setenv("MY_VAR", "world")
	result := expandEnvVars("hello %MY_VAR% and $MY_VAR")
	if !strings.Contains(result, "world") {
		t.Errorf("expected MY_VAR to be expanded, got %q", result)
	}
}
