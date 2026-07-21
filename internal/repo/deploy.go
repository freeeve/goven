package repo

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
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

// PutBytes uploads data followed by its md5/sha1/sha256/sha512 checksum
// sidecars, covering both Maven's default publication set and stronger
// verifiers.
func (cl *Client) PutBytes(repo RemoteRepo, path string, data []byte) error {
	if err := cl.put(repo, path, func() (io.ReadCloser, int64) {
		return io.NopCloser(bytes.NewReader(data)), int64(len(data))
	}); err != nil {
		return err
	}
	for _, algo := range []struct {
		ext string
		new func() hash.Hash
	}{{"md5", md5.New}, {"sha1", sha1.New}, {"sha256", sha256.New}, {"sha512", sha512.New}} {
		h := algo.new()
		h.Write(data)
		sum := []byte(hex.EncodeToString(h.Sum(nil)))
		if err := cl.put(repo, path+"."+algo.ext, func() (io.ReadCloser, int64) {
			return io.NopCloser(bytes.NewReader(sum)), int64(len(sum))
		}); err != nil {
			return err
		}
	}
	return nil
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
	hashes := []hash.Hash{md5.New(), sha1.New(), sha256.New(), sha512.New()}
	writers := make([]io.Writer, len(hashes))
	for i, h := range hashes {
		writers[i] = h
	}
	if _, err := io.Copy(io.MultiWriter(writers...), f); err != nil {
		f.Close()
		return err
	}
	f.Close()

	if err := cl.put(repo, path, func() (io.ReadCloser, int64) {
		rc, err := os.Open(localFile)
		if err != nil {
			return io.NopCloser(bytes.NewReader(nil)), 0
		}
		return rc, size
	}); err != nil {
		return err
	}
	for i, ext := range []string{"md5", "sha1", "sha256", "sha512"} {
		sum := []byte(hex.EncodeToString(hashes[i].Sum(nil)))
		if err := cl.put(repo, path+"."+ext, func() (io.ReadCloser, int64) {
			return io.NopCloser(bytes.NewReader(sum)), int64(len(sum))
		}); err != nil {
			return err
		}
	}
	return nil
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
	if err := cl.PutFile(repo, artifactPath, artifactFile); err != nil {
		return res, fmt.Errorf("upload %s: %w", artifactPath, err)
	}
	res.Uploaded = append(res.Uploaded, artifactPath)

	pomCoords := Coords{GroupID: c.GroupID, ArtifactID: c.ArtifactID, Version: c.Version, Type: "pom"}
	if pom != nil {
		pomPath := pomCoords.ArtifactPath(resolved)
		if err := cl.PutBytes(repo, pomPath, pom); err != nil {
			return res, fmt.Errorf("upload %s: %w", pomPath, err)
		}
		res.Uploaded = append(res.Uploaded, pomPath)
	}

	if c.IsSnapshot() {
		mergeSnapshotVersions(versionMeta, c, resolved, pom != nil, now)
		versionMeta.Versioning.LastUpdated = now.Format(lastUpdatedFormat)
		if err := cl.putMetadata(repo, c.VersionMetadataPath(), versionMeta); err != nil {
			return res, err
		}
		res.Uploaded = append(res.Uploaded, c.VersionMetadataPath())
	}

	artMeta, err := cl.fetchOrInitMetadata(repo, c.ArtifactMetadataPath(), c, false)
	if err != nil {
		return res, err
	}
	updateArtifactMetadata(artMeta, c, now)
	if err := cl.putMetadata(repo, c.ArtifactMetadataPath(), artMeta); err != nil {
		return res, err
	}
	res.Uploaded = append(res.Uploaded, c.ArtifactMetadataPath())
	return res, nil
}

// fetchOrInitMetadata reads existing metadata from the repository or starts a
// fresh document for the coordinate. versionLevel selects the SNAPSHOT
// (modelVersion 1.1.0, with <version>) form over the artifact-level form.
func (cl *Client) fetchOrInitMetadata(repo RemoteRepo, path string, c Coords, versionLevel bool) (*Metadata, error) {
	raw, err := cl.GetBytes(repo, path)
	if err == nil {
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
