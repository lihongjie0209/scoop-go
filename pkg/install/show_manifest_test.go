package install

import (
	"bytes"
	"strings"
	"testing"

	"github.com/scoopinstaller/scoop-go/pkg/manifest"
)

func TestShouldShowManifest(t *testing.T) {
	if ShouldShowManifest(false) {
		t.Fatal("disabled")
	}
	if !ShouldShowManifest(true) {
		t.Fatal("enabled")
	}
}

func TestConfirmManifestInstall_YesDefault(t *testing.T) {
	in := strings.NewReader("\n")
	out := &bytes.Buffer{}
	m := &manifest.Manifest{Version: "1.0.0", Homepage: "https://ex", License: "MIT", Description: "d"}
	ok, err := ConfirmManifestInstall(m, "demo", in, out)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v out=%s", ok, err, out.String())
	}
	if !strings.Contains(out.String(), "demo") || !strings.Contains(out.String(), "1.0.0") {
		t.Fatalf("output missing manifest summary: %s", out.String())
	}
}

func TestConfirmManifestInstall_No(t *testing.T) {
	in := strings.NewReader("n\n")
	out := &bytes.Buffer{}
	m := &manifest.Manifest{Version: "1.0.0", Homepage: "https://ex", License: "MIT"}
	ok, err := ConfirmManifestInstall(m, "demo", in, out)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected cancellation")
	}
}

func TestConfirmManifestInstall_ExplicitYes(t *testing.T) {
	in := strings.NewReader("Y\n")
	out := &bytes.Buffer{}
	m := &manifest.Manifest{Version: "2.0", Homepage: "https://ex", License: "MIT"}
	ok, err := ConfirmManifestInstall(m, "x", in, out)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestFormatManifestReview(t *testing.T) {
	m := &manifest.Manifest{
		Version:     "1.2.3",
		Homepage:    "https://example.com",
		License:     "MIT",
		Description: "A tool",
		URL:         manifest.FlexibleStrings{"https://example.com/a.zip"},
	}
	s := FormatManifestReview(m, "tool")
	for _, want := range []string{"tool", "1.2.3", "https://example.com", "A tool", "a.zip"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}
