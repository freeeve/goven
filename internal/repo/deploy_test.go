package repo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var deployTime = time.Date(2026, 7, 21, 9, 30, 12, 0, time.UTC)

func writeTempArtifact(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.jar")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixtureMetadata(t *testing.T, f *fixtureServer, path string) *Metadata {
	t.Helper()
	raw, ok := f.get(path)
	if !ok {
		t.Fatalf("metadata %s not uploaded", path)
	}
	m, err := ParseMetadata(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("metadata %s unparseable: %v", path, err)
	}
	return m
}

func TestDeployRelease(t *testing.T) {
	f, repo := newFixture(t, "", "")
	c := Coords{GroupID: "com.example", ArtifactID: "lib", Version: "1.0.0", Type: "jar"}
	art := writeTempArtifact(t, "release-bytes")

	res, err := NewClient().Deploy(repo, c, art, []byte("<project/>"), deployTime)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res.ResolvedVersion != "1.0.0" {
		t.Errorf("resolved = %q", res.ResolvedVersion)
	}
	jar, ok := f.get("com/example/lib/1.0.0/lib-1.0.0.jar")
	if !ok || string(jar) != "release-bytes" {
		t.Errorf("jar = %q ok=%v", jar, ok)
	}
	if _, ok := f.get("com/example/lib/1.0.0/lib-1.0.0.pom"); !ok {
		t.Error("pom not uploaded")
	}
	sum, ok := f.get("com/example/lib/1.0.0/lib-1.0.0.jar.sha256")
	want := sha256.Sum256([]byte("release-bytes"))
	if !ok || string(sum) != hex.EncodeToString(want[:]) {
		t.Errorf("sha256 sidecar = %q ok=%v", sum, ok)
	}
	for _, ext := range []string{"md5", "sha1"} {
		if _, ok := f.get("com/example/lib/1.0.0/lib-1.0.0.jar." + ext); !ok {
			t.Errorf("%s sidecar missing", ext)
		}
	}
	if _, ok := f.get("com/example/lib/1.0.0/lib-1.0.0.jar.sha512"); ok {
		t.Error("sha512 sidecar should not be published (Maven publishes md5/sha1; goven adds sha256)")
	}
	m := fixtureMetadata(t, f, "com/example/lib/maven-metadata.xml")
	if m.Versioning.Release != "1.0.0" || m.Versioning.Latest != "" ||
		len(m.Versioning.Versions.Version) != 1 || m.Versioning.LastUpdated != "20260721093012" {
		t.Errorf("artifact metadata = %+v (deploy must set release but never latest)", m.Versioning)
	}
	if m.Versioning.SnapshotVersions != nil {
		t.Error("artifact-level metadata must not contain a snapshotVersions element")
	}
}

func TestDeploySecondReleaseMergesVersions(t *testing.T) {
	f, repo := newFixture(t, "", "")
	art := writeTempArtifact(t, "x")
	cl := NewClient()
	for _, v := range []string{"1.0.0", "1.1.0"} {
		c := Coords{GroupID: "g", ArtifactID: "a", Version: v, Type: "jar"}
		if _, err := cl.Deploy(repo, c, art, []byte("<project/>"), deployTime); err != nil {
			t.Fatalf("Deploy %s: %v", v, err)
		}
	}
	m := fixtureMetadata(t, f, "g/a/maven-metadata.xml")
	if len(m.Versioning.Versions.Version) != 2 || m.Versioning.Release != "1.1.0" {
		t.Errorf("metadata = %+v", m.Versioning)
	}
}

