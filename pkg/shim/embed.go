package shim

import (
	_ "embed"
)

// ShimExe is the embedded Windows shim binary.
// Built from internal/shimbinary/main.go via:
//
//	cd scoop-go && GOOS=windows GOARCH=amd64 go build -o pkg/shim/shim.exe ./internal/shimbinary/
//
//go:embed shim.exe
var ShimExe []byte
