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
	XMLName      xml.Name   `xml:"metadata"`
	ModelVersion string     `xml:"modelVersion,attr,omitempty"`
	GroupID      string     `xml:"groupId"`
	ArtifactID   string     `xml:"artifactId"`
	Version      string     `xml:"version,omitempty"`
	Versioning   Versioning `xml:"versioning"`
}

// Versioning is the <versioning> block of maven-metadata.xml. Field order
// matters for serialization: it yields Maven's element order at both metadata
// levels. The list wrappers are pointers because Go's a>b path tags would
// otherwise emit empty wrapper elements Maven never writes.
type Versioning struct {
	Latest           string               `xml:"latest,omitempty"`
	Release          string               `xml:"release,omitempty"`
	Versions         *VersionList         `xml:"versions,omitempty"`
	Snapshot         *Snapshot            `xml:"snapshot,omitempty"`
	LastUpdated      string               `xml:"lastUpdated,omitempty"`
	SnapshotVersions *SnapshotVersionList `xml:"snapshotVersions,omitempty"`
}

// VersionList wraps the artifact-level <versions> element.
type VersionList struct {
	Version []string `xml:"version"`
}

// SnapshotVersionList wraps the version-level <snapshotVersions> element.
type SnapshotVersionList struct {
	SnapshotVersion []SnapshotVersion `xml:"snapshotVersion"`
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

// MarshalMetadata serializes metadata as Maven writes it: XML declaration,
// two-space indent, trailing newline.
func MarshalMetadata(m *Metadata) ([]byte, error) {
	body, err := xml.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(append([]byte(xml.Header), body...), '\n'), nil
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
	if l := m.Versioning.SnapshotVersions; l != nil {
		for _, sv := range l.SnapshotVersion {
			if sv.Classifier == c.Classifier && sv.Extension == c.Type && sv.Value != "" {
				return sv.Value
			}
		}
	}
	s := m.Versioning.Snapshot
	if s != nil && !s.LocalCopy && s.Timestamp != "" && s.BuildNumber > 0 {
		base := strings.TrimSuffix(c.Version, "SNAPSHOT")
		return fmt.Sprintf("%s%s-%d", base, s.Timestamp, s.BuildNumber)
	}
	return c.Version
}
