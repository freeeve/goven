// Package pom generates and (in later milestones) parses Maven POM files.
package pom

import (
	"encoding/xml"

	"github.com/freeeve/goven/internal/repo"
)

// project is the minimal POM document deploy-file generates when no POM is
// supplied.
type project struct {
	XMLName      xml.Name `xml:"project"`
	Xmlns        string   `xml:"xmlns,attr"`
	ModelVersion string   `xml:"modelVersion"`
	GroupID      string   `xml:"groupId"`
	ArtifactID   string   `xml:"artifactId"`
	Version      string   `xml:"version"`
	Packaging    string   `xml:"packaging,omitempty"`
}

// Generate renders a minimal POM for the coordinates, equivalent to
// mvn deploy:deploy-file's generatePom output. The packaging element is
// omitted for the default "jar".
func Generate(c repo.Coords) ([]byte, error) {
	p := project{
		Xmlns:        "http://maven.apache.org/POM/4.0.0",
		ModelVersion: "4.0.0",
		GroupID:      c.GroupID,
		ArtifactID:   c.ArtifactID,
		Version:      c.Version,
	}
	if c.Type != "jar" {
		p.Packaging = c.Type
	}
	body, err := xml.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(append([]byte(xml.Header), body...), '\n'), nil
}
