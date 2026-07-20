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

// FileName is the artifact's file name for a given resolved version string.
// For releases the resolved version equals c.Version; for SNAPSHOTs it may be
// the timestamped form (e.g. 1.0-20260720.093012-3).
func (c Coords) FileName(resolvedVersion string) string {
	name := c.ArtifactID + "-" + resolvedVersion
	if c.Classifier != "" {
		name += "-" + c.Classifier
	}
	return name + "." + c.Type
}

// VersionDir is the repository directory holding the artifact's version:
// group path (dots to slashes) / artifactId / version.
func (c Coords) VersionDir() string {
	return strings.ReplaceAll(c.GroupID, ".", "/") + "/" + c.ArtifactID + "/" + c.Version
}

// ArtifactPath is the repository-relative path of the artifact file for a
// resolved version.
func (c Coords) ArtifactPath(resolvedVersion string) string {
	return c.VersionDir() + "/" + c.FileName(resolvedVersion)
}

// VersionMetadataPath is the repository-relative path of the version-level
// maven-metadata.xml, which holds SNAPSHOT timestamp/buildNumber data.
func (c Coords) VersionMetadataPath() string {
	return c.VersionDir() + "/maven-metadata.xml"
}

// ArtifactMetadataPath is the repository-relative path of the artifact-level
// maven-metadata.xml, which lists known versions.
func (c Coords) ArtifactMetadataPath() string {
	return strings.ReplaceAll(c.GroupID, ".", "/") + "/" + c.ArtifactID + "/maven-metadata.xml"
}