func TestDeploySnapshotIncrementsBuildNumber(t *testing.T) {
	f, repo := newFixture(t, "", "")
	c := Coords{GroupID: "g", ArtifactID: "a", Version: "2.0-SNAPSHOT", Type: "jar"}
	art := writeTempArtifact(t, "snap")
	cl := NewClient()

	res1, err := cl.Deploy(repo, c, art, []byte("<project/>"), deployTime)
	if err != nil {
		t.Fatalf("Deploy 1: %v", err)
	}
	if res1.ResolvedVersion != "2.0-20260721.093012-1" {
		t.Errorf("first resolved = %q", res1.ResolvedVersion)
	}
	if _, ok := f.get("g/a/2.0-SNAPSHOT/a-2.0-20260721.093012-1.jar"); !ok {
		t.Error("timestamped jar not uploaded")
	}

	later := deployTime.Add(90 * time.Second)
	res2, err := cl.Deploy(repo, c, art, []byte("<project/>"), later)
	if err != nil {
		t.Fatalf("Deploy 2: %v", err)
	}
	if res2.ResolvedVersion != "2.0-20260721.093142-2" {
		t.Errorf("second resolved = %q (buildNumber must increment)", res2.ResolvedVersion)
	}

	m := fixtureMetadata(t, f, "g/a/2.0-SNAPSHOT/maven-metadata.xml")
	if m.ModelVersion != "1.1.0" || m.Version != "2.0-SNAPSHOT" {
		t.Errorf("metadata header = modelVersion %q version %q", m.ModelVersion, m.Version)
	}
	s := m.Versioning.Snapshot
	if s == nil || s.BuildNumber != 2 || s.Timestamp != "20260721.093142" {
		t.Errorf("snapshot block = %+v", s)
	}
	// jar + pom entries, each pointing at build 2.
	if len(m.Versioning.SnapshotVersions.SnapshotVersion) != 2 {
		t.Fatalf("snapshotVersions = %+v", m.Versioning.SnapshotVersions)
	}
	for _, sv := range m.Versioning.SnapshotVersions.SnapshotVersion {
		if sv.Value != "2.0-20260721.093142-2" {
			t.Errorf("entry %s/%s value = %q", sv.Classifier, sv.Extension, sv.Value)
		}
	}
	art2 := fixtureMetadata(t, f, "g/a/maven-metadata.xml")
	if art2.Versioning.Release != "" || art2.Versioning.Latest != "" ||
		!slices.Contains(art2.Versioning.Versions.Version, "2.0-SNAPSHOT") {
		t.Errorf("artifact metadata = %+v (snapshot deploy sets neither release nor latest)", art2.Versioning)
	}
}

func TestDeploySnapshotRoundTripThroughGet(t *testing.T) {
	_, repo := newFixture(t, "", "")
	c := Coords{GroupID: "g", ArtifactID: "a", Version: "3.0-SNAPSHOT", Type: "jar"}
	art := writeTempArtifact(t, "round-trip-bytes")
	cl := NewClient()
	if _, err := cl.Deploy(repo, c, art, []byte("<project/>"), deployTime); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "fetched.jar")
	_, resolved, err := cl.FetchArtifact(c, []RemoteRepo{repo}, dest)
	if err != nil {
		t.Fatalf("FetchArtifact after deploy: %v", err)
	}
	if resolved != "3.0-20260721.093012-1" {
		t.Errorf("resolved = %q", resolved)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "round-trip-bytes" {
		t.Errorf("content = %q", got)
	}
}

func TestDeployClassifierKeepsOtherEntries(t *testing.T) {
	f, repo := newFixture(t, "", "")
	base := Coords{GroupID: "g", ArtifactID: "a", Version: "1.0-SNAPSHOT", Type: "jar"}
	art := writeTempArtifact(t, "main")
	cl := NewClient()
	if _, err := cl.Deploy(repo, base, art, []byte("<project/>"), deployTime); err != nil {
		t.Fatalf("Deploy main: %v", err)
	}
	sources := base
	sources.Classifier = "sources"
	if _, err := cl.Deploy(repo, sources, art, nil, deployTime.Add(time.Minute)); err != nil {
		t.Fatalf("Deploy sources: %v", err)
	}

	m := fixtureMetadata(t, f, "g/a/1.0-SNAPSHOT/maven-metadata.xml")
	byKey := map[string]string{}
	for _, sv := range m.Versioning.SnapshotVersions.SnapshotVersion {
		byKey[sv.Classifier+"/"+sv.Extension] = sv.Value
	}
	if len(byKey) != 3 {
		t.Fatalf("snapshotVersions = %+v (want jar, pom, sources/jar)", byKey)
	}
	if byKey["/jar"] != "1.0-20260721.093012-1" || byKey["sources/jar"] != "1.0-20260721.093112-2" {
		t.Errorf("entries = %+v (main entries must survive classifier deploy)", byKey)
	}
}

