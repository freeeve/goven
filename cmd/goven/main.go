// Command goven is a fast Maven repository client: it fetches and inspects
// artifacts in Maven repositories using native settings.xml support, with no
// JVM required.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/freeeve/goven/internal/repo"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

// globalOpts carries options shared by every subcommand, mirroring the Maven
// flags users already know: settings files, active profiles, and -D properties.
type globalOpts struct {
	userSettings   string
	globalSettings string
	profiles       []string
	props          map[string]string
}

// command is a registered subcommand; implementations add themselves to the
// commands map from their file's init function.
type command struct {
	name    string
	summary string
	run     func(g *globalOpts, args []string) error
}

var commands = map[string]*command{}

func register(c *command) { commands[c.name] = c }

func main() {
	repo.Version = version
	g, rest, err := parseGlobal(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "goven:", err)
		os.Exit(2)
	}
	if len(rest) == 0 {
		usage()
		os.Exit(2)
	}
	name := rest[0]
	switch name {
	case "help", "-h", "--help":
		usage()
		return
	case "version", "--version":
		fmt.Println("goven", version)
		return
	}
	c, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "goven: unknown command %q\n\n", name)
		usage()
		os.Exit(2)
	}
	if err := c.run(g, rest[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "goven:", err)
		os.Exit(1)
	}
}

// parseGlobal extracts the Maven-style global flags, accepting both
// "-Dkey=val" and "-P profile" spellings. Attached forms (-Dkey=val,
// -Pprofiles) are collected from anywhere in the argument list, as Maven
// users habitually place them after the goal; flags that consume a separate
// value (-s, -gs, -P) must precede the subcommand, where parsing stops at the
// first unrecognized token.
func parseGlobal(args []string) (*globalOpts, []string, error) {
	g := &globalOpts{props: map[string]string{}}
	var kept []string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "-D") && len(a) > 2:
			k, v, _ := strings.Cut(a[2:], "=")
			g.props[k] = v
		case strings.HasPrefix(a, "-P") && len(a) > 2:
			g.profiles = append(g.profiles, splitList(a[2:])...)
		default:
			kept = append(kept, a)
		}
	}
	args = kept
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "-P", "--activate-profiles":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s requires an argument", a)
			}
			i++
			g.profiles = append(g.profiles, splitList(args[i])...)
		case "-s", "--settings":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s requires an argument", a)
			}
			i++
			g.userSettings = args[i]
		case "-gs", "--global-settings":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s requires an argument", a)
			}
			i++
			g.globalSettings = args[i]
		default:
			return g, args[i:], nil
		}
		i++
	}
	return g, nil, nil
}

// reorderArgs moves flags ahead of positional arguments so commands accept
// "goven get g:a:v -o dir" as well as "goven get -o dir g:a:v", which stdlib
// flag parsing alone does not. valueFlags names flags that consume the next
// argument when not written as -flag=value.
func reorderArgs(args []string, valueFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if !strings.Contains(name, "=") && valueFlags[name] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

// splitList splits a comma-separated flag value, dropping empty entries.
func splitList(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func usage() {
	fmt.Fprintf(os.Stderr, `goven %s - fast Maven repository client (no JVM)

Usage:
  goven [global flags] <command> [command flags] [args]

Global flags (before the command; -D may appear anywhere):
  -s <file>       user settings.xml (default ~/.m2/settings.xml)
  -gs <file>      global settings.xml (default $M2_HOME/conf/settings.xml)
  -P <profiles>   comma-separated profiles to activate ("!" deactivates)
  -Dkey=value     set a property (repeatable)

Commands:
`, version)
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(os.Stderr, "  %-10s %s\n", n, commands[n].summary)
	}
	fmt.Fprintf(os.Stderr, "  %-10s %s\n  %-10s %s\n", "version", "print the goven version", "help", "show this help")
}
