package repo

import (
	"strings"
	"testing"
)

const sampleSettings = `<?xml version="1.0"?>
<settings>
  <localRepository>/tmp/repo</localRepository>
  <servers>
    <server><id>nexus</id><username>deployer</username><password>${env.NEXUS_PASS}</password></server>
  </servers>
  <mirrors>
    <mirror><id>corp-mirror</id><mirrorOf>central</mirrorOf><url>https://nexus.corp/repository/maven-central/</url></mirror>
  </mirrors>
  <proxies>
    <proxy><id>corp</id><active>true</active><protocol>http</protocol><host>proxy.corp</host><port>3128</port><nonProxyHosts>*.corp|localhost</nonProxyHosts></proxy>
  </proxies>
  <profiles>
    <profile>
      <id>nexus-prod</id>
      <repositories>
        <repository>
          <id>nexus</id>
          <url>https://nexus.corp/repository/maven-public</url>
          <snapshots><enabled>true</enabled></snapshots>
        </repository>
      </repositories>
      <properties><deploy.env>prod</deploy.env></properties>
    </profile>
    <profile>
      <id>defaults</id>
      <activation><activeByDefault>true</activeByDefault></activation>
      <properties><deploy.env>dev</deploy.env></properties>
    </profile>
    <profile>
      <id>ci</id>
      <activation><property><name>ci</name></property></activation>
    </profile>
  </profiles>
  <activeProfiles></activeProfiles>
</settings>`

func parseSample(t *testing.T) *Settings {
	t.Helper()
	s, err := ParseSettings(strings.NewReader(sampleSettings))
	if err != nil {
		t.Fatalf("ParseSettings: %v", err)
	}
	return s
}

func TestParseSettings(t *testing.T) {
	s := parseSample(t)
	if s.LocalRepository != "/tmp/repo" {
		t.Errorf("localRepository = %q", s.LocalRepository)
	}
	if len(s.Servers) != 1 || s.Servers[0].ID != "nexus" || s.Servers[0].Username != "deployer" {
		t.Errorf("servers = %+v", s.Servers)
	}
	if len(s.Mirrors) != 1 || s.Mirrors[0].MirrorOf != "central" {
		t.Errorf("mirrors = %+v", s.Mirrors)
	}
	if len(s.Proxies) != 1 || !s.Proxies[0].Active || s.Proxies[0].Port != 3128 {
		t.Errorf("proxies = %+v", s.Proxies)
	}
	if len(s.Profiles) != 3 {
		t.Fatalf("profiles = %d, want 3", len(s.Profiles))
	}
	nexus := s.Profiles[0]
	if !nexus.Repositories[0].Snapshots || !nexus.Repositories[0].Releases {
		t.Errorf("nexus repo policies = %+v (releases should default true)", nexus.Repositories[0])
	}
	if nexus.Properties["deploy.env"] != "prod" {
		t.Errorf("properties = %+v", nexus.Properties)
	}
	if !s.Profiles[1].ActiveByDefault {
		t.Error("defaults profile should be activeByDefault")
	}
	if s.Profiles[2].ActivationProperty == nil || s.Profiles[2].ActivationProperty.Name != "ci" {
		t.Errorf("ci activation = %+v", s.Profiles[2].ActivationProperty)
	}
}

func TestParseSettingsRejectsGarbage(t *testing.T) {
	if _, err := ParseSettings(strings.NewReader("not xml at all")); err == nil {
		t.Error("expected error for non-XML input")
	}
}

func TestMergeSettingsUserWins(t *testing.T) {
	user := &Settings{
		LocalRepository: "/u/repo",
		Servers:         []Server{{ID: "nexus", Username: "user-cred"}},
		Profiles:        []Profile{{ID: "shared"}},
	}
	global := &Settings{
		LocalRepository: "/g/repo",
		Servers:         []Server{{ID: "nexus", Username: "global-cred"}, {ID: "other", Username: "g"}},
		Profiles:        []Profile{{ID: "shared", ActiveByDefault: true}, {ID: "global-only"}},
		ActiveProfiles:  []string{"global-only"},
	}
	m := mergeSettings(user, global)
	if m.LocalRepository != "/u/repo" {
		t.Errorf("localRepository = %q", m.LocalRepository)
	}
	if len(m.Servers) != 2 || m.Servers[0].Username != "user-cred" {
		t.Errorf("servers = %+v", m.Servers)
	}
	if len(m.Profiles) != 2 || m.Profiles[0].ActiveByDefault {
		t.Errorf("profiles = %+v (user's shared profile must win)", m.Profiles)
	}
	if len(m.ActiveProfiles) != 1 || m.ActiveProfiles[0] != "global-only" {
		t.Errorf("activeProfiles = %v", m.ActiveProfiles)
	}
}

func TestMergeSettingsNilInputs(t *testing.T) {
	if m := mergeSettings(nil, nil); m == nil {
		t.Fatal("mergeSettings(nil, nil) returned nil")
	}
}

func FuzzParseSettings(f *testing.F) {
	f.Add(sampleSettings)
	f.Add("<settings></settings>")
	f.Add("<settings><profiles><profile><properties><a>1</a></properties></profile></profiles></settings>")
	f.Fuzz(func(t *testing.T, data string) {
		s, err := ParseSettings(strings.NewReader(data))
		if err == nil && s == nil {
			t.Error("nil settings without error")
		}
	})
}
