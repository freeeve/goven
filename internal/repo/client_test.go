package repo

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fixtureServer is an in-memory maven2-layout repository for tests, serving
// GETs and accepting PUTs like a hosted Nexus repository. putOrder records
// the sequence of PUT paths for upload-ordering assertions.
type fixtureServer struct {
	mu       sync.RWMutex
	files    map[string][]byte
	putOrder []string
	user     string
	pass     string
}

// put stores a file plus its sha1 and sha256 checksum sidecars.
func (f *fixtureServer) put(path string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = data
	s1 := sha1.Sum(data)
	s256 := sha256.Sum256(data)
	f.files[path+".sha1"] = []byte(hex.EncodeToString(s1[:]))
	f.files[path+".sha256"] = []byte(hex.EncodeToString(s256[:]))
}

// get reads a stored file under the read lock.
func (f *fixtureServer) get(path string) ([]byte, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	data, ok := f.files[path]
	return data, ok
}

func (f *fixtureServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.user != "" {
			u, p, ok := r.BasicAuth()
			if !ok || u != f.user || p != f.pass {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		path := r.URL.Path[1:]
		if r.Method == http.MethodPut {
			// Preallocate from Content-Length so server-side allocations do
			// not swamp client-side numbers in benchmarks.
			var buf bytes.Buffer
			if r.ContentLength > 0 {
				buf.Grow(int(r.ContentLength))
			}
			if _, err := io.Copy(&buf, r.Body); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			f.mu.Lock()
			f.files[path] = buf.Bytes()
			f.putOrder = append(f.putOrder, path)
			f.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			return
		}
		data, ok := f.get(path)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(data)
	})
}

func newFixture(t *testing.T, user, pass string) (*fixtureServer, RemoteRepo) {
	t.Helper()
	f := &fixtureServer{files: map[string][]byte{}, user: user, pass: pass}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return f, RemoteRepo{ID: "fixture", URL: srv.URL, Releases: true, Snapshots: true, Username: user, Password: pass}
}

func TestDownloadVerifiesChecksums(t *testing.T) {
	f, repo := newFixture(t, "", "")
	f.put("g/a/1.0/a-1.0.jar", []byte("artifact-bytes"))

	dest := filepath.Join(t.TempDir(), "a.jar")
	if err := NewClient().Download(repo, "g/a/1.0/a-1.0.jar", dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "artifact-bytes" {
		t.Errorf("content = %q", got)
	}
}

func TestDownloadChecksumMismatch(t *testing.T) {
	f, repo := newFixture(t, "", "")
	f.put("g/a/1.0/a-1.0.jar", []byte("artifact-bytes"))
	f.files["g/a/1.0/a-1.0.jar.sha256"] = []byte("deadbeef")

	dest := filepath.Join(t.TempDir(), "a.jar")
	err := NewClient().Download(repo, "g/a/1.0/a-1.0.jar", dest)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Error("dest file should not exist after failed verification")
	}
}

func TestDownloadWithoutChecksumsSucceeds(t *testing.T) {
	f, repo := newFixture(t, "", "")
	f.files["g/a/1.0/a-1.0.jar"] = []byte("no sidecars")

	dest := filepath.Join(t.TempDir(), "a.jar")
	if err := NewClient().Download(repo, "g/a/1.0/a-1.0.jar", dest); err != nil {
		t.Fatalf("Download without sidecars: %v", err)
	}
}

func TestDownloadAuth(t *testing.T) {
	f, repo := newFixture(t, "deployer", "hunter2")
	f.put("g/a/1.0/a-1.0.jar", []byte("secret"))

	dest := filepath.Join(t.TempDir(), "a.jar")
	if err := NewClient().Download(repo, "g/a/1.0/a-1.0.jar", dest); err != nil {
		t.Fatalf("authenticated download: %v", err)
	}
	bad := repo
	bad.Password = "wrong"
	if err := NewClient().Download(bad, "g/a/1.0/a-1.0.jar", filepath.Join(t.TempDir(), "b.jar")); err == nil {
		t.Error("expected 401 error with wrong password")
	}
}

