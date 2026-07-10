package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerDefaultNotDebug(t *testing.T) {
	l := NewLogger(nil, false)
	if l.IsDebug() {
		t.Error("expected debug to be false by default")
	}
}

func TestLoggerSetDebug(t *testing.T) {
	l := NewLogger(nil, false)
	l.SetDebug(true)
	if !l.IsDebug() {
		t.Error("expected debug to be true after SetDebug(true)")
	}
}

func TestLoggerDebugOutput(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf, true)

	l.Debug("test debug message: %d", 42)

	output := buf.String()
	if !strings.Contains(output, "DEBUG") {
		t.Errorf("expected DEBUG prefix in output, got: %s", output)
	}
	if !strings.Contains(output, "test debug message: 42") {
		t.Errorf("expected message in output, got: %s", output)
	}
}

func TestLoggerDebugSuppressedWhenNotEnabled(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf, false)

	l.Debug("should not appear")

	if buf.Len() > 0 {
		t.Errorf("expected no output when debug is false, got: %s", buf.String())
	}
}

func TestLoggerSetDebugFromFalseToTrue(t *testing.T) {
	// Simulate the root.go flow: initialize with debug=false, then set debug=true
	var buf bytes.Buffer
	l := NewLogger(&buf, false)

	// Initial state: debug disabled
	l.Debug("first message")
	if buf.Len() > 0 {
		t.Error("expected no output before SetDebug")
	}

	// Simulate --debug flag being applied
	l.SetDebug(true)

	// After SetDebug, debug output should appear
	l.Debug("second message: %s", "after enable")
	output := buf.String()
	if !strings.Contains(output, "second message: after enable") {
		t.Errorf("expected debug output after SetDebug(true), got: %s", output)
	}
}

func TestLogDebugPackageFunction(t *testing.T) {
	// Save and restore global logger
	savedLogger := logger
	defer func() { logger = savedLogger }()

	var buf bytes.Buffer
	logger = NewLogger(&buf, true)

	LogDebug("package level debug: %s", "works")
	output := buf.String()
	if !strings.Contains(output, "package level debug: works") {
		t.Errorf("expected LogDebug output, got: %s", output)
	}
}
