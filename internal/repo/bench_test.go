package repo

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newBenchServer starts the fixture HTTP server for a benchmark.
func newBenchServer(b *testing.B, f *fixtureServer) string {
	srv := httptest.NewServer(f.handler())
	b.Cleanup(srv.Close)
	return srv.URL
}

// benchFixture builds a fixture repository and client outside the timed loop.
// Note: the httptest server runs in-process, so allocs/op includes server-side
// handler allocations; comparisons before/after an optimization remain valid.
func benchFixture(b *testing.B, artifactSize int) (*fixtureServer, RemoteRepo, []byte) {
	b.Helper()
	f := &fixtureServer{files: map[string][]byte{}}
	srv := newBenchServer(b, f)
	data := bytes.Repeat([]byte{0xa5}, artifactSize)
	f.put("g/a/1.0/a-1.0.jar", data)
	return f, RemoteRepo{ID: "bench", URL: srv, Releases: true, Snapshots: true}, data
}

func BenchmarkDownload4MB(b *testing.B) {
	_, repo, data := benchFixture(b, 4<<20)
	cl := NewClient()
	dest := filepath.Join(b.TempDir(), "a.jar")
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := cl.Download(repo, "g/a/1.0/a-1.0.jar", dest); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDownload4MBNoChecksums(b *testing.B) {
	f, repo, data := benchFixture(b, 4<<20)
	delete(f.files, "g/a/1.0/a-1.0.jar.sha1")
	delete(f.files, "g/a/1.0/a-1.0.jar.sha256")
	cl := NewClient()
	dest := filepath.Join(b.TempDir(), "a.jar")
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := cl.Download(repo, "g/a/1.0/a-1.0.jar", dest); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPutFile4MB(b *testing.B) {
	_, repo, _ := benchFixture(b, 1)
	cl := NewClient()
	local := filepath.Join(b.TempDir(), "up.jar")
	if err := os.WriteFile(local, bytes.Repeat([]byte{0x5a}, 4<<20), 0o644); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(4 << 20)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := cl.PutFile(repo, "g/a/2.0/a-2.0.jar", local); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDeploySnapshot64KB(b *testing.B) {
	_, repo, _ := benchFixture(b, 1)
	cl := NewClient()
	local := filepath.Join(b.TempDir(), "up.jar")
	if err := os.WriteFile(local, bytes.Repeat([]byte{0x5a}, 64<<10), 0o644); err != nil {
		b.Fatal(err)
	}
	c := Coords{GroupID: "g", ArtifactID: "dep", Version: "1.0-SNAPSHOT", Type: "jar"}
	when := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		if _, err := cl.Deploy(repo, c, local, []byte("<project/>"), when.Add(time.Duration(i)*time.Second)); err != nil {
			b.Fatal(err)
		}
		i++
	}
}

func BenchmarkGetBytes64KB(b *testing.B) {
	f, repo, _ := benchFixture(b, 1)
	f.files["meta.xml"] = bytes.Repeat([]byte{'x'}, 64<<10)
	cl := NewClient()
	b.SetBytes(64 << 10)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := cl.GetBytes(repo, "meta.xml"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCoordsPaths(b *testing.B) {
	c := Coords{GroupID: "com.example.deeply.nested.group", ArtifactID: "artifact-name", Version: "2.1.0-SNAPSHOT", Type: "jar", Classifier: "sources"}
	b.ReportAllocs()
	for b.Loop() {
		_ = c.ArtifactPath("2.1.0-20260721.000000-1")
		_ = c.VersionMetadataPath()
		_ = c.ArtifactMetadataPath()
	}
}

func BenchmarkParseCoords(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ParseCoords("com.example:artifact:2.1.0-SNAPSHOT:jar:sources"); err != nil {
			b.Fatal(err)
		}
	}
}

// sink prevents dead-code elimination in micro benchmarks.
var sink string

func BenchmarkRemoteRepoString(b *testing.B) {
	r := RemoteRepo{ID: "nexus", URL: "https://nexus.corp/repo", Username: "u", MirrorID: "m", Releases: true}
	b.ReportAllocs()
	for b.Loop() {
		sink = r.String()
	}
}
