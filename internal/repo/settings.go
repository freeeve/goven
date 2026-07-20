// Package repo implements the Maven repository protocol natively: settings.xml
// configuration, maven2 layout, metadata, and checksum-verified HTTP transfer.
package repo

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Settings models the subset of Maven's settings.xml that governs repository
// access: servers, mirrors, proxies, and profiles with their repositories.
type Settings struct {
	LocalRepository string
	Servers         []Server
	Mirrors         []Mirror
	Proxies         []Proxy
	Profiles        []Profile
	ActiveProfiles  []string
}

// Server holds credentials for a repository or mirror, matched by ID.
type Server struct {
	ID       string
	Username string
	Password string
}

// Mirror redirects requests for the repositories matched by MirrorOf to URL.
type Mirror struct {
	ID       string
	MirrorOf string
	URL      string
}

// Proxy describes an outbound HTTP proxy from settings.xml.
type Proxy struct {
	ID            string
	Active        bool
	Protocol      string
	Host          string
	Port          int
	Username      string
	Password      string
	NonProxyHosts string
}

// Profile carries the repository definitions and properties contributed when
// the profile is active.
type Profile struct {
	ID                 string
	ActiveByDefault    bool
	ActivationProperty *ActivationProperty
	Repositories       []Repository
	Properties         map[string]string
}

// ActivationProperty activates a profile when a property is present (or has a
// specific value); a leading "!" on Name inverts to absence.
type ActivationProperty struct {
	Name  string
	Value string
}

// Repository is a remote repository declaration from a settings profile.
type Repository struct {
	ID        string
	URL       string
	Releases  bool
	Snapshots bool
}

// xml decoding shadows: settings.xml uses nested wrapper elements and dynamic
// property tags, so decoding goes through these and converts to the public
// model.
type xmlSettings struct {
	LocalRepository string       `xml:"localRepository"`
	Servers         []xmlServer  `xml:"servers>server"`
	Mirrors         []xmlMirror  `xml:"mirrors>mirror"`
	Proxies         []xmlProxy   `xml:"proxies>proxy"`
	Profiles        []xmlProfile `xml:"profiles>profile"`
	ActiveProfiles  []string     `xml:"activeProfiles>activeProfile"`
}

type xmlServer struct {
	ID       string `xml:"id"`
	Username string `xml:"username"`
	Password string `xml:"password"`
}

type xmlMirror struct {
	ID       string `xml:"id"`
	MirrorOf string `xml:"mirrorOf"`
	URL      string `xml:"url"`
}

type xmlProxy struct {
	ID            string `xml:"id"`
	Active        *bool  `xml:"active"`
	Protocol      string `xml:"protocol"`
	Host          string `xml:"host"`
	Port          int    `xml:"port"`
	Username      string `xml:"username"`
	Password      string `xml:"password"`
	NonProxyHosts string `xml:"nonProxyHosts"`
}

type xmlProfile struct {
	ID         string `xml:"id"`
	Activation *struct {
		ActiveByDefault bool `xml:"activeByDefault"`
		Property        *struct {
			Name  string `xml:"name"`
			Value string `xml:"value"`
		} `xml:"property"`
	} `xml:"activation"`
	Repositories []xmlRepository `xml:"repositories>repository"`
	Properties   propertyMap     `xml:"properties"`
}

type xmlRepository struct {
	ID        string     `xml:"id"`
	URL       string     `xml:"url"`
	Releases  *xmlPolicy `xml:"releases"`
	Snapshots *xmlPolicy `xml:"snapshots"`
}

type xmlPolicy struct {
	Enabled *bool `xml:"enabled"`
}

// propertyMap decodes a <properties> element whose children are arbitrary
// property names.
type propertyMap map[string]string

// UnmarshalXML collects each child element's local name and character data.
func (p *propertyMap) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	*p = propertyMap{}
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			var v string
			if err := d.DecodeElement(&v, &t); err != nil {
				return err
			}
			(*p)[t.Name.Local] = v
		case xml.EndElement:
			return nil
		}
	}
}

// ParseSettings decodes one settings.xml document.
func ParseSettings(r io.Reader) (*Settings, error) {
	var x xmlSettings
	dec := xml.NewDecoder(r)
	if err := dec.Decode(&x); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	s := &Settings{
		LocalRepository: x.LocalRepository,
		ActiveProfiles:  x.ActiveProfiles,
	}
	for _, v := range x.Servers {
		s.Servers = append(s.Servers, Server(v))
	}
	for _, v := range x.Mirrors {
		s.Mirrors = append(s.Mirrors, Mirror(v))
	}
	for _, v := range x.Proxies {
		p := Proxy{ID: v.ID, Active: true, Protocol: v.Protocol, Host: v.Host,
			Port: v.Port, Username: v.Username, Password: v.Password, NonProxyHosts: v.NonProxyHosts}
		if v.Active != nil {
			p.Active = *v.Active
		}
		if p.Protocol == "" {
			p.Protocol = "http"
		}
		s.Proxies = append(s.Proxies, p)
	}
	for _, v := range x.Profiles {
		s.Profiles = append(s.Profiles, convertProfile(v))
	}
	return s, nil
}

