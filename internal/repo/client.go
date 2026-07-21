package repo

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrNotFound reports a 404 from the remote repository, letting callers fall
// through to the next repository in the effective list.
var ErrNotFound = errors.New("not found")

// Client transfers files to and from Maven repositories with authentication,
// proxy support, retries, and checksum verification. It is safe for
// concurrent use.
type Client struct {
	UserAgent string
	Retries   int // total attempts per request (default 3)
	mu        sync.Mutex
	transport map[string]*http.Client
}

// NewClient returns a Client with default retry policy and timeouts.
func NewClient() *Client {
	return &Client{
		UserAgent: "goven/" + strings.TrimPrefix(Version, "v"),
		Retries:   3,
		transport: map[string]*http.Client{},
	}
}

// Version is the goven version, stamped by the main package for User-Agent.
var Version = "dev"

// httpClient returns (building on demand) an HTTP client honoring the
// repository's proxy configuration.
func (cl *Client) httpClient(repo RemoteRepo) *http.Client {
	key := ""
	if repo.Proxy != nil {
		key = fmt.Sprintf("%s:%d", repo.Proxy.Host, repo.Proxy.Port)
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if c, ok := cl.transport[key]; ok {
		return c
	}
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	if p := repo.Proxy; p != nil {
		u := &url.URL{Scheme: "http", Host: fmt.Sprintf("%s:%d", p.Host, p.Port)}
		if p.Username != "" {
			u.User = url.UserPassword(p.Username, p.Password)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	c := &http.Client{Transport: tr}
	cl.transport[key] = c
	return c
}

// do issues one request with auth and retries; retryable failures are network
// errors, 5xx, and 429. The caller owns the response body on success.
func (cl *Client) do(repo RemoteRepo, method, path string, body func() (io.ReadCloser, int64)) (*http.Response, error) {
	target := repo.URL + "/" + path
	attempts := max(cl.Retries, 1)
	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 250 * time.Millisecond)
		}
		var reqBody io.ReadCloser
		var length int64
		if body != nil {
			reqBody, length = body()
		}
		req, err := http.NewRequest(method, target, reqBody)
		if err != nil {
			return nil, err
		}
		req.ContentLength = length
		req.Header.Set("User-Agent", cl.UserAgent)
		if repo.Username != "" {
			req.SetBasicAuth(repo.Username, repo.Password)
		}
		resp, err := cl.httpClient(repo).Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		switch {
		case resp.StatusCode == http.StatusNotFound:
			resp.Body.Close()
			return nil, fmt.Errorf("%s: %w", target, ErrNotFound)
		case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
			resp.Body.Close()
			lastErr = fmt.Errorf("%s: HTTP %d", target, resp.StatusCode)
			continue
		case resp.StatusCode >= 400:
			resp.Body.Close()
			return nil, fmt.Errorf("%s: HTTP %d", target, resp.StatusCode)
		}
		return resp, nil
	}
	return nil, fmt.Errorf("after %d attempts: %w", attempts, lastErr)
}

// maxMetaBytes bounds in-memory fetches of metadata and checksum files.
const maxMetaBytes = 16 << 20

// metaBufPool holds reusable buffers for goven's internal metadata and
// checksum-sidecar fetches, so repeated repository operations settle at zero
// buffer allocations.
var metaBufPool = sync.Pool{New: func() any {
	b := make([]byte, 0, 32<<10)
	return &b
}}

// GetBytes fetches a small repository file (metadata, checksums) into a
// freshly allocated buffer.
func (cl *Client) GetBytes(repo RemoteRepo, path string) ([]byte, error) {
	return cl.GetBytesInto(repo, path, nil)
}

// GetBytesInto fetches a small repository file into buf, reusing its capacity
// when sufficient (buf may be nil). It returns the possibly regrown buffer,
// sized from Content-Length when the server provides one; content beyond
// maxMetaBytes is truncated, matching the previous LimitReader behavior.
func (cl *Client) GetBytesInto(repo RemoteRepo, path string, buf []byte) ([]byte, error) {
	resp, err := cl.do(repo, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if n := resp.ContentLength; n > 0 && n <= maxMetaBytes {
		if int64(cap(buf)) < n {
			buf = make([]byte, n)
		} else {
			buf = buf[:n]
		}
		if _, err := io.ReadFull(resp.Body, buf); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		return buf, nil
	}
	buf = buf[:0]
	for int64(len(buf)) < maxMetaBytes {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)]
		}
		n, err := resp.Body.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}
	return buf[:maxMetaBytes], nil
}

// checksumAlgos lists remote checksum sidecars in verification preference
// order alongside their hash constructors. md5 is verified only as a last
// resort but is still published for repositories that require it.
var checksumAlgos = []struct {
	ext string
	new func() hash.Hash
}{
	{"sha256", sha256.New},
	{"sha1", sha1.New},
	{"md5", md5.New},
}

