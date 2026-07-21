package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/freeeve/goven/internal/repo"
)

func init() {
	register(&command{
		name:    "copy",
		summary: "promote a release artifact from one repository to another",
		run:     runCopy,
	})
}

// runCopy promotes a release (artifact + POM, checksums regenerated,
// target metadata updated) between repositories, e.g. staging to releases.
func runCopy(g *globalOpts, args []string) error {
	fs := flag.NewFlagSet("copy", flag.ContinueOnError)
	from := fs.String("from", "", "source repository, as [id::]url (id looks up settings credentials)")
	to := fs.String("to", "", "target repository, as [id::]url")
	if err := fs.Parse(reorderArgs(args, map[string]bool{"from": true, "to": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 || *from == "" || *to == "" {
		return fmt.Errorf("usage: goven copy --from [id::]url --to [id::]url <groupId:artifactId:version[:type[:classifier]]>")
	}
	coords, err := repo.ParseCoords(fs.Arg(0))
	if err != nil {
		return err
	}
	srcRepos, _, err := effectiveRepos(g, *from)
	if err != nil {
		return err
	}
	dstRepos, _, err := effectiveRepos(g, *to)
	if err != nil {
		return err
	}

	start := time.Now()
	res, err := repo.NewClient().Copy(srcRepos[0], dstRepos[0], coords, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s -> %s\n", coords, srcRepos[0].URL, dstRepos[0].URL)
	for _, p := range res.Uploaded {
		fmt.Printf("  %s\n", p)
	}
	fmt.Printf("done in %s\n", time.Since(start).Round(time.Millisecond))
	return nil
}
