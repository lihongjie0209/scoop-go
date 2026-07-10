package config

import (
	"testing"
)

func TestSetBoolFromBool(t *testing.T) {
	var b bool
	if err := setBool(&b, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b != true {
		t.Errorf("expected true, got %v", b)
	}

	if err := setBool(&b, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b != false {
		t.Errorf("expected false, got %v", b)
	}
}

func TestSetBoolFromString(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"1", true},
		{"yes", true},
		{"on", true},
		{"false", false},
		{"FALSE", false},
		{"False", false},
		{"0", false},
		{"no", false},
		{"off", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var b bool
			if err := setBool(&b, tt.input); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if b != tt.want {
				t.Errorf("setBool(_, %q) = %v, want %v", tt.input, b, tt.want)
			}
		})
	}
}

func TestSetBoolFromInvalidString(t *testing.T) {
	var b bool
	err := setBool(&b, "invalid")
	if err == nil {
		t.Error("expected error for invalid bool string")
	}
}

func TestSetBoolFromNonBoolType(t *testing.T) {
	var b bool
	err := setBool(&b, 42)
	if err == nil {
		t.Error("expected error for non-bool type")
	}
}

func TestSetBoolNil(t *testing.T) {
	var b bool = true
	if err := setBool(&b, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b != false {
		t.Error("expected false after nil set")
	}
}

func TestSetBoolPtrFromBool(t *testing.T) {
	var p *bool
	if err := setBoolPtr(&p, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil || *p != true {
		t.Error("expected pointer to true")
	}
}

func TestSetBoolPtrFromString(t *testing.T) {
	var p *bool
	if err := setBoolPtr(&p, "true"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil || *p != true {
		t.Error("expected pointer to true from string")
	}

	if err := setBoolPtr(&p, "false"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil || *p != false {
		t.Error("expected pointer to false from string")
	}
}

func TestSetBoolPtrInvalidString(t *testing.T) {
	var p *bool
	err := setBoolPtr(&p, "bad")
	if err == nil {
		t.Error("expected error for invalid bool string")
	}
}

func TestSetBoolPtrNil(t *testing.T) {
	var p *bool = boolPtrHelper(true)
	if err := setBoolPtr(&p, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Error("expected nil after nil set")
	}
}

func TestSetIntFromInt(t *testing.T) {
	var n int
	if err := setInt(&n, 42); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 42 {
		t.Errorf("expected 42, got %d", n)
	}
}

func TestSetIntFromFloat64(t *testing.T) {
	var n int
	if err := setInt(&n, float64(99)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 99 {
		t.Errorf("expected 99, got %d", n)
	}
}

func TestSetIntFromString(t *testing.T) {
	var n int
	if err := setInt(&n, "123"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 123 {
		t.Errorf("expected 123, got %d", n)
	}
}

func TestSetIntFromInvalidString(t *testing.T) {
	var n int
	err := setInt(&n, "not-a-number")
	if err == nil {
		t.Error("expected error for invalid int string")
	}
}

func TestSetIntNil(t *testing.T) {
	var n int = 50
	if err := setInt(&n, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Error("expected 0 after nil set")
	}
}

func TestSetString(t *testing.T) {
	var s string
	if err := setString(&s, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != "hello" {
		t.Errorf("expected 'hello', got '%s'", s)
	}
}

func TestSetStringNil(t *testing.T) {
	var s string = "keep"
	if err := setString(&s, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != "" {
		t.Error("expected empty string after nil set")
	}
}

func TestSetStringInvalidType(t *testing.T) {
	var s string
	err := setString(&s, 123)
	if err == nil {
		t.Error("expected error for non-string type")
	}
}

func TestManagerSetAndGet(t *testing.T) {
	m := NewManager("")
	tests := []struct {
		key   string
		value interface{}
	}{
		{"debug", "true"},
		{"force_update", "1"},
		{"use_sqlite_cache", "yes"},
		{"no_junction", "false"},
		{"aria2-retry-wait", "10"},
		{"aria2-split", "8"},
		{"scoop_repo", "https://github.com/example/repo"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if err := m.Set(tt.key, tt.value); err != nil {
				t.Fatalf("Set(%q, %v) error: %v", tt.key, tt.value, err)
			}
			got := m.Get(tt.key)
			if got == nil {
				t.Errorf("Get(%q) = nil, want non-nil", tt.key)
			}
		})
	}
}

func TestManagerUnset(t *testing.T) {
	m := NewManager("")
	m.Set("debug", "true")
	if err := m.Unset("debug"); err != nil {
		t.Fatalf("Unset error: %v", err)
	}
	// After unset, Get should return nil or zero value
	// debug is not a pointer type, so it returns false after unset
	val := m.Get("debug")
	if val == nil {
		t.Error("expected non-nil after unset (bool defaults to false)")
	}
}

func boolPtrHelper(b bool) *bool {
	return &b
}
