package status

import "testing"

func TestDepAppName(t *testing.T) {
	cases := map[string]string{
		"git":         "git",
		"main/git":    "git",
		"main/git.json": "git",
		"extras/foo":  "foo",
		"  bar  ":     "bar",
	}
	for in, want := range cases {
		if got := depAppName(in); got != want {
			t.Errorf("depAppName(%q)=%q want %q", in, got, want)
		}
	}
}