func TestDeployConcurrentDistinctGAVs(t *testing.T) {
	f, repo := newFixture(t, "", "")
	art := writeTempArtifact(t, "concurrent")
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := Coords{GroupID: "g", ArtifactID: fmt.Sprintf("a%d", i), Version: "1.0.0", Type: "jar"}
			_, errs[i] = NewClient().Deploy(repo, c, art, []byte("<project/>"), deployTime)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("deploy %d: %v", i, err)
		}
	}
	for i := range errs {
		m := fixtureMetadata(t, f, fmt.Sprintf("g/a%d/maven-metadata.xml", i))
		if m.Versioning.Release != "1.0.0" {
			t.Errorf("a%d metadata = %+v", i, m.Versioning)
		}
	}
}

func TestDeployWithAttachments(t *testing.T) {
	f, repo := newFixture(t, "", "")
	c := Coords{GroupID: "g", ArtifactID: "multi", Version: "1.0-SNAPSHOT", Type: "jar"}
	main := writeTempArtifact(t, "main")
	sources := writeTempArtifact(t, "sources-bytes")
	dist := writeTempArtifact(t, "dist-bytes")

	res, err := NewClient().Deploy(repo, c, main, []byte("<project/>"), deployTime,
		Attachment{File: sources, Classifier: "sources", Type: "jar"},
		Attachment{File: dist, Classifier: "dist", Type: "tar.gz"})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	base := "g/multi/1.0-SNAPSHOT/multi-1.0-20260721.093012-1"
	for _, want := range []string{base + ".jar", base + "-sources.jar", base + "-dist.tar.gz", base + ".pom"} {
		if _, ok := f.get(want); !ok {
			t.Errorf("missing %s", want)
		}
	}
	if len(res.Uploaded) != 6 {
		t.Errorf("Uploaded = %v", res.Uploaded)
	}
	m := fixtureMetadata(t, f, "g/multi/1.0-SNAPSHOT/maven-metadata.xml")
	if n := len(m.Versioning.SnapshotVersions.SnapshotVersion); n != 4 {
		t.Fatalf("snapshotVersions entries = %d, want 4 (jar, pom, sources, dist)", n)
	}
	for _, sv := range m.Versioning.SnapshotVersions.SnapshotVersion {
		if sv.Value != "1.0-20260721.093012-1" {
			t.Errorf("entry %s/%s = %q (all must share one build in one metadata update)", sv.Classifier, sv.Extension, sv.Value)
		}
	}
}

func TestDeployAttachmentNeedsClassifier(t *testing.T) {
	_, repo := newFixture(t, "", "")
	c := Coords{GroupID: "g", ArtifactID: "a", Version: "1.0.0", Type: "jar"}
	f := writeTempArtifact(t, "x")
	if _, err := NewClient().Deploy(repo, c, f, nil, deployTime, Attachment{File: f, Type: "jar"}); err == nil {
		t.Error("classifier-less same-type attachment must be rejected (would overwrite main artifact)")
	}
}

// TestDeployMetadataAfterFiles asserts the protocol's ordering constraint
// survives concurrent uploads: metadata documents are only written after
// every file they reference has landed.
func TestDeployMetadataAfterFiles(t *testing.T) {
	f, repo := newFixture(t, "", "")
	c := Coords{GroupID: "g", ArtifactID: "ord", Version: "1.0-SNAPSHOT", Type: "jar"}
	if _, err := NewClient().Deploy(repo, c, writeTempArtifact(t, "x"), []byte("<project/>"), deployTime); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	f.mu.RLock()
	order := append([]string{}, f.putOrder...)
	f.mu.RUnlock()
	idx := func(suffix string) int {
		for i, p := range order {
			if strings.HasSuffix(p, suffix) {
				return i
			}
		}
		t.Fatalf("no PUT ending in %q in %v", suffix, order)
		return -1
	}
	jar := idx("-1.jar")
	pom := idx("-1.pom")
	versionMeta := idx("1.0-SNAPSHOT/maven-metadata.xml")
	artifactMeta := idx("ord/maven-metadata.xml")
	if versionMeta < jar || versionMeta < pom || artifactMeta < jar || artifactMeta < pom {
		t.Errorf("metadata written before files: order = %v", order)
	}
}

