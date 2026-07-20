package main

import (
	"reflect"
	"testing"
)

func TestParseGlobal(t *testing.T) {
	g, rest, err := parseGlobal([]string{"-s", "my-settings.xml", "-Pnexus-prod,ci", "-Ddeploy.env=prod", "get", "g:a:1.0", "-o", "out"})
	if err != nil {
		t.Fatalf("parseGlobal: %v", err)
	}
	if g.userSettings != "my-settings.xml" {
		t.Errorf("userSettings = %q", g.userSettings)
	}
	if !reflect.DeepEqual(g.profiles, []string{"nexus-prod", "ci"}) {
		t.Errorf("profiles = %v", g.profiles)
	}
	if g.props["deploy.env"] != "prod" {
		t.Errorf("props = %v", g.props)
	}
	if !reflect.DeepEqual(rest, []string{"get", "g:a:1.0", "-o", "out"}) {
		t.Errorf("rest = %v", rest)
	}
}

func TestParseGlobalSeparateP(t *testing.T) {
	g, rest, err := parseGlobal([]string{"-P", "one", "-D", "doctor"})
	if err != nil {
		t.Fatalf("parseGlobal: %v", err)
	}
	if !reflect.DeepEqual(g.profiles, []string{"one"}) {
		t.Errorf("profiles = %v", g.profiles)
	}
	// A bare -D (no key) is not a property; it ends global parsing.
	if !reflect.DeepEqual(rest, []string{"-D", "doctor"}) {
		t.Errorf("rest = %v", rest)
	}
}

func TestParseGlobalMissingValue(t *testing.T) {
	if _, _, err := parseGlobal([]string{"-s"}); err == nil {
		t.Error("expected error for -s without value")
	}
}

func TestReorderArgs(t *testing.T) {
	valueFlags := map[string]bool{"o": true, "repo": true}
	cases := []struct{ in, want []string }{
		{[]string{"g:a:1.0", "-o", "dir"}, []string{"-o", "dir", "g:a:1.0"}},
		{[]string{"-o", "dir", "g:a:1.0"}, []string{"-o", "dir", "g:a:1.0"}},
		{[]string{"g:a:1.0", "--repo=id::http://x"}, []string{"--repo=id::http://x", "g:a:1.0"}},
		{[]string{"g:a:1.0"}, []string{"g:a:1.0"}},
	}
	for _, tc := range cases {
		if got := reorderArgs(tc.in, valueFlags); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("reorderArgs(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
