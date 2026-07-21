package repo

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Transfer benchmarks run against an out-of-process fixture server
// (internal/benchserver) so ns/op and allocs/op measure only the client.
// Set GOVEN_BENCH_URL to benchmark against an already-running server (for
// example a real Nexus) instead of the spawned one.

var (
	benchBinOnce sync.Once
	benchBin     string
	benchBinErr  error
)

// benchServerBinary builds the benchserver binary once per test process.
func benchServerBinary() (string, error) {
	benchBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "goven-benchserver-*")
		if err != nil {
			benchBinErr = err
			return
		}
		benchBin = filepath.Join(dir, "benchserver")
		out, err := exec.Command("go", "build", "-o", benchBin,
			"github.com/freeeve/goven/internal/benchserver").CombinedOutput()
		if err != nil {
			benchBinErr = fmt.Errorf("build benchserver: %v\n%s", err, out)
		}
	})
	return benchBin, benchBinErr
}

// externalRepo starts (or reuses via GOVEN_BENCH_URL) an out-of-process
// fixture server and returns it as a repository.
func externalRepo(b *testing.B) RemoteRepo {
	b.Helper()
	repo := RemoteRepo{ID: "bench", Releases: true, Snapshots: true}
	if url := os.Getenv("GOVEN_BENCH_URL"); url != "" {
		repo.URL = strings.TrimRight(url, "/")
		return repo
	}
	bin, err := benchServerBinary()
	if err != nil {
		b.Fatal(err)
	}
	cmd := exec.Command(bin)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		b.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	addrCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if addr, ok := strings.CutPrefix(sc.Text(), "LISTEN "); ok {
				addrCh <- addr
				return
			}
		}
		// EOF or read error both mean the server died before announcing.
		_ = sc.Err()
		addrCh <- ""
	}()
	select {
	case addr := <-addrCh:
		if addr == "" {
			b.Fatal("benchserver exited without announcing an address")
		}
		repo.URL = "http://" + addr
	case <-time.After(10 * time.Second):
		b.Fatal("benchserver did not start within 10s")
	}
	return repo
}

// seedRaw uploads a file to the fixture server without checksum sidecars.
func seedRaw(b *testing.B, cl *Client, repo RemoteRepo, path string, data []byte) {
	b.Helper()
	err := cl.put(repo, path, func() (io.ReadCloser, int64) {
		return io.NopCloser(bytes.NewReader(data)), int64(len(data))
	})
	if err != nil {
		b.Fatal(err)
	}
}

func BenchmarkDownload4MB(b *testing.B) {
	repo := externalRepo(b)
	cl := NewClient()
	data := bytes.Repeat([]byte{0xa5}, 4<<20)
	if err := cl.PutBytes(repo, "g/a/1.0/a-1.0.jar", data); err != nil {
		b.Fatal(err)
	}
	dest := filepath.Join(b.TempDir(), "a.jar")
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if err := cl.Download(repo, "g/a/1.0/a-1.0.jar", dest); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDownload4MBNoChecksums(b *testing.B) {
	repo := externalRepo(b)
	cl := NewClient()
	data := bytes.Repeat([]byte{0xa5}, 4<<20)
	seedRaw(b, cl, repo, "g/a/1.0/a-1.0.jar", data)
	dest := filepath.Join(b.TempDir(), "a.jar")
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if err := cl.Download(repo, "g/a/1.0/a-1.0.jar", dest); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPutFile4MB(b *testing.B) {
	repo := externalRepo(b)
	cl := NewClient()
	local := filepath.Join(b.TempDir(), "up.jar")
	if err := os.WriteFile(local, bytes.Repeat([]byte{0x5a}, 4<<20), 0o644); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(4 << 20)
	b.ReportAllocs()
	for b.Loop() {
		if err := cl.PutFile(repo, "g/a/2.0/a-2.0.jar", local); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDeploySnapshot64KB(b *testing.B) {
	repo := externalRepo(b)
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
	repo := externalRepo(b)
	cl := NewClient()
	seedRaw(b, cl, repo, "meta.xml", bytes.Repeat([]byte{'x'}, 64<<10))
	b.SetBytes(64 << 10)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := cl.GetBytes(repo, "meta.xml"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetBytesInto64KB(b *testing.B) {
	repo := externalRepo(b)
	cl := NewClient()
	seedRaw(b, cl, repo, "meta.xml", bytes.Repeat([]byte{'x'}, 64<<10))
	var buf []byte
	b.SetBytes(64 << 10)
	b.ReportAllocs()
	for b.Loop() {
		var err error
		buf, err = cl.GetBytesInto(repo, "meta.xml", buf[:0])
		if err != nil {
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