// sidecarResult is the outcome of checksum-sidecar discovery: the index into
// checksumAlgos (or -1 when the repository publishes none) and the expected
// digest.
type sidecarResult struct {
	algo int
	want string
	err  error
}

// Download streams a repository file to destFile and verifies it against the
// strongest checksum sidecar published next to it. Sidecar discovery runs
// concurrently with the body transfer, and the file is hashed once with only
// the algorithm that was actually found. A missing sidecar set is tolerated
// (Maven's default "warn" policy); a mismatch is an error and the partial
// file is removed. The download lands in a temp file renamed into place so
// destFile is never left truncated.
func (cl *Client) Download(repo RemoteRepo, path, destFile string) error {
	resp, err := cl.do(repo, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	sidecar := make(chan sidecarResult, 1)
	go func() { sidecar <- cl.findChecksum(repo, path) }()

	if err := os.MkdirAll(filepath.Dir(destFile), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destFile), ".goven-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := copyPooled(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("download %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	sc := <-sidecar
	if sc.err != nil {
		return sc.err
	}
	if sc.algo >= 0 {
		got, err := hashFile(tmp.Name(), checksumAlgos[sc.algo].new())
		if err != nil {
			return err
		}
		if !strings.EqualFold(sc.want, got) {
			return fmt.Errorf("%s checksum mismatch for %s: remote %s, computed %s",
				checksumAlgos[sc.algo].ext, path, sc.want, got)
		}
	}
	return os.Rename(tmp.Name(), destFile)
}

// findChecksum locates the strongest checksum sidecar the repository
// publishes for path. Absent sidecars are skipped; transport errors abort.
func (cl *Client) findChecksum(repo RemoteRepo, path string) sidecarResult {
	bp := metaBufPool.Get().(*[]byte)
	defer metaBufPool.Put(bp)
	for i, a := range checksumAlgos {
		remote, err := cl.GetBytesInto(repo, path+"."+a.ext, (*bp)[:0])
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return sidecarResult{err: fmt.Errorf("fetch %s checksum: %w", a.ext, err)}
		}
		*bp = remote
		return sidecarResult{algo: i, want: parseChecksum(string(remote))}
	}
	return sidecarResult{algo: -1}
}

// hashFile computes the hex digest of a file with one streaming pass through
// a pooled buffer.
func hashFile(path string, h hash.Hash) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := copyPooled(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// parseChecksum extracts the hex digest from a checksum file, which may carry
// a trailing filename ("<hex>  <name>") depending on the producing tool.
func parseChecksum(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

// ResolveVersion determines the concrete version string for the coordinate in
// one repository: the version itself for releases, or the timestamped build
// from version-level metadata for SNAPSHOTs (falling back to the literal
// -SNAPSHOT name when no metadata is published).
func (cl *Client) ResolveVersion(repo RemoteRepo, c Coords) (string, error) {
	if !c.IsSnapshot() {
		return c.Version, nil
	}
	bp := metaBufPool.Get().(*[]byte)
	defer metaBufPool.Put(bp)
	raw, err := cl.GetBytesInto(repo, c.VersionMetadataPath(), (*bp)[:0])
	if errors.Is(err, ErrNotFound) {
		return c.Version, nil
	}
	if err != nil {
		return "", err
	}
	*bp = raw
	m, err := ParseMetadata(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("%s: %w", c.VersionMetadataPath(), err)
	}
	return m.ResolveSnapshotVersion(c), nil
}

// FetchArtifact downloads the artifact from the first repository in repos
// that has it, honoring each repository's release/snapshot policy. It returns
// the repository used and the resolved version.
func (cl *Client) FetchArtifact(c Coords, repos []RemoteRepo, destFile string) (RemoteRepo, string, error) {
	var errs []error
	for _, repo := range repos {
		if c.IsSnapshot() && !repo.Snapshots {
			continue
		}
		if !c.IsSnapshot() && !repo.Releases {
			continue
		}
		resolved, err := cl.ResolveVersion(repo, c)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", repo.ID, err))
			continue
		}
		err = cl.Download(repo, c.ArtifactPath(resolved), destFile)
		if err == nil {
			return repo, resolved, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", repo.ID, err))
		if !errors.Is(err, ErrNotFound) {
			break
		}
	}
	if len(errs) == 0 {
		return RemoteRepo{}, "", fmt.Errorf("%s: no repository in settings allows %s artifacts",
			c, map[bool]string{true: "snapshot", false: "release"}[c.IsSnapshot()])
	}
	return RemoteRepo{}, "", fmt.Errorf("%s: %w", c, errors.Join(errs...))
}