func TestCustomHeadersSent(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("Authorization"))
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	repo := RemoteRepo{ID: "t", URL: srv.URL, Releases: true,
		Headers: map[string]string{"Authorization": "Bearer tok123"}}
	if _, err := NewClient().GetBytes(repo, "any"); err != nil {
		t.Fatal(err)
	}
	if got.Load() != "Bearer tok123" {
		t.Errorf("Authorization header = %q", got.Load())
	}
}

func TestNotFound(t *testing.T) {
	_, repo := newFixture(t, "", "")
	err := NewClient().Download(repo, "g/a/1.0/missing.jar", filepath.Join(t.TempDir(), "x.jar"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestRetriesOn500(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte("eventually"))
	}))
	defer srv.Close()
	repo := RemoteRepo{ID: "flaky", URL: srv.URL, Releases: true}

	data, err := NewClient().GetBytes(repo, "any/path")
	if err != nil {
		t.Fatalf("GetBytes after retries: %v", err)
	}
	if string(data) != "eventually" || calls.Load() != 3 {
		t.Errorf("data=%q calls=%d", data, calls.Load())
	}
}

func TestResolveVersionSnapshot(t *testing.T) {
	f, repo := newFixture(t, "", "")
	f.put("com/example/lib/2.1.0-SNAPSHOT/maven-metadata.xml", []byte(versionMetadata))
	c := Coords{GroupID: "com.example", ArtifactID: "lib", Version: "2.1.0-SNAPSHOT", Type: "jar"}

	v, err := NewClient().ResolveVersion(repo, c)
	if err != nil {
		t.Fatalf("ResolveVersion: %v", err)
	}
	if v != "2.1.0-20260720.093012-3" {
		t.Errorf("resolved = %q", v)
	}
}

func TestResolveVersionSnapshotNoMetadata(t *testing.T) {
	_, repo := newFixture(t, "", "")
	c := Coords{GroupID: "g", ArtifactID: "a", Version: "1.0-SNAPSHOT", Type: "jar"}
	v, err := NewClient().ResolveVersion(repo, c)
	if err != nil || v != "1.0-SNAPSHOT" {
		t.Errorf("resolved = %q, err = %v; want literal fallback", v, err)
	}
}

func TestFetchArtifactRepoFallback(t *testing.T) {
	_, empty := newFixture(t, "", "")
	f2, second := newFixture(t, "", "")
	f2.put("g/a/1.0/a-1.0.jar", []byte("from-second"))

	dest := filepath.Join(t.TempDir(), "a.jar")
	c := Coords{GroupID: "g", ArtifactID: "a", Version: "1.0", Type: "jar"}
	used, resolved, err := NewClient().FetchArtifact(c, []RemoteRepo{empty, second}, dest)
	if err != nil {
		t.Fatalf("FetchArtifact: %v", err)
	}
	if used.ID != second.ID || resolved != "1.0" {
		t.Errorf("used=%s resolved=%s", used.ID, resolved)
	}
}

func TestFetchArtifactPolicyFiltering(t *testing.T) {
	f, repo := newFixture(t, "", "")
	f.put("g/a/1.0-SNAPSHOT/a-1.0-SNAPSHOT.jar", []byte("snap"))
	repo.Snapshots = false

	c := Coords{GroupID: "g", ArtifactID: "a", Version: "1.0-SNAPSHOT", Type: "jar"}
	_, _, err := NewClient().FetchArtifact(c, []RemoteRepo{repo}, filepath.Join(t.TempDir(), "a.jar"))
	if err == nil {
		t.Error("snapshot fetch must fail when no repo allows snapshots")
	}
}

func TestGetBytesIntoReusesCapacity(t *testing.T) {
	f, repo := newFixture(t, "", "")
	f.files["meta.xml"] = bytes.Repeat([]byte{'x'}, 1024)
	cl := NewClient()

	buf := make([]byte, 0, 4096)
	got, err := cl.GetBytesInto(repo, "meta.xml", buf)
	if err != nil {
		t.Fatalf("GetBytesInto: %v", err)
	}
	if len(got) != 1024 || &got[0] != &buf[:1][0] {
		t.Errorf("len=%d; buffer with sufficient capacity must be reused, not reallocated", len(got))
	}

	small := make([]byte, 0, 8)
	got, err = cl.GetBytesInto(repo, "meta.xml", small)
	if err != nil || len(got) != 1024 {
		t.Errorf("undersized buffer: len=%d err=%v (must grow)", len(got), err)
	}

	got, err = cl.GetBytesInto(repo, "meta.xml", nil)
	if err != nil || len(got) != 1024 {
		t.Errorf("nil buffer: len=%d err=%v", len(got), err)
	}
}

