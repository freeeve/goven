package repo

import (
	"strings"
	"testing"
)

const versionMetadata = `<?xml version="1.0" encoding="UTF-8"?>
<metadata modelVersion="1.1.0">
  <groupId>com.example</groupId>
  <artifactId>lib</artifactId>
  <version>2.1.0-SNAPSHOT</version>
  <versioning>
    <snapshot>
      <timestamp>20260720.093012</timestamp>
      <buildNumber>3</buildNumber>
    </snapshot>
    <lastUpdated>20260720093012</lastUpdated>
    <snapshotVersions>
      <snapshotVersion>
        <extension>jar</extension>
        <value>2.1.0-20260720.093012-3</value>
        <updated>20260720093012</updated>
      </snapshotVersion>
      <snapshotVersion>
        <classifier>sources</classifier>
        <extension>jar</extension>
        <value>2.1.0-20260719.120000-2</value>
        <updated>20260719120000</updated>
      </snapshotVersion>
    </snapshotVersions>
  </versioning>
</metadata>`

const artifactMetadata = `<?xml version="1.0"?>
<metadata>
  <groupId>com.example</groupId>
  <artifactId>lib</artifactId>
  <versioning>
    <latest>2.1.0-SNAPSHOT</latest>
    <release>2.0.0</release>
    <versions><version>1.0.0</version><version>2.0.0</version><version>2.1.0-SNAPSHOT</version></versions>
    <lastUpdated>20260720093012</lastUpdated>
  </versioning>
</metadata>`

func TestParseArtifactMetadata(t *testing.T) {
	m, err := ParseMetadata(strings.NewReader(artifactMetadata))
	if err != nil {
		t.Fatalf("ParseMetadata: %v", err)
	}
	if m.Versioning.Release != "2.0.0" || len(m.Versioning.Versions.Version) != 3 {
		t.Errorf("versioning = %+v", m.Versioning)
	}
}

func TestResolveSnapshotVersion(t *testing.T) {
	m, err := ParseMetadata(strings.NewReader(versionMetadata))
	if err != nil {
		t.Fatalf("ParseMetadata: %v", err)
	}
	base := Coords{GroupID: "com.example", ArtifactID: "lib", Version: "2.1.0-SNAPSHOT", Type: "jar"}

	if got, want := m.ResolveSnapshotVersion(base), "2.1.0-20260720.093012-3"; got != want {
		t.Errorf("jar = %q, want %q (snapshotVersions entry)", got, want)
	}
	sources := base
	sources.Classifier = "sources"
	if got, want := m.ResolveSnapshotVersion(sources), "2.1.0-20260719.120000-2"; got != want {
		t.Errorf("sources = %q, want %q (older classifier entry must win over <snapshot>)", got, want)
	}
	pom := base
	pom.Type = "pom"
	if got, want := m.ResolveSnapshotVersion(pom), "2.1.0-20260720.093012-3"; got != want {
		t.Errorf("pom = %q, want %q (fallback to <snapshot> block)", got, want)
	}
}

func TestResolveSnapshotVersionLocalCopy(t *testing.T) {
	m := &Metadata{Versioning: Versioning{Snapshot: &Snapshot{LocalCopy: true, Timestamp: "20260101.000000", BuildNumber: 1}}}
	c := Coords{Version: "1.0-SNAPSHOT", Type: "jar"}
	if got := m.ResolveSnapshotVersion(c); got != "1.0-SNAPSHOT" {
		t.Errorf("localCopy = %q, want literal -SNAPSHOT", got)
	}
}

func TestResolveSnapshotVersionNoData(t *testing.T) {
	m := &Metadata{}
	c := Coords{Version: "1.0-SNAPSHOT", Type: "jar"}
	if got := m.ResolveSnapshotVersion(c); got != "1.0-SNAPSHOT" {
		t.Errorf("empty metadata = %q, want literal -SNAPSHOT", got)
	}
}

func FuzzParseMetadata(f *testing.F) {
	f.Add(versionMetadata)
	f.Add(artifactMetadata)
	f.Fuzz(func(t *testing.T, data string) {
		m, err := ParseMetadata(strings.NewReader(data))
		if err == nil && m != nil {
			m.ResolveSnapshotVersion(Coords{Version: "1.0-SNAPSHOT", Type: "jar"})
		}
	})
}
