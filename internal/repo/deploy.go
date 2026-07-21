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
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// timestampFormat and lastUpdatedFormat are Maven's UTC layouts for SNAPSHOT
// build timestamps and metadata lastUpdated stamps.
const (
	timestampFormat   = "20060102.150405"
	lastUpdatedFormat = "20060102150405"
)

// DeployResult reports what a deploy uploaded.
type DeployResult struct {
	ResolvedVersion string   // timestamped version for SNAPSHOTs
	Uploaded        []string // repository-relative paths, checksums excluded
}

// taskGroup runs functions concurrently and reports the first error, like
// errgroup without the dependency. With seq set it runs everything inline,
// preserving submission order for debugging and deterministic tests.
type taskGroup struct {
	seq bool
	wg  sync.WaitGroup
	mu  sync.Mutex
	err error
}

// group returns a taskGroup honoring the client's Sequential setting.
func (cl *Client) group() *taskGroup { return &taskGroup{seq: cl.Sequential} }

// Go runs f, on its own goroutine unless the group is sequential, recording
// the first error.
func (g *taskGroup) Go(f func() error) {
	if g.seq {
		if err := f(); err != nil && g.err == nil {
			g.err = err
		}
		return
	}
	g.wg.Go(func() {
		if err := f(); err != nil {
			g.mu.Lock()
			if g.err == nil {
				g.err = err
			}
			g.mu.Unlock()
		}
	})
}

// Wait blocks until every function returns and yields the first error.
func (g *taskGroup) Wait() error {
	g.wg.Wait()
	return g.err
}

// publishAlgos is the checksum sidecar set goven publishes on upload: Maven's
// required md5/sha1 pair plus sha256 for modern verifiers.
var publishAlgos = []struct {
	ext string
	new func() hash.Hash
}{
	{"md5", md5.New},
	{"sha1", sha1.New},
	{"sha256", sha256.New},
}

// putChecksums uploads the checksum sidecars for already-computed digests,
// concurrently unless the client is configured sequential.
func (cl *Client) putChecksums(repo RemoteRepo, path string, hashes []hash.Hash) error {
	putOne := func(i int) func() error {
		sum := []byte(hex.EncodeToString(hashes[i].Sum(nil)))
		sidecarPath := path + "." + publishAlgos[i].ext
		return func() error {
			return cl.put(repo, sidecarPath, func() (io.ReadCloser, int64) {
				return io.NopCloser(bytes.NewReader(sum)), int64(len(sum))
			})
		}
	}
	g := cl.group()
	for i := range publishAlgos {
		g.Go(putOne(i))
	}
	return g.Wait()
}

// newPublishHashes computes all publication digests over one pass of r.
func newPublishHashes(r io.Reader) ([]hash.Hash, error) {
	hashes := make([]hash.Hash, len(publishAlgos))
	writers := make([]io.Writer, len(publishAlgos))
	for i, a := range publishAlgos {
		hashes[i] = a.new()
		writers[i] = hashes[i]
	}
	if _, err := copyPooled(io.MultiWriter(writers...), r); err != nil {
		return nil, err
	}
	return hashes, nil
}

