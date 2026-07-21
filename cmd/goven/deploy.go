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
	var attachments attachList
	fs.Var(&attachments, "attach", "secondary artifact as file:classifier[:type] (repeatable)")
	if err := fs.Parse(reorderArgs(args, map[string]bool{"gav": true, "pom": true, "repo": true, "attach": true})); err != nil {
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
	if err := attachments.addSideFiles(g.props); err != nil {
		return err
	}

	start := time.Now()
	cl := repo.NewClient()
	cl.Sequential = *serial
	res, err := cl.Deploy(dest, coords, file, pomBytes, time.Now(), attachments...)
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

// attachList collects --attach flags ("file:classifier[:type]") and the
// deploy-file property spelling (-Dfiles/-Dclassifiers/-Dtypes comma lists).
type attachList []repo.Attachment

// String renders the list for flag help.
func (a *attachList) String() string {
	var parts []string
	for _, att := range *a {
		parts = append(parts, att.File+":"+att.Classifier+":"+att.Type)
	}
	return strings.Join(parts, ",")
}

// Set parses one file:classifier[:type] value, splitting from the right so
// the file path may itself contain separators.
func (a *attachList) Set(s string) error {
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return fmt.Errorf("attach %q: want file:classifier[:type]", s)
	}
	att := repo.Attachment{Type: "jar"}
	if len(parts) >= 3 && !strings.Contains(parts[len(parts)-1], "/") {
		att.Type = parts[len(parts)-1]
		att.Classifier = parts[len(parts)-2]
		att.File = strings.Join(parts[:len(parts)-2], ":")
	} else {
		att.Classifier = parts[len(parts)-1]
		att.File = strings.Join(parts[:len(parts)-1], ":")
	}
	if att.File == "" || att.Classifier == "" {
		return fmt.Errorf("attach %q: empty file or classifier", s)
	}
	*a = append(*a, att)
	return nil
}

// addSideFiles appends attachments given in deploy:deploy-file's property
// spelling: -Dfiles=a.jar,b.tar -Dclassifiers=sources,dist -Dtypes=jar,tar.
func (a *attachList) addSideFiles(props map[string]string) error {
	if props["files"] == "" {
		return nil
	}
	files := strings.Split(props["files"], ",")
	classifiers := strings.Split(props["classifiers"], ",")
	types := strings.Split(props["types"], ",")
	if len(classifiers) != len(files) || len(types) != len(files) {
		return fmt.Errorf("-Dfiles/-Dclassifiers/-Dtypes must have the same number of entries")
	}
	for i := range files {
		att := repo.Attachment{File: strings.TrimSpace(files[i]),
			Classifier: strings.TrimSpace(classifiers[i]), Type: strings.TrimSpace(types[i])}
		if att.Type == "" {
			att.Type = "jar"
		}
		if att.File == "" {
			return fmt.Errorf("-Dfiles entry %d is empty", i)
		}
		*a = append(*a, att)
	}
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