// TestConnectionReuse locks in the transport-reuse behavior the client
// relies on: Content-Length bodies fully consumed by GetBytes reuse their
// connection without an explicit EOF read, and error responses with unread
// bodies must be drained (drainAndClose) rather than closed, or the
// connection is torn down.
func TestConnectionReuse(t *testing.T) {
	var conns atomic.Int32
	body := bytes.Repeat([]byte{'z'}, 2048)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "missing") {
			// Non-empty 404 body, like a real repository manager's error page.
			http.Error(w, "<html>not found, with a body worth draining</html>", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", "2048")
		w.Write(body)
	}))
	srv.Config.ConnState = func(c net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	repo := RemoteRepo{ID: "reuse", URL: srv.URL, Releases: true}

	cl := NewClient()
	for range 5 {
		if _, err := cl.GetBytes(repo, "some/file"); err != nil {
			t.Fatal(err)
		}
	}
	for range 5 {
		if _, err := cl.GetBytes(repo, "some/missing"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	}
	if got := conns.Load(); got != 1 {
		t.Errorf("server saw %d connections for 10 sequential requests, want 1 (keep-alive broken)", got)
	}
}

func TestGetBytesIntoUnknownLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Transfer-Encoding", "chunked")
		w.(http.Flusher).Flush()
		w.Write(bytes.Repeat([]byte{'y'}, 3000))
	}))
	defer srv.Close()
	repo := RemoteRepo{ID: "chunked", URL: srv.URL, Releases: true}

	got, err := NewClient().GetBytesInto(repo, "any", make([]byte, 0, 16))
	if err != nil {
		t.Fatalf("GetBytesInto chunked: %v", err)
	}
	if len(got) != 3000 {
		t.Errorf("len = %d, want 3000", len(got))
	}
}

func TestExists(t *testing.T) {
	f, repo := newFixture(t, "", "")
	f.put("g/a/1.0/a-1.0.jar", []byte("x"))
	f.put("com/example/lib/2.1.0-SNAPSHOT/maven-metadata.xml", []byte(versionMetadata))
	f.put("com/example/lib/2.1.0-SNAPSHOT/lib-2.1.0-20260720.093012-3.jar", []byte("snap"))
	cl := NewClient()

	found, resolved, err := cl.Exists(repo, Coords{GroupID: "g", ArtifactID: "a", Version: "1.0", Type: "jar"})
	if err != nil || !found || resolved != "1.0" {
		t.Errorf("release: found=%v resolved=%q err=%v", found, resolved, err)
	}
	found, resolved, err = cl.Exists(repo, Coords{GroupID: "com.example", ArtifactID: "lib", Version: "2.1.0-SNAPSHOT", Type: "jar"})
	if err != nil || !found || resolved != "2.1.0-20260720.093012-3" {
		t.Errorf("snapshot: found=%v resolved=%q err=%v", found, resolved, err)
	}
	found, _, err = cl.Exists(repo, Coords{GroupID: "g", ArtifactID: "a", Version: "9.9", Type: "jar"})
	if err != nil || found {
		t.Errorf("absent: found=%v err=%v", found, err)
	}
}

func TestFetchArtifactMetadata(t *testing.T) {
	f, repo := newFixture(t, "", "")
	f.put("com/example/lib/maven-metadata.xml", []byte(artifactMetadata))
	m, err := NewClient().FetchArtifactMetadata(repo, "com.example", "lib")
	if err != nil {
		t.Fatal(err)
	}
	if m.Versioning.Release != "2.0.0" {
		t.Errorf("release = %q", m.Versioning.Release)
	}
	if _, err := NewClient().FetchArtifactMetadata(repo, "no", "pe"); !errors.Is(err, ErrNotFound) {
		t.Errorf("absent metadata err = %v", err)
	}
}

func TestParseChecksum(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ABCDEF0123", "abcdef0123"},
		{"abc123  lib-1.0.jar\n", "abc123"},
		{"  abc123\n", "abc123"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := parseChecksum(tc.in); got != tc.want {
			t.Errorf("parseChecksum(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
