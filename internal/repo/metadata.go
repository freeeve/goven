package repo

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Metadata models maven-metadata.xml at both levels: artifact level (version
// list, latest/release) and version level (SNAPSHOT timestamp/buildNumber and
// per-file snapshotVersions).
type Metadata struct {
	XMLName    xml.Name   `xml:"metadata"`
	GroupID    string     `xml:"groupId"`
	ArtifactID string     `xml:"artifactId"`
	Version    string     `xml:"version,omitempty"`
	Versioning Versioning `xml:"versioning"`
}

// Versioning is the <versioning> block of maven-metadata.xml.
type Versioning struct {
	Latest           string            `xml:"latest,omitempty"`
	Release          string            `xml:"release,omitempty"`
	Versions         []string          `xml:"versions>version,omitempty"`
	Snapshot         *Snapshot         `xml:"snapshot,omitempty"`
	SnapshotVersions []SnapshotVersion `xml:"snapshotVersions>snapshotVersion,omitempty"`
	LastUpdated      string            `xml:"lastUpdated,omitempty"`
}

// Snapshot carries the current timestamped build of a SNAPSHOT version.
type Snapshot struct {
	Timestamp   string `xml:"timestamp,omitempty"`
	BuildNumber int    `xml:"buildNumber,omitempty"`
	LocalCopy   bool   `xml:"localCopy,omitempty"`
}

// SnapshotVersion records the resolved value for one classifier/extension
// combination of a SNAPSHOT.
type SnapshotVersion struct {
	Classifier string `xml:"classifier,omitempty"`
	Extension  string `xml:"extension"`
	Value      string `xml:"value"`
	Updated    string `xml:"updated,omitempty"`
}

// ParseMetadata decodes one maven-metadata.xml document.
func ParseMetadata(r io.Reader) (*Metadata, error) {
	var m Metadata
	if err := xml.NewDecoder(r).Decode(&m); err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}
	return &m, nil
}

// ResolveSnapshotVersion returns the concrete version string to use in file
// names for a SNAPSHOT coordinate, given the version-level metadata fetched
// from a remote repository. Preference order follows Maven: an exact
// snapshotVersions entry for the coordinate's classifier and extension, then
// the <snapshot> timestamp/buildNumber, then the literal -SNAPSHOT name (the
// non-unique-snapshot / localCopy case).
func (m *Metadata) ResolveSnapshotVersion(c Coords) string {
	for _, sv := range m.Versioning.SnapshotVersions {
		if sv.Classifier == c.Classifier && sv.Extension == c.Type && sv.Value != "" {
			return sv.Value
		}
	}
	s := m.Versioning.Snapshot
	if s != nil && !s.LocalCopy && s.Timestamp != "" && s.BuildNumber > 0 {
		base := strings.TrimSuffix(c.Version, "SNAPSHOT")
		return fmt.Sprintf("%s%s-%d", base, s.Timestamp, s.BuildNumber)
	}
	return c.Version
}
