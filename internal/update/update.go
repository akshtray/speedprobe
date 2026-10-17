// Package update answers "is there a newer speedprobe?" when, and only when,
// the user asks. Nothing in this package runs automatically (see README:
// no automatic update checks, no telemetry).
//
// The latest version comes from following GitHub's releases/latest redirect,
// which needs no API call and so is not subject to the anonymous API rate
// limit.
package update

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"

	"golang.org/x/mod/semver"
)

// EnvReleasesURL overrides the releases page, for tests.
const EnvReleasesURL = "SPEEDPROBE_RELEASES_URL"

const (
	defaultReleases = "https://github.com/akshtray/speedprobe/releases"
	Repo            = "akshtray/speedprobe"
)

// Status is the result of a check.
type Status struct {
	Installed string `json:"installed"`      // as built, e.g. "0.2.1" or "dev"
	Latest    string `json:"latest"`         // newest release, e.g. "0.3.0"
	Outdated  bool   `json:"outdated"`       // Installed is a release older than Latest
	Upgrade   string `json:"upgrade"`        // the command that upgrades this install
	Method    string `json:"install_method"` // how this copy appears to have been installed
}

// Latest resolves the newest release tag by following the redirect of the
// releases/latest page. It returns the version without the leading "v".
func Latest(ctx context.Context, client *http.Client) (string, error) {
	base := os.Getenv(EnvReleasesURL)
	if base == "" {
		base = defaultReleases
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, strings.TrimRight(base, "/")+"/latest", nil)
	if err != nil {
		return "", err
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return "", fmt.Errorf("unexpected response %s from the releases page", resp.Status)
	}
	loc := resp.Header.Get("Location")
	tag := path.Base(loc)
	if !strings.HasPrefix(tag, "v") || !semver.IsValid(tag) {
		return "", errors.New("could not find a release tag in the redirect: " + loc)
	}
	return strings.TrimPrefix(tag, "v"), nil
}

// Check compares installed against the newest release and works out the
// upgrade command for this installation.
func Check(ctx context.Context, client *http.Client, installed string) (Status, error) {
	latest, err := Latest(ctx, client)
	if err != nil {
		return Status{Installed: installed}, err
	}
	st := Status{Installed: installed, Latest: latest}
	if v := "v" + installed; semver.IsValid(v) {
		st.Outdated = semver.Compare(v, "v"+latest) < 0
	}
	exe, _ := os.Executable()
	st.Method, st.Upgrade = upgradeFor(runtime.GOOS, runtime.GOARCH, exe, installed, fileExists)
	return st, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// upgradeFor picks the upgrade command from how the binary got here.
func upgradeFor(goos, goarch, exe, installed string, exists func(string) bool) (method, cmd string) {
	const latest = "https://github.com/akshtray/speedprobe/releases/latest/download/"
	arch := goarch
	if goarch == "arm" {
		arch = "armv7"
	}
	switch {
	case installed == "dev" || strings.Contains(installed, "-next"):
		return "source", "go install github.com/akshtray/speedprobe/cmd/speedprobe@latest"
	case strings.Contains(exe, "/homebrew/") || strings.Contains(exe, "/Cellar/") || strings.Contains(exe, "linuxbrew"):
		return "brew", "brew upgrade akshtray/tap/speedprobe"
	case goos == "linux" && exists("/var/lib/dpkg/info/speedprobe.list"):
		name := "speedprobe_linux_" + arch + ".deb"
		return "deb", "curl -LO " + latest + name + " && sudo apt install ./" + name
	case goos == "linux" && exists("/var/lib/rpm"):
		name := "speedprobe_linux_" + arch + ".rpm"
		return "rpm", "curl -LO " + latest + name + " && sudo dnf install ./" + name
	case goos == "windows":
		return "zip", "download " + latest + "speedprobe_windows_" + arch + ".zip and replace speedprobe.exe"
	default:
		return "archive", "download " + latest + "speedprobe_" + goos + "_" + arch + ".tar.gz and replace the binary"
	}
}
