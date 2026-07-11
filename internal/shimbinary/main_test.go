package main

import "testing"

func TestExpandWindowsEnv(t *testing.T) {
	t.Setenv("SCOOP_GO_SHIM_TEST", `C:\Program Files\Scoop`)

	tests := map[string]string{
		`%SCOOP_GO_SHIM_TEST%\app.exe`: `C:\Program Files\Scoop\app.exe`,
		`prefix-%SCOOP_GO_SHIM_TEST%`:  `prefix-C:\Program Files\Scoop`,
		`%SCOOP_GO_UNKNOWN%`:           `%SCOOP_GO_UNKNOWN%`,
		`plain`:                        `plain`,
		`unclosed%VAR`:                 `unclosed%VAR`,
	}

	for input, want := range tests {
		if got := expandWindowsEnv(input); got != want {
			t.Errorf("expandWindowsEnv(%q) = %q, want %q", input, got, want)
		}
	}
}