// convertProfile maps a decoded profile onto the public model, applying
// Maven's default of enabled=true for unspecified repository policies.
func convertProfile(x xmlProfile) Profile {
	p := Profile{ID: x.ID, Properties: x.Properties}
	if x.Activation != nil {
		p.ActiveByDefault = x.Activation.ActiveByDefault
		if x.Activation.Property != nil {
			p.ActivationProperty = &ActivationProperty{
				Name:  x.Activation.Property.Name,
				Value: x.Activation.Property.Value,
			}
		}
	}
	for _, r := range x.Repositories {
		p.Repositories = append(p.Repositories, Repository{
			ID:        r.ID,
			URL:       r.URL,
			Releases:  policyEnabled(r.Releases),
			Snapshots: policyEnabled(r.Snapshots),
		})
	}
	return p
}

// policyEnabled returns Maven's effective enabled flag for a repository
// policy element, defaulting to true when the element or flag is absent.
func policyEnabled(p *xmlPolicy) bool {
	return p == nil || p.Enabled == nil || *p.Enabled
}

// DefaultUserSettingsPath returns ~/.m2/settings.xml.
func DefaultUserSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".m2", "settings.xml")
}

// DefaultGlobalSettingsPath returns $M2_HOME/conf/settings.xml (or
// $MAVEN_HOME/conf/settings.xml) when set.
func DefaultGlobalSettingsPath() string {
	for _, env := range []string{"M2_HOME", "MAVEN_HOME"} {
		if v := os.Getenv(env); v != "" {
			return filepath.Join(v, "conf", "settings.xml")
		}
	}
	return ""
}

// LoadSettings reads and merges the user and global settings files. Either
// path may be empty or missing; an empty Settings is returned when neither
// exists. The returned slice lists the files actually loaded, user first.
func LoadSettings(userPath, globalPath string) (*Settings, []string, error) {
	var loaded []string
	parse := func(path string) (*Settings, error) {
		if path == "" {
			return nil, nil
		}
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		defer f.Close()
		s, err := ParseSettings(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		loaded = append(loaded, path)
		return s, nil
	}
	user, err := parse(userPath)
	if err != nil {
		return nil, nil, err
	}
	global, err := parse(globalPath)
	if err != nil {
		return nil, nil, err
	}
	return mergeSettings(user, global), loaded, nil
}

// mergeSettings combines user and global settings with user entries dominant:
// list entries are concatenated user-first with global entries of a duplicate
// ID dropped, matching Maven's recessive merge.
func mergeSettings(user, global *Settings) *Settings {
	if user == nil {
		user = &Settings{}
	}
	if global == nil {
		global = &Settings{}
	}
	out := &Settings{
		LocalRepository: user.LocalRepository,
		ActiveProfiles:  append(append([]string{}, user.ActiveProfiles...), global.ActiveProfiles...),
	}
	if out.LocalRepository == "" {
		out.LocalRepository = global.LocalRepository
	}
	out.Servers = append([]Server{}, user.Servers...)
	for _, g := range global.Servers {
		if !hasID(out.Servers, g.ID, func(s Server) string { return s.ID }) {
			out.Servers = append(out.Servers, g)
		}
	}
	out.Mirrors = append([]Mirror{}, user.Mirrors...)
	for _, g := range global.Mirrors {
		if !hasID(out.Mirrors, g.ID, func(m Mirror) string { return m.ID }) {
			out.Mirrors = append(out.Mirrors, g)
		}
	}
	out.Proxies = append([]Proxy{}, user.Proxies...)
	for _, g := range global.Proxies {
		if !hasID(out.Proxies, g.ID, func(p Proxy) string { return p.ID }) {
			out.Proxies = append(out.Proxies, g)
		}
	}
	out.Profiles = append([]Profile{}, user.Profiles...)
	for _, g := range global.Profiles {
		if !hasID(out.Profiles, g.ID, func(p Profile) string { return p.ID }) {
			out.Profiles = append(out.Profiles, g)
		}
	}
	return out
}

// hasID reports whether any element of list has the given ID under key.
func hasID[T any](list []T, id string, key func(T) string) bool {
	for _, v := range list {
		if key(v) == id {
			return true
		}
	}
	return false
}
