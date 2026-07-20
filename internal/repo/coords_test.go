package repo

import "testing"

func TestParseCoords(t *testing.T) {
	cases := []struct {
		in   string
		want Coords
		err  bool
	}{
		{in: "org.apache.commons:commons-lang3:3.14.0",
			want: Coords{GroupID: "org.apache.commons", ArtifactID: "commons-lang3", Version: "3.14.0", Type: "jar"}},
		{in: "g:a:1.0:war",
			want: Coords{GroupID: "g", ArtifactID: "a", Version: "1.0", Type: "war"}},
		{in: "g:a:1.0:jar:sources",
			want: Coords{GroupID: "g", ArtifactID: "a", Version: "1.0", Type: "jar", Classifier: "sources"}},
		{in: "g:a", err: true},
		{in: "g:a:1:t:c:extra", err: true},
		{in: "g::1.0", err: true},
		{in: "", err: true},
	}
	for _, tc := range cases {
		got, err := ParseCoords(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("ParseCoords(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseCoords(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseCoords(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestCoordsRoundTrip(t *testing.T) {
	for _, s := range []string{
		"g:a:1.0",
		"g:a:1.0:war",
		"g:a:1.0:jar:sources",
	} {
		c, err := ParseCoords(s)
		if err != nil {
			t.Fatalf("ParseCoords(%q): %v", s, err)
		}
		if c.String() != s {
			t.Errorf("round trip %q -> %q", s, c.String())
		}
	}
}

func TestCoordsPaths(t *testing.T) {
	c := Coords{GroupID: "com.example.app", ArtifactID: "lib", Version: "2.1.0-SNAPSHOT", Type: "jar", Classifier: "sources"}
	if !c.IsSnapshot() {
		t.Error("IsSnapshot() = false")
	}
	if got, want := c.ArtifactPath("2.1.0-20260720.093012-3"),
		"com/example/app/lib/2.1.0-SNAPSHOT/lib-2.1.0-20260720.093012-3-sources.jar"; got != want {
		t.Errorf("ArtifactPath = %q, want %q", got, want)
	}
	if got, want := c.VersionMetadataPath(), "com/example/app/lib/2.1.0-SNAPSHOT/maven-metadata.xml"; got != want {
		t.Errorf("VersionMetadataPath = %q, want %q", got, want)
	}
	if got, want := c.ArtifactMetadataPath(), "com/example/app/lib/maven-metadata.xml"; got != want {
		t.Errorf("ArtifactMetadataPath = %q, want %q", got, want)
	}
	rel := Coords{GroupID: "g", ArtifactID: "a", Version: "1.0", Type: "jar"}
	if got, want := rel.ArtifactPath("1.0"), "g/a/1.0/a-1.0.jar"; got != want {
		t.Errorf("ArtifactPath = %q, want %q", got, want)
	}
}

func FuzzParseCoords(f *testing.F) {
	f.Add("g:a:1.0:jar:sources")
	f.Add("::::")
	f.Fuzz(func(t *testing.T, s string) {
		c, err := ParseCoords(s)
		if err != nil {
			return
		}
		if c.GroupID == "" || c.ArtifactID == "" || c.Version == "" || c.Type == "" {
			t.Errorf("ParseCoords(%q) accepted empty required field: %+v", s, c)
		}
	})
}
