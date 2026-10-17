package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func releases(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest" {
			http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	t.Setenv(EnvReleasesURL, srv.URL+"/releases")
	return srv
}

func TestLatestFollowsRedirect(t *testing.T) {
	srv := releases(t, "v0.3.0")
	defer srv.Close()
	got, err := Latest(context.Background(), http.DefaultClient)
	if err != nil || got != "0.3.0" {
		t.Fatalf("Latest = %q, %v", got, err)
	}
}

func TestLatestRejectsNonTag(t *testing.T) {
	srv := releases(t, "oops")
	defer srv.Close()
	if _, err := Latest(context.Background(), http.DefaultClient); err == nil {
		t.Fatal("expected an error for a non-semver redirect")
	}
}

func TestCheckCompares(t *testing.T) {
	srv := releases(t, "v0.3.0")
	defer srv.Close()
	for installed, outdated := range map[string]bool{"0.2.1": true, "0.3.0": false, "0.4.0-rc1": false, "dev": false} {
		st, err := Check(context.Background(), http.DefaultClient, installed)
		if err != nil {
			t.Fatal(err)
		}
		if st.Outdated != outdated || st.Latest != "0.3.0" || st.Upgrade == "" {
			t.Errorf("installed %q: %+v", installed, st)
		}
	}
}

func TestUpgradeFor(t *testing.T) {
	none := func(string) bool { return false }
	deb := func(p string) bool { return p == "/var/lib/dpkg/info/speedprobe.list" }
	rpm := func(p string) bool { return p == "/var/lib/rpm" }
	cases := []struct {
		goos, goarch, exe, installed string
		exists                       func(string) bool
		method                       string
	}{
		{"darwin", "arm64", "/opt/homebrew/bin/speedprobe", "0.2.1", none, "brew"},
		{"linux", "amd64", "/home/linuxbrew/.linuxbrew/bin/speedprobe", "0.2.1", none, "brew"},
		{"linux", "amd64", "/usr/bin/speedprobe", "0.2.1", deb, "deb"},
		{"linux", "arm", "/usr/bin/speedprobe", "0.2.1", rpm, "rpm"},
		{"windows", "amd64", `C:\tools\speedprobe.exe`, "0.2.1", none, "zip"},
		{"freebsd", "amd64", "/usr/local/bin/speedprobe", "0.2.1", none, "archive"},
		{"linux", "amd64", "/home/x/go/bin/speedprobe", "dev", deb, "source"},
	}
	for _, c := range cases {
		method, cmd := upgradeFor(c.goos, c.goarch, c.exe, c.installed, c.exists)
		if method != c.method || cmd == "" {
			t.Errorf("%s/%s %s: method=%q cmd=%q", c.goos, c.goarch, c.exe, method, cmd)
		}
		if c.goarch == "arm" && !strings.Contains(cmd, "armv7") {
			t.Errorf("arm should map to armv7: %s", cmd)
		}
	}
}
