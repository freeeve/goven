package repo

import (
	"fmt"
	"slices"
	"strings"
)

// Coords identifies one artifact by Maven coordinates. Type defaults to
// "jar"; Classifier is optional.
type Coords struct {
	GroupID    string
	ArtifactID string
	Version    string
	Type       string
	Classifier string
}

// ParseCoords parses "groupId:artifactId:version[:type[:classifier]]", the
// coordinate form used by mvn dependency:get.
func ParseCoords(s string) (Coords, error) {
	parts := strings.Split(s, ":")
	if slices.Contains(parts, "") {
		return Coords{}, fmt.Errorf("invalid coordinates %q: empty segment", s)
	}
	c := Coords{Type: "jar"}
	switch len(parts) {
	case 5:
		c.Classifier = parts[4]
		fallthrough
	case 4:
		c.Type = parts[3]
		fallthrough
	case 3:
		c.GroupID, c.ArtifactID, c.Version = parts[0], parts[1], parts[2]
	default:
		return Coords{}, fmt.Errorf("invalid coordinates %q: want groupId:artifactId:version[:type[:classifier]]", s)
	}
	return c, nil
}

// String renders the coordinates in canonical colon form.
func (c Coords) String() string {
	s := c.GroupID + ":" + c.ArtifactID + ":" + c.Version
	if c.Type != "jar" || c.Classifier != "" {
		s += ":" + c.Type
	}
	if c.Classifier != "" {
		s += ":" + c.Classifier
	}
	return s
}

// IsSnapshot reports whether the version is a SNAPSHOT.
func (c Coords) IsSnapshot() bool {
	return strings.HasSuffix(c.Version, "-SNAPSHOT")
}

// writeGroupPath appends the groupId with dots replaced by slashes.
func (c Coords) writeGroupPath(b *strings.Builder) {
	for i := 0; i < len(c.GroupID); i++ {
		if c.GroupID[i] == '.' {
			b.WriteByte('/')
		} else {
			b.WriteByte(c.GroupID[i])
		}
	}
}

// writeFileName appends the artifact file name for a resolved version.
func (c Coords) writeFileName(b *strings.Builder, resolvedVersion string) {
	b.WriteString(c.ArtifactID)
	b.WriteByte('-')
	b.WriteString(resolvedVersion)
	if c.Classifier != "" {
		b.WriteByte('-')
		b.WriteString(c.Classifier)
	}
	b.WriteByte('.')
	b.WriteString(c.Type)
}

// FileName is the artifact's file name for a given resolved version string.
// For releases the resolved version equals c.Version; for SNAPSHOTs it may be
// the timestamped form (e.g. 1.0-20260720.093012-3).
func (c Coords) FileName(resolvedVersion string) string {
	var b strings.Builder
	b.Grow(len(c.ArtifactID) + len(resolvedVersion) + len(c.Classifier) + len(c.Type) + 3)
	c.writeFileName(&b, resolvedVersion)
	return b.String()
}

// writeVersionDir appends group path / artifactId / version.
func (c Coords) writeVersionDir(b *strings.Builder) {
	c.writeGroupPath(b)
	b.WriteByte('/')
	b.WriteString(c.ArtifactID)
	b.WriteByte('/')
	b.WriteString(c.Version)
}

// VersionDir is the repository directory holding the artifact's version:
// group path (dots to slashes) / artifactId / version.
func (c Coords) VersionDir() string {
	var b strings.Builder
	b.Grow(len(c.GroupID) + len(c.ArtifactID) + len(c.Version) + 2)
	c.writeVersionDir(&b)
	return b.String()
}

// ArtifactPath is the repository-relative path of the artifact file for a
// resolved version.
func (c Coords) ArtifactPath(resolvedVersion string) string {
	var b strings.Builder
	b.Grow(len(c.GroupID) + 2*len(c.ArtifactID) + len(c.Version) + len(resolvedVersion) +
		len(c.Classifier) + len(c.Type) + 6)
	c.writeVersionDir(&b)
	b.WriteByte('/')
	c.writeFileName(&b, resolvedVersion)
	return b.String()
}

// metadataFile is the file name of Maven repository metadata documents.
const metadataFile = "maven-metadata.xml"

// VersionMetadataPath is the repository-relative path of the version-level
// maven-metadata.xml, which holds SNAPSHOT timestamp/buildNumber data.
func (c Coords) VersionMetadataPath() string {
	var b strings.Builder
	b.Grow(len(c.GroupID) + len(c.ArtifactID) + len(c.Version) + len(metadataFile) + 3)
	c.writeVersionDir(&b)
	b.WriteByte('/')
	b.WriteString(metadataFile)
	return b.String()
}

// ArtifactMetadataPath is the repository-relative path of the artifact-level
// maven-metadata.xml, which lists known versions.
func (c Coords) ArtifactMetadataPath() string {
	var b strings.Builder
	b.Grow(len(c.GroupID) + len(c.ArtifactID) + len(metadataFile) + 2)
	c.writeGroupPath(&b)
	b.WriteByte('/')
	b.WriteString(c.ArtifactID)
	b.WriteByte('/')
	b.WriteString(metadataFile)
	return b.String()
}
