package pom

import (
	"strings"
	"testing"

	"github.com/freeeve/goven/internal/repo"
)

func TestGenerate(t *testing.T) {
	data, err := Generate(repo.Coords{GroupID: "com.example", ArtifactID: "lib", Version: "1.0.0", Type: "jar"})
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		`<project xmlns="http://maven.apache.org/POM/4.0.0">`,
		"<modelVersion>4.0.0</modelVersion>",
		"<groupId>com.example</groupId>",
		"<artifactId>lib</artifactId>",
		"<version>1.0.0</version>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated POM missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<packaging>") {
		t.Errorf("jar packaging must be omitted:\n%s", out)
	}
}

func TestGenerateNonJarPackaging(t *testing.T) {
	data, err := Generate(repo.Coords{GroupID: "g", ArtifactID: "a", Version: "1.0", Type: "tar.gz"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<packaging>tar.gz</packaging>") {
		t.Errorf("packaging missing:\n%s", data)
	}
}
