package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/freeeve/goven/internal/pom"
	"github.com/freeeve/goven/internal/repo"
)

func init() {
	register(&command{
		name:    "deploy",
		summary: "upload an artifact (deploy-file semantics, with SNAPSHOT metadata handling)",
		run:     runDeploy,
	})
}

// runDeploy uploads one artifact with Maven deploy:deploy-file semantics.
// Coordinates and target come from --gav/--repo flags or from the familiar
// -Dfile/-DgroupId/-DartifactId/-Dversion/-Durl property spelling, so
// existing mvn deploy:deploy-file invocations migrate by swapping the command
// name. A minimal POM is generated unless one is supplied or a classifier is
// set (override with -DgeneratePom=true/false).
func runDeploy(g *globalOpts, args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	gav := fs.String("gav", "", "coordinates groupId:artifactId:version[:type[:classifier]]")
	pomFile := fs.String("pom", "", "POM file to upload alongside the artifact")
	repoFlag := fs.String("repo", "", "target repository, as [id::]url (id looks up settings credentials)")
	serial := fs.Bool("serial", false, "upload files one at a time instead of concurrently")
	if err := fs.Parse(reorderArgs(args, map[string]bool{"gav": true, "pom": true, "repo": true})); err != nil {
		return err
	}

	file, coords, err := deployCoords(fs.Args(), *gav, g.props)
	if err != nil {
		return err
	}
	if *pomFile == "" {
		*pomFile = g.props["pomFile"]
	}
	target := *repoFlag
	if target == "" && g.props["url"] != "" {
		if id := g.props["repositoryId"]; id != "" {
			target = id + "::" + g.props["url"]
		} else {
			target = g.props["url"]
		}
	}
	if target == "" {
		return fmt.Errorf("no target repository: pass --repo [id::]url (or -DrepositoryId=... -Durl=...)")
	}

	repos, _, err := effectiveRepos(g, target)
	if err != nil {
		return err
	}
	dest := repos[0]

	pomBytes, err := deployPOM(coords, *pomFile, g.props)
	if err != nil {
		return err
	}

	start := time.Now()
	cl := repo.NewClient()
	cl.Sequential = *serial
	res, err := cl.Deploy(dest, coords, file, pomBytes, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("%s (%s) -> %s\n", coords, res.ResolvedVersion, dest.URL)
	for _, p := range res.Uploaded {
		fmt.Printf("  %s\n", p)
	}
	fmt.Printf("done in %s\n", time.Since(start).Round(time.Millisecond))
	return nil
}

// deployCoords determines the artifact file and coordinates from the
// positional argument plus --gav, or from -D properties.
func deployCoords(positional []string, gav string, props map[string]string) (string, repo.Coords, error) {
	file := props["file"]
	if len(positional) == 1 {
		file = positional[0]
	} else if len(positional) > 1 {
		return "", repo.Coords{}, fmt.Errorf("expected one artifact file, got %d", len(positional))
	}
	if file == "" {
		return "", repo.Coords{}, fmt.Errorf("no artifact file: pass it as an argument (or -Dfile=...)")
	}
	if _, err := os.Stat(file); err != nil {
		return "", repo.Coords{}, err
	}
	if gav != "" {
		c, err := repo.ParseCoords(gav)
		return file, c, err
	}
	g, a, v := props["groupId"], props["artifactId"], props["version"]
	if g == "" || a == "" || v == "" {
		return "", repo.Coords{}, fmt.Errorf("no coordinates: pass --gav g:a:v[:type[:classifier]] (or -DgroupId/-DartifactId/-Dversion)")
	}
	c := repo.Coords{GroupID: g, ArtifactID: a, Version: v, Type: "jar", Classifier: props["classifier"]}
	if p := props["packaging"]; p != "" {
		c.Type = p
	}
	return file, c, nil
}

// deployPOM decides what POM to upload: an explicit file, a generated minimal
// POM, or none. Unlike deploy:deploy-file, POM generation defaults OFF when a
// classifier is set, since the classifier-less POM path would overwrite the
// main artifact's POM; -DgeneratePom=true restores Maven's behavior.
func deployPOM(c repo.Coords, pomFile string, props map[string]string) ([]byte, error) {
	if pomFile != "" {
		return os.ReadFile(pomFile)
	}
	generate := c.Classifier == ""
	if v, ok := props["generatePom"]; ok {
		generate = !strings.EqualFold(v, "false")
	}
	if !generate {
		return nil, nil
	}
	return pom.Generate(c)
}