func TestCopyPromotesRelease(t *testing.T) {
	src, from := newFixture(t, "", "")
	dst, to := newFixture(t, "", "")
	src.put("g/lib/1.0.0/lib-1.0.0.jar", []byte("promote-me"))
	src.put("g/lib/1.0.0/lib-1.0.0.pom", []byte("<project/>"))
	c := Coords{GroupID: "g", ArtifactID: "lib", Version: "1.0.0", Type: "jar"}

	res, err := NewClient().Copy(from, to, c, deployTime)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	jar, ok := dst.get("g/lib/1.0.0/lib-1.0.0.jar")
	if !ok || string(jar) != "promote-me" {
		t.Errorf("target jar = %q ok=%v", jar, ok)
	}
	pom, ok := dst.get("g/lib/1.0.0/lib-1.0.0.pom")
	if !ok || string(pom) != "<project/>" {
		t.Errorf("target pom = %q ok=%v (source POM must be copied, not regenerated)", pom, ok)
	}
	if _, ok := dst.get("g/lib/1.0.0/lib-1.0.0.jar.sha256"); !ok {
		t.Error("target checksum sidecars missing")
	}
	m := fixtureMetadata(t, dst, "g/lib/maven-metadata.xml")
	if m.Versioning.Release != "1.0.0" {
		t.Errorf("target metadata = %+v", m.Versioning)
	}
	if len(res.Uploaded) != 3 {
		t.Errorf("Uploaded = %v, want jar+pom+artifact-metadata", res.Uploaded)
	}
}

func TestCopyRefusesSnapshotAndBrokenSource(t *testing.T) {
	src, from := newFixture(t, "", "")
	_, to := newFixture(t, "", "")
	cl := NewClient()
	if _, err := cl.Copy(from, to, Coords{GroupID: "g", ArtifactID: "a", Version: "1.0-SNAPSHOT", Type: "jar"}, deployTime); err == nil {
		t.Error("snapshot copy must be refused")
	}
	src.put("g/nopom/1.0/nopom-1.0.jar", []byte("x"))
	if _, err := cl.Copy(from, to, Coords{GroupID: "g", ArtifactID: "nopom", Version: "1.0", Type: "jar"}, deployTime); err == nil {
		t.Error("missing source POM must be refused")
	}
}

func TestDeployAuthRequired(t *testing.T) {
	_, repo := newFixture(t, "deployer", "hunter2")
	repo.Password = "wrong"
	c := Coords{GroupID: "g", ArtifactID: "a", Version: "1.0.0", Type: "jar"}
	if _, err := NewClient().Deploy(repo, c, writeTempArtifact(t, "x"), nil, deployTime); err == nil {
		t.Error("expected auth failure")
	}
}

func TestMarshalMetadataFormat(t *testing.T) {
	m := &Metadata{ModelVersion: "1.1.0", GroupID: "g", ArtifactID: "a", Version: "1.0-SNAPSHOT",
		Versioning: Versioning{
			Snapshot:    &Snapshot{Timestamp: "20260721.093012", BuildNumber: 1},
			LastUpdated: "20260721093012",
			SnapshotVersions: &SnapshotVersionList{SnapshotVersion: []SnapshotVersion{
				{Extension: "jar", Value: "1.0-20260721.093012-1", Updated: "20260721093012"}}},
		}}
	data, err := MarshalMetadata(m)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		`<metadata modelVersion="1.1.0">`,
		"<snapshot>",
		"<timestamp>20260721.093012</timestamp>",
		"<buildNumber>1</buildNumber>",
		"<lastUpdated>20260721093012</lastUpdated>",
		"<snapshotVersion>",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("marshaled metadata missing %q:\n%s", want, out)
		}
	}
	back, err := ParseMetadata(bytes.NewReader(data))
	if err != nil || back.Versioning.Snapshot.BuildNumber != 1 {
		t.Errorf("round trip failed: %v %+v", err, back)
	}
}
