package repo

import (
	"fmt"
	"maps"
	"net/url"
	"os"
	"strings"
)

// CentralURL is the default Maven Central repository, injected when no active
// profile overrides the "central" repository ID, mirroring the super POM.
const CentralURL = "https://repo.maven.apache.org/maven2"

// RemoteRepo is a fully resolved repository: mirror applied, credentials and
// proxy attached, URLs interpolated. This is what the transfer client consumes.
type RemoteRepo struct {
	ID        string
	URL       string
	Releases  bool
	Snapshots bool
	Username  string
	Password  string
	MirrorID  string // non-empty when a mirror redirected this repository
	Proxy     *Proxy
}

// ActiveProfiles resolves which profiles are active given the -P list (with
// "!" deactivations), <activeProfiles>, and property-based activation against
// props. Per Maven, activeByDefault applies only when nothing else activated
// any profile.
func ActiveProfiles(s *Settings, requested []string, props map[string]string) []Profile {
	explicit := map[string]bool{}
	deactivated := map[string]bool{}
	for _, p := range requested {
		if name, ok := strings.CutPrefix(p, "!"); ok {
			deactivated[name] = true
		} else {
			explicit[p] = true
		}
	}
	for _, p := range s.ActiveProfiles {
		explicit[p] = true
	}
	var active []Profile
	for _, prof := range s.Profiles {
		if deactivated[prof.ID] {
			continue
		}
		if explicit[prof.ID] || propertyActive(prof.ActivationProperty, props) {
			active = append(active, prof)
		}
	}
	if len(active) == 0 {
		for _, prof := range s.Profiles {
			if prof.ActiveByDefault && !deactivated[prof.ID] {
				active = append(active, prof)
			}
		}
	}
	return active
}

// propertyActive evaluates a property activation rule: a leading "!" on the
// name requires absence; a leading "!" on the value requires inequality.
func propertyActive(a *ActivationProperty, props map[string]string) bool {
	if a == nil {
		return false
	}
	if name, ok := strings.CutPrefix(a.Name, "!"); ok {
		_, present := props[name]
		return !present
	}
	v, present := props[a.Name]
	if !present {
		return false
	}
	if a.Value == "" {
		return true
	}
	if want, ok := strings.CutPrefix(a.Value, "!"); ok {
		return v != want
	}
	return v == a.Value
}

// Interpolate expands ${...} references using, in precedence order: the
// supplied properties, ${env.NAME} environment lookups, and the built-in
// ${user.home}. Unresolvable references are left intact. Expansion recurses
// to a fixed depth to terminate self-referential cycles.
func Interpolate(s string, props map[string]string) string {
	for range 10 {
		expanded, changed := interpolateOnce(s, props)
		if !changed {
			return expanded
		}
		s = expanded
	}
	return s
}

// interpolateOnce performs a single expansion pass, reporting whether any
// reference was substituted.
func interpolateOnce(s string, props map[string]string) (string, bool) {
	var b strings.Builder
	changed := false
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			b.WriteString(s)
			return b.String(), changed
		}
		j := strings.Index(s[i:], "}")
		if j < 0 {
			b.WriteString(s)
			return b.String(), changed
		}
		b.WriteString(s[:i])
		key := s[i+2 : i+j]
		val, ok := lookupProperty(key, props)
		if ok {
			b.WriteString(val)
			changed = true
		} else {
			b.WriteString(s[i : i+j+1])
		}
		s = s[i+j+1:]
	}
}

// lookupProperty resolves one ${...} key against props, env, and built-ins.
func lookupProperty(key string, props map[string]string) (string, bool) {
	if v, ok := props[key]; ok {
		return v, true
	}
	if name, ok := strings.CutPrefix(key, "env."); ok {
		if v, ok := os.LookupEnv(name); ok {
			return v, true
		}
		return "", false
	}
	if key == "user.home" {
		if home, err := os.UserHomeDir(); err == nil {
			return home, true
		}
	}
	return "", false
}

// MatchMirror returns the first mirror whose mirrorOf pattern matches the
// repository ID, following Maven's DefaultMirrorSelector semantics: exact ID,
// "*", "external:*", comma-separated lists, and "!" exclusions (exclusions
// win regardless of position).
func MatchMirror(mirrors []Mirror, repo Repository) *Mirror {
	for i, m := range mirrors {
		if mirrorMatches(m.MirrorOf, repo) {
			return &mirrors[i]
		}
	}
	return nil
}

