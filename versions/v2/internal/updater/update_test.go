package updater

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsNewerSemver(t *testing.T) {
	t.Parallel()

	cases := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"v2.0.0", "v1.1.0", true},
		{"v1.1.0", "v1.0.9", true},
		{"v2.0.1", "v2.0.0", true},
		{"v2.0.0", "v2.0.0", false},
		{"v2.0.0", "v2.0.1", false},
		{"v1.0.0", "v2.0.0", false},
		{"v2.0.0", "v2.0.0-rc1", true}, // release wins over its own pre-release
		{"v2.0.0-rc2", "v2.0.0-rc1", true},
	}
	for _, tc := range cases {
		got := isNewer(tc.candidate, tc.current)
		if got != tc.want {
			t.Errorf("isNewer(%q, %q) = %v; want %v", tc.candidate, tc.current, got, tc.want)
		}
	}
}

func TestCheckOnlyReportsAvailableUpdate(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v2.5.0","html_url":"https://example.invalid/r"}`))
	}))
	t.Cleanup(server.Close)

	var stdout bytes.Buffer
	cfg := Config{
		CurrentVersion: "v2.0.0",
		CheckOnly:      true,
		ReleasesAPI:    server.URL + "/releases",
		Stdout:         &stdout,
		Stderr:         &stdout,
		HTTPClient:     server.Client(),
	}
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !res.UpdateAvailable {
		t.Fatalf("expected UpdateAvailable=true; got %#v", res)
	}
	if res.Installed {
		t.Fatalf("CheckOnly should never install; got Installed=true")
	}
	if res.LatestVersion != "v2.5.0" {
		t.Fatalf("expected latest v2.5.0, got %q", res.LatestVersion)
	}
}

func TestUpToDateExitsCleanly(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v2.0.0","html_url":"https://example.invalid/r"}`))
	}))
	t.Cleanup(server.Close)

	var stdout bytes.Buffer
	cfg := Config{
		CurrentVersion: "v2.0.0",
		ReleasesAPI:    server.URL + "/releases",
		Stdout:         &stdout,
		Stderr:         &stdout,
		HTTPClient:     server.Client(),
	}
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.UpdateAvailable {
		t.Fatalf("expected UpdateAvailable=false")
	}
	if !strings.Contains(stdout.String(), "already up to date") {
		t.Fatalf("expected 'already up to date' message: %s", stdout.String())
	}
}

func TestCanonicalSemverNormalizesInput(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"v2.0.0":     "v2.0.0",
		"2.1.5":      "v2.1.5",
		"v2.0.0-rc1": "v2.0.0-rc1",
		" v1.0.0 ":   "v1.0.0",
		"":           "",
		"not-semver": "",
	}
	for input, want := range cases {
		got := canonicalSemver(input)
		if got != want {
			t.Errorf("canonicalSemver(%q) = %q; want %q", input, got, want)
		}
	}
}
