package repo

import (
	"strings"
	"testing"
)

func TestActiveProfiles(t *testing.T) {
	s := parseSample(t)
	ids := func(profiles []Profile) string {
		var out []string
		for _, p := range profiles {
			out = append(out, p.ID)
		}
		return strings.Join(out, ",")
	}
	cases := []struct {
		name      string
		requested []string
		props     map[string]string
		want      string
	}{
		{"default only", nil, nil, "defaults"},
		{"explicit disables default", []string{"nexus-prod"}, nil, "nexus-prod"},
		{"property activation", nil, map[string]string{"ci": ""}, "ci"},
		{"deactivate default", []string{"!defaults"}, nil, ""},
		{"explicit plus property", []string{"nexus-prod"}, map[string]string{"ci": ""}, "nexus-prod,ci"},
		{"unknown profile ignored", []string{"nope"}, nil, "defaults"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ids(ActiveProfiles(s, tc.requested, tc.props)); got != tc.want {
				t.Errorf("ActiveProfiles(%v, %v) = %q, want %q", tc.requested, tc.props, got, tc.want)
			}
		})
	}
}

func TestPropertyActivation(t *testing.T) {
	cases := []struct {
		name  string
		act   ActivationProperty
		props map[string]string
		want  bool
	}{
		{"present", ActivationProperty{Name: "ci"}, map[string]string{"ci": ""}, true},
		{"absent", ActivationProperty{Name: "ci"}, nil, false},
		{"negated absent", ActivationProperty{Name: "!ci"}, nil, true},
		{"negated present", ActivationProperty{Name: "!ci"}, map[string]string{"ci": ""}, false},
		{"value match", ActivationProperty{Name: "env", Value: "prod"}, map[string]string{"env": "prod"}, true},
		{"value mismatch", ActivationProperty{Name: "env", Value: "prod"}, map[string]string{"env": "dev"}, false},
		{"value negation", ActivationProperty{Name: "env", Value: "!prod"}, map[string]string{"env": "dev"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := propertyActive(&tc.act, tc.props); got != tc.want {
				t.Errorf("propertyActive(%+v, %v) = %v, want %v", tc.act, tc.props, got, tc.want)
			}
		})
	}
}

func TestMirrorMatching(t *testing.T) {
	external := Repository{ID: "central", URL: "https://repo.maven.apache.org/maven2"}
	local := Repository{ID: "local-nexus", URL: "http://localhost:8081/repository/maven-public"}
	cases := []struct {
		pattern string
		repo    Repository
		want    bool
	}{
		{"central", external, true},
		{"other", external, false},
		{"*", external, true},
		{"*", local, true},
		{"external:*", external, true},
		{"external:*", local, false},
		{"*,!central", external, false},
		{"!central,*", external, false},
		{"central,other", external, true},
		{"external:*,!central", external, false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"/"+tc.repo.ID, func(t *testing.T) {
			if got := mirrorMatches(tc.pattern, tc.repo); got != tc.want {
				t.Errorf("mirrorMatches(%q, %s) = %v, want %v", tc.pattern, tc.repo.ID, got, tc.want)
			}
		})
	}
}

func TestInterpolate(t *testing.T) {
	t.Setenv("GOVEN_TEST_TOKEN", "s3cret")
	props := map[string]string{"nexus.host": "nexus.corp", "a": "${b}", "b": "${a}"}
	cases := []struct{ in, want string }{
		{"https://${nexus.host}/repo", "https://nexus.corp/repo"},
		{"${env.GOVEN_TEST_TOKEN}", "s3cret"},
		{"${env.GOVEN_TEST_MISSING}", "${env.GOVEN_TEST_MISSING}"},
		{"${undefined}", "${undefined}"},
		{"no refs", "no refs"},
		{"${unclosed", "${unclosed"},
	}
	for _, tc := range cases {
		if got := Interpolate(tc.in, props); got != tc.want {
			t.Errorf("Interpolate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Self-referential properties must terminate rather than loop.
	_ = Interpolate("${a}", props)
}

func TestEffectiveRepos(t *testing.T) {
	s := parseSample(t)
	t.Setenv("NEXUS_PASS", "hunter2")

	repos := EffectiveRepos(s, []string{"nexus-prod"}, nil)
	if len(repos) != 2 {
		t.Fatalf("repos = %d, want 2 (nexus + central)", len(repos))
	}
	nexus := repos[0]
	if nexus.ID != "nexus" || nexus.Username != "deployer" || nexus.Password != "hunter2" {
		t.Errorf("nexus repo = %+v (credentials should attach and interpolate)", nexus)
	}
	if !nexus.Snapshots {
		t.Error("nexus repo should have snapshots enabled")
	}
	central := repos[1]
	if central.MirrorID != "corp-mirror" || central.URL != "https://nexus.corp/repository/maven-central" {
		t.Errorf("central = %+v (should be mirrored, URL trimmed)", central)
	}
	if central.Proxy != nil {
		t.Errorf("central proxy = %+v (https target, http-only proxy)", central.Proxy)
	}
}

func TestEffectiveReposCentralOverride(t *testing.T) {
	s := &Settings{Profiles: []Profile{{
		ID:              "override",
		ActiveByDefault: true,
		Repositories:    []Repository{{ID: "central", URL: "https://mirror.example/m2", Releases: true}},
	}}}
	repos := EffectiveRepos(s, nil, nil)
	if len(repos) != 1 || repos[0].URL != "https://mirror.example/m2" {
		t.Errorf("repos = %+v (profile central must replace default)", repos)
	}
}

func TestSelectProxy(t *testing.T) {
	proxies := []Proxy{
		{ID: "inactive", Active: false, Protocol: "https", Host: "p1"},
		{ID: "corp", Active: true, Protocol: "https", Host: "p2", NonProxyHosts: "*.corp|localhost"},
	}
	if p := selectProxy(proxies, "https://nexus.corp/repo"); p != nil {
		t.Errorf("nexus.corp should be exempt via nonProxyHosts, got %+v", p)
	}
	if p := selectProxy(proxies, "https://repo.maven.apache.org/maven2"); p == nil || p.ID != "corp" {
		t.Errorf("external host should use corp proxy, got %+v", p)
	}
	if p := selectProxy(proxies, "http://plain.example/repo"); p != nil {
		t.Errorf("http target must not match https-only proxies, got %+v", p)
	}
}

func TestRemoteRepoStringMasksPassword(t *testing.T) {
	r := RemoteRepo{ID: "nexus", URL: "https://n/repo", Username: "deployer", Password: "hunter2", MirrorID: "m"}
	s := r.String()
	if strings.Contains(s, "hunter2") {
		t.Errorf("String() leaked password: %s", s)
	}
	if !strings.Contains(s, "deployer") || !strings.Contains(s, "mirror m") {
		t.Errorf("String() missing expected fields: %s", s)
	}
}

func FuzzInterpolate(f *testing.F) {
	f.Add("${a}", "a", "${b}")
	f.Add("${${nested}}", "nested", "x")
	f.Add("plain", "k", "v")
	f.Fuzz(func(t *testing.T, s, k, v string) {
		Interpolate(s, map[string]string{k: v})
	})
}
