package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/freeeve/goven/internal/repo"
)

func init() {
	register(&command{
		name:    "latest",
		summary: "print the latest/release versions of groupId:artifactId",
		run:     runLatest,
	})
	register(&command{
		name:    "exists",
		summary: "check whether an artifact version exists (exit 0/1)",
		run:     runExists,
	})
}

// runLatest prints the artifact-level version metadata from the first
// effective repository that has it.
func runLatest(g *globalOpts, args []string) error {
	fs := flag.NewFlagSet("latest", flag.ContinueOnError)
	repoOverride := fs.String("repo", "", "query only this repository, as [id::]url")
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(reorderArgs(args, map[string]bool{"repo": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: goven latest [--json] [--repo [id::]url] <groupId:artifactId>")
	}
	groupID, artifactID, ok := strings.Cut(fs.Arg(0), ":")
	if !ok || groupID == "" || artifactID == "" || strings.Contains(artifactID, ":") {
		return fmt.Errorf("want groupId:artifactId, got %q", fs.Arg(0))
	}

	repos, _, err := effectiveRepos(g, *repoOverride)
	if err != nil {
		return err
	}
	cl := repo.NewClient()
	var errs []error
	for _, r := range repos {
		m, err := cl.FetchArtifactMetadata(r, groupID, artifactID)
		if errors.Is(err, repo.ErrNotFound) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"groupId":    groupID,
				"artifactId": artifactID,
				"repository": r.ID,
				"latest":     m.Versioning.Latest,
				"release":    m.Versioning.Release,
				"versions":   versionsOf(m),
			})
		}
		fmt.Printf("repository: %s\n", r.ID)
		if m.Versioning.Release != "" {
			fmt.Printf("release: %s\n", m.Versioning.Release)
		}
		if m.Versioning.Latest != "" {
			fmt.Printf("latest: %s\n", m.Versioning.Latest)
		}
		fmt.Printf("versions: %s\n", strings.Join(versionsOf(m), ", "))
		return nil
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return fmt.Errorf("%s:%s: no metadata in any effective repository", groupID, artifactID)
}

// versionsOf returns the artifact metadata's version list, empty-safe.
func versionsOf(m *repo.Metadata) []string {
	if m.Versioning.Versions == nil {
		return nil
	}
	return m.Versioning.Versions.Version
}

// runExists checks artifact presence (SNAPSHOT-resolved) and exits non-zero
// when absent, making it usable as a CI guard against double deploys.
func runExists(g *globalOpts, args []string) error {
	fs := flag.NewFlagSet("exists", flag.ContinueOnError)
	repoOverride := fs.String("repo", "", "query only this repository, as [id::]url")
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(reorderArgs(args, map[string]bool{"repo": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: goven exists [--json] [--repo [id::]url] <groupId:artifactId:version[:type[:classifier]]>")
	}
	coords, err := repo.ParseCoords(fs.Arg(0))
	if err != nil {
		return err
	}
	repos, _, err := effectiveRepos(g, *repoOverride)
	if err != nil {
		return err
	}
	cl := repo.NewClient()
	var errs []error
	for _, r := range repos {
		if coords.IsSnapshot() && !r.Snapshots || !coords.IsSnapshot() && !r.Releases {
			continue
		}
		found, resolved, err := cl.Exists(r, coords)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if found {
			if *asJSON {
				return json.NewEncoder(os.Stdout).Encode(map[string]any{
					"exists": true, "repository": r.ID, "resolvedVersion": resolved,
				})
			}
			fmt.Printf("%s exists in %s (%s)\n", coords, r.ID, resolved)
			return nil
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if *asJSON {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"exists": false})
	}
	return fmt.Errorf("%s: not found in any effective repository", coords)
}
