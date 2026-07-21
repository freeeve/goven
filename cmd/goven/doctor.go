package main

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/freeeve/goven/internal/repo"
)

func init() {
	register(&command{
		name:    "doctor",
		summary: "diagnose settings, effective repositories, reachability, and toolchain",
		run:     runDoctor,
	})
}

// runDoctor reports the loaded settings files, active profiles, effective
// repositories (credentials masked), repository reachability with the
// configured credentials, and the local Maven/JDK toolchain. It returns an
// error when any repository check fails, so CI can gate on it.
func runDoctor(g *globalOpts, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("doctor takes no arguments")
	}
	userPath := g.userSettings
	if userPath == "" {
		userPath = repo.DefaultUserSettingsPath()
	}
	globalPath := g.globalSettings
	if globalPath == "" {
		globalPath = repo.DefaultGlobalSettingsPath()
	}
	securityPath := g.props["settings.security"]
	if securityPath == "" {
		securityPath = repo.DefaultSecurityPath()
	}
	settings, loaded, err := repo.LoadSettings(userPath, globalPath, securityPath)
	if err != nil {
		return err
	}
	fmt.Println("settings files:")
	if len(loaded) == 0 {
		fmt.Println("  (none found; using built-in defaults)")
	}
	for _, f := range loaded {
		fmt.Printf("  %s\n", f)
	}

	active := repo.ActiveProfiles(settings, g.profiles, g.props)
	var ids []string
	for _, p := range active {
		ids = append(ids, p.ID)
	}
	fmt.Printf("active profiles: %s\n", orNone(strings.Join(ids, ", ")))

	repos, err := repo.EffectiveRepos(settings, g.profiles, g.props)
	if err != nil {
		return err
	}
	fmt.Println("effective repositories:")
	failures := 0
	for _, r := range repos {
		fmt.Printf("  %s\n", r)
		status, latency, err := checkRepo(r)
		switch {
		case err != nil:
			failures++
			fmt.Printf("    unreachable: %v\n", err)
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			failures++
			fmt.Printf("    HTTP %d in %s: credentials rejected (server id must match %q)\n",
				status, latency.Round(time.Millisecond), credID(r))
		default:
			fmt.Printf("    reachable: HTTP %d in %s\n", status, latency.Round(time.Millisecond))
		}
	}

	fmt.Println("toolchain:")
	fmt.Printf("  mvn:  %s\n", toolVersion("mvn", "-v"))
	fmt.Printf("  java: %s\n", toolVersion("java", "-version"))

	if failures > 0 {
		return fmt.Errorf("%d repository check(s) failed", failures)
	}
	return nil
}

// checkRepo probes a repository root with the configured credentials and
// reports the HTTP status and latency. Any HTTP response counts as reachable.
func checkRepo(r repo.RemoteRepo) (int, time.Duration, error) {
	req, err := http.NewRequest(http.MethodGet, r.URL+"/", nil)
	if err != nil {
		return 0, 0, err
	}
	if r.Username != "" {
		req.SetBasicAuth(r.Username, r.Password)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, time.Since(start), err
	}
	resp.Body.Close()
	return resp.StatusCode, time.Since(start), nil
}

// credID names the server ID whose credentials apply to a repository (the
// mirror ID when mirrored).
func credID(r repo.RemoteRepo) string {
	if r.MirrorID != "" {
		return r.MirrorID
	}
	return r.ID
}

// toolVersion runs a tool's version command and returns its first output
// line, or a not-found note. Both stdout and stderr are consulted because
// java -version writes to stderr.
func toolVersion(tool string, arg string) string {
	path, err := exec.LookPath(tool)
	if err != nil {
		return "not found on PATH"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, path, arg).CombinedOutput()
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if line == "" {
		return path
	}
	return line + " (" + path + ")"
}

// orNone substitutes a placeholder for an empty display string.
func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