// mirrorMatches evaluates one mirrorOf pattern against a repository.
func mirrorMatches(pattern string, repo Repository) bool {
	matched := false
	for tok := range strings.SplitSeq(pattern, ",") {
		tok = strings.TrimSpace(tok)
		switch {
		case tok == "!"+repo.ID:
			return false
		case tok == repo.ID:
			matched = true
		case tok == "*":
			matched = true
		case tok == "external:*" && isExternal(repo.URL):
			matched = true
		}
	}
	return matched
}

// isExternal reports whether a repository URL points outside the local
// machine, per Maven's external:* rule (not localhost, not file://).
func isExternal(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	if strings.EqualFold(u.Scheme, "file") {
		return false
	}
	host := u.Hostname()
	return host != "localhost" && host != "127.0.0.1"
}

// EffectiveProps layers -D properties over the active profiles' properties
// (later profiles win among themselves, -D wins overall).
func EffectiveProps(active []Profile, cmdProps map[string]string) map[string]string {
	out := map[string]string{}
	for _, p := range active {
		maps.Copy(out, p.Properties)
	}
	maps.Copy(out, cmdProps)
	return out
}

// EffectiveRepos computes the ordered repository list for the given profile
// activation: profile repositories first (user settings order), then Maven
// Central unless an active profile redefines the "central" ID. Mirrors,
// credentials (with settings-security decryption), proxies, and
// interpolation are all applied.
func EffectiveRepos(s *Settings, requested []string, cmdProps map[string]string) ([]RemoteRepo, error) {
	active := ActiveProfiles(s, requested, cmdProps)
	props := EffectiveProps(active, cmdProps)

	var repos []Repository
	for _, prof := range active {
		repos = append(repos, prof.Repositories...)
	}
	if !hasID(repos, "central", func(r Repository) string { return r.ID }) {
		repos = append(repos, Repository{ID: "central", URL: CentralURL, Releases: true, Snapshots: false})
	}

	var out []RemoteRepo
	for _, r := range repos {
		r.URL = strings.TrimRight(Interpolate(r.URL, props), "/")
		rr := RemoteRepo{
			ID:        r.ID,
			URL:       r.URL,
			Releases:  r.Releases,
			Snapshots: r.Snapshots,
		}
		if m := MatchMirror(s.Mirrors, r); m != nil {
			rr.URL = strings.TrimRight(Interpolate(m.URL, props), "/")
			rr.MirrorID = m.ID
		}
		credID := rr.ID
		if rr.MirrorID != "" {
			credID = rr.MirrorID
		}
		for _, srv := range s.Servers {
			if srv.ID == credID {
				rr.Username = Interpolate(srv.Username, props)
				pw, err := ResolvePassword(Interpolate(srv.Password, props), s.Master)
				if err != nil {
					return nil, fmt.Errorf("server %q: %w", credID, err)
				}
				rr.Password = pw
				break
			}
		}
		rr.Proxy = selectProxy(s.Proxies, rr.URL)
		out = append(out, rr)
	}
	return out, nil
}

// selectProxy picks the first active proxy whose protocol matches the target
// URL's scheme and whose nonProxyHosts list does not exempt the host.
func selectProxy(proxies []Proxy, target string) *Proxy {
	u, err := url.Parse(target)
	if err != nil {
		return nil
	}
	for i, p := range proxies {
		if !p.Active || !strings.EqualFold(p.Protocol, u.Scheme) {
			continue
		}
		if hostExempt(p.NonProxyHosts, u.Hostname()) {
			continue
		}
		return &proxies[i]
	}
	return nil
}

// hostExempt evaluates a nonProxyHosts pattern list ("|"-separated, "*"
// wildcards at either end) against a hostname.
func hostExempt(patterns, host string) bool {
	for pat := range strings.SplitSeq(patterns, "|") {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		switch {
		case pat == host:
			return true
		case strings.HasPrefix(pat, "*") && strings.HasSuffix(host, pat[1:]):
			return true
		case strings.HasSuffix(pat, "*") && strings.HasPrefix(host, pat[:len(pat)-1]):
			return true
		}
	}
	return false
}

// String renders a repository for diagnostics with credentials masked.
func (r RemoteRepo) String() string {
	auth := ""
	if r.Username != "" {
		auth = fmt.Sprintf(" auth=%s/****", r.Username)
	}
	via := ""
	if r.MirrorID != "" {
		via = fmt.Sprintf(" (via mirror %s)", r.MirrorID)
	}
	return fmt.Sprintf("%s %s releases=%v snapshots=%v%s%s", r.ID, r.URL, r.Releases, r.Snapshots, auth, via)
}
