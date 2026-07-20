package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/freeeve/goven/internal/repo"
)

func init() {
	register(&command{
		name:    "get",
		summary: "fetch an artifact by groupId:artifactId:version[:type[:classifier]]",
		run:     runGet,
	})
}

// runGet resolves and downloads one artifact from the effective repositories.
func runGet(g *globalOpts, args []string) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	out := fs.String("o", ".", "output file, or directory to place the artifact in")
	repoOverride := fs.String("repo", "", "fetch only from this repository, as [id::]url (id looks up settings credentials)")
	if err := fs.Parse(reorderArgs(args, map[string]bool{"o": true, "repo": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: goven get [-o out] [--repo [id::]url] <groupId:artifactId:version[:type[:classifier]]>")
	}
	coords, err := repo.ParseCoords(fs.Arg(0))
	if err != nil {
		return err
	}

	repos, _, err := effectiveRepos(g, *repoOverride)
	if err != nil {
		return err
	}

	dest := *out
	if info, statErr := os.Stat(dest); (statErr == nil && info.IsDir()) || strings.HasSuffix(dest, string(os.PathSeparator)) {
		dest = filepath.Join(dest, coords.FileName(coords.Version))
	}

	start := time.Now()
	used, resolved, err := repo.NewClient().FetchArtifact(coords, repos, dest)
	if err != nil {
		return err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return err
	}
	fmt.Printf("%s (%s) from %s -> %s (%d bytes, %s)\n",
		coords, resolved, used.ID, dest, info.Size(), time.Since(start).Round(time.Millisecond))
	return nil
}

// effectiveRepos computes the repository list for a command, honoring an
// optional [id::]url override that bypasses profile repositories while still
// resolving credentials from settings servers by ID.
func effectiveRepos(g *globalOpts, override string) ([]repo.RemoteRepo, *repo.Settings, error) {
	userPath := g.userSettings
	if userPath == "" {
		userPath = repo.DefaultUserSettingsPath()
	}
	globalPath := g.globalSettings
	if globalPath == "" {
		globalPath = repo.DefaultGlobalSettingsPath()
	}
	settings, _, err := repo.LoadSettings(userPath, globalPath)
	if err != nil {
		return nil, nil, err
	}
	if override != "" {
		id, url, found := strings.Cut(override, "::")
		if !found {
			id, url = "", override
		}
		rr := repo.RemoteRepo{ID: id, URL: strings.TrimRight(url, "/"), Releases: true, Snapshots: true}
		if rr.ID == "" {
			rr.ID = "override"
		}
		props := repo.EffectiveProps(repo.ActiveProfiles(settings, g.profiles, g.props), g.props)
		for _, srv := range settings.Servers {
			if srv.ID == id {
				rr.Username = repo.Interpolate(srv.Username, props)
				rr.Password = repo.Interpolate(srv.Password, props)
				break
			}
		}
		return []repo.RemoteRepo{rr}, settings, nil
	}
	return repo.EffectiveRepos(settings, g.profiles, g.props), settings, nil
}