// PutBytes uploads data followed by its checksum sidecars.
func (cl *Client) PutBytes(repo RemoteRepo, path string, data []byte) error {
	if err := cl.put(repo, path, func() (io.ReadCloser, int64) {
		return io.NopCloser(bytes.NewReader(data)), int64(len(data))
	}); err != nil {
		return err
	}
	hashes, err := newPublishHashes(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return cl.putChecksums(repo, path, hashes)
}

// PutFile uploads a local file with checksum sidecars, streaming the content
// rather than holding it in memory.
func (cl *Client) PutFile(repo RemoteRepo, path, localFile string) error {
	f, err := os.Open(localFile)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	size := info.Size()
	hashes, err := newPublishHashes(f)
	f.Close()
	if err != nil {
		return err
	}

	if err := cl.put(repo, path, func() (io.ReadCloser, int64) {
		rc, err := os.Open(localFile)
		if err != nil {
			return io.NopCloser(bytes.NewReader(nil)), 0
		}
		return rc, size
	}); err != nil {
		return err
	}
	return cl.putChecksums(repo, path, hashes)
}

// put issues one PUT and discards the response body.
func (cl *Client) put(repo RemoteRepo, path string, body func() (io.ReadCloser, int64)) error {
	resp, err := cl.do(repo, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Deploy uploads an artifact (and optional POM content) to a repository with
// full Maven metadata semantics: SNAPSHOT versions get a timestamped file
// name with an incremented buildNumber and a merged version-level
// maven-metadata.xml; both flows update the artifact-level version list.
//
// Uploads are concurrent where the protocol allows (unless cl.Sequential):
// the artifact, POM, and artifact-level metadata prefetch run in parallel,
// as do each file's checksum sidecars and the final metadata writes — but
// metadata is only written after every file it references has landed.
// Concurrent deploys of the same GAV race on the metadata read-modify-write,
// exactly as they do with Maven itself.
func (cl *Client) Deploy(repo RemoteRepo, c Coords, artifactFile string, pom []byte, now time.Time) (DeployResult, error) {
	var res DeployResult
	now = now.UTC()
	resolved := c.Version
	var versionMeta *Metadata
	if c.IsSnapshot() {
		var err error
		versionMeta, err = cl.fetchOrInitMetadata(repo, c.VersionMetadataPath(), c, true)
		if err != nil {
			return res, err
		}
		resolved = nextSnapshotVersion(versionMeta, c, now)
	}
	res.ResolvedVersion = resolved

	artifactPath := c.ArtifactPath(resolved)
	pomCoords := Coords{GroupID: c.GroupID, ArtifactID: c.ArtifactID, Version: c.Version, Type: "pom"}
	pomPath := pomCoords.ArtifactPath(resolved)

	var artMeta *Metadata
	files := cl.group()
	files.Go(func() error {
		if err := cl.PutFile(repo, artifactPath, artifactFile); err != nil {
			return fmt.Errorf("upload %s: %w", artifactPath, err)
		}
		return nil
	})
	if pom != nil {
		files.Go(func() error {
			if err := cl.PutBytes(repo, pomPath, pom); err != nil {
				return fmt.Errorf("upload %s: %w", pomPath, err)
			}
			return nil
		})
	}
	files.Go(func() error {
		var err error
		artMeta, err = cl.fetchOrInitMetadata(repo, c.ArtifactMetadataPath(), c, false)
		return err
	})
	if err := files.Wait(); err != nil {
		return res, err
	}
	res.Uploaded = append(res.Uploaded, artifactPath)
	if pom != nil {
		res.Uploaded = append(res.Uploaded, pomPath)
	}

	meta := cl.group()
	if c.IsSnapshot() {
		mergeSnapshotVersions(versionMeta, c, resolved, pom != nil, now)
		versionMeta.Versioning.LastUpdated = now.Format(lastUpdatedFormat)
		meta.Go(func() error { return cl.putMetadata(repo, c.VersionMetadataPath(), versionMeta) })
	}
	updateArtifactMetadata(artMeta, c, now)
	meta.Go(func() error { return cl.putMetadata(repo, c.ArtifactMetadataPath(), artMeta) })
	if err := meta.Wait(); err != nil {
		return res, err
	}
	if c.IsSnapshot() {
		res.Uploaded = append(res.Uploaded, c.VersionMetadataPath())
	}
	res.Uploaded = append(res.Uploaded, c.ArtifactMetadataPath())
	return res, nil
}

// fetchOrInitMetadata reads existing metadata from the repository or starts a
// fresh document for the coordinate. versionLevel selects the SNAPSHOT
// (modelVersion 1.1.0, with <version>) form over the artifact-level form.
func (cl *Client) fetchOrInitMetadata(repo RemoteRepo, path string, c Coords, versionLevel bool) (*Metadata, error) {
	bp := metaBufPool.Get().(*[]byte)
	defer metaBufPool.Put(bp)
	raw, err := cl.GetBytesInto(repo, path, (*bp)[:0])
	if err == nil {
		*bp = raw
		m, perr := ParseMetadata(bytes.NewReader(raw))
		if perr == nil {
			return m, nil
		}
		return nil, fmt.Errorf("existing %s is unparseable: %w", path, perr)
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	m := &Metadata{GroupID: c.GroupID, ArtifactID: c.ArtifactID}
	if versionLevel {
		m.ModelVersion = "1.1.0"
		m.Version = c.Version
	}
	return m, nil
}

// putMetadata serializes and uploads a metadata document with checksums.
func (cl *Client) putMetadata(repo RemoteRepo, path string, m *Metadata) error {
	data, err := MarshalMetadata(m)
	if err != nil {
		return err
	}
	if err := cl.PutBytes(repo, path, data); err != nil {
		return fmt.Errorf("upload %s: %w", path, err)
	}
	return nil
}

// nextSnapshotVersion computes the timestamped version for a new SNAPSHOT
// build and records it in the metadata's snapshot block.
func nextSnapshotVersion(m *Metadata, c Coords, now time.Time) string {
	build := 1
	if s := m.Versioning.Snapshot; s != nil {
		build = s.BuildNumber + 1
	}
	m.Versioning.Snapshot = &Snapshot{Timestamp: now.Format(timestampFormat), BuildNumber: build}
	return strings.TrimSuffix(c.Version, "SNAPSHOT") + now.Format(timestampFormat) + "-" + fmt.Sprint(build)
}

// mergeSnapshotVersions records the uploaded files in snapshotVersions,
// replacing prior entries for the same classifier/extension and keeping
// entries for other classifiers intact.
func mergeSnapshotVersions(m *Metadata, c Coords, resolved string, withPOM bool, now time.Time) {
	if m.Versioning.SnapshotVersions == nil {
		m.Versioning.SnapshotVersions = &SnapshotVersionList{}
	}
	list := m.Versioning.SnapshotVersions
	upsert := func(classifier, ext string) {
		updated := now.Format(lastUpdatedFormat)
		for i, sv := range list.SnapshotVersion {
			if sv.Classifier == classifier && sv.Extension == ext {
				list.SnapshotVersion[i].Value = resolved
				list.SnapshotVersion[i].Updated = updated
				return
			}
		}
		list.SnapshotVersion = append(list.SnapshotVersion,
			SnapshotVersion{Classifier: classifier, Extension: ext, Value: resolved, Updated: updated})
	}
	upsert(c.Classifier, c.Type)
	if withPOM {
		upsert("", "pom")
	}
}

// updateArtifactMetadata adds the version to the artifact-level version list
// and, for non-SNAPSHOTs, advances release. Maven's deploy does not write
// latest (a plugin-metadata concept), so neither does goven.
func updateArtifactMetadata(m *Metadata, c Coords, now time.Time) {
	if m.Versioning.Versions == nil {
		m.Versioning.Versions = &VersionList{}
	}
	if !slices.Contains(m.Versioning.Versions.Version, c.Version) {
		m.Versioning.Versions.Version = append(m.Versioning.Versions.Version, c.Version)
	}
	if !c.IsSnapshot() {
		m.Versioning.Release = c.Version
	}
	m.Versioning.LastUpdated = now.Format(lastUpdatedFormat)
}
