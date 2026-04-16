// Package updater implements the `kerebrom update` self-upgrade flow.
//
// It checks GitHub for the latest published release of the kerebrom
// repository, downloads the source tarball for that tag, extracts it into
// a temporary directory, and runs `make install-user` from
// versions/v2/. The new binary atomically replaces the current one (via
// the install-user target's tmp+mv pattern), and on macOS/Linux the
// running process keeps executing from its open file handle until exit
// — so the upgrade is safe to invoke from the binary upgrading itself.
package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	defaultRepo            = "ulianbass/kerebrom"
	defaultUserAgent       = "kerebrom-updater"
	defaultRequestTimeout  = 30 * time.Second
	defaultDownloadTimeout = 5 * time.Minute
)

// Config controls a single update run.
type Config struct {
	// Repo is the GitHub "owner/name" coordinate. Defaults to ulianbass/kerebrom.
	Repo string
	// CurrentVersion is the semver string the running binary reports
	// (for example "v2.0.0").
	CurrentVersion string
	// VersionRoot is the path inside the extracted tarball that contains
	// the Makefile to install (for example "versions/v2"). Defaults to
	// "versions/v2".
	VersionRoot string
	// IncludePreReleases also considers pre-release tags when picking the
	// latest version. Default: false.
	IncludePreReleases bool
	// CheckOnly reports whether an update exists without installing it.
	CheckOnly bool
	// AssumeYes skips the interactive confirmation prompt.
	AssumeYes bool
	// Stdin / Stdout / Stderr override the standard streams. Useful for
	// tests and for running inside non-tty environments.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// HTTPClient overrides the default HTTP client. Useful for tests.
	HTTPClient *http.Client
	// ReleasesAPI overrides the GitHub Releases endpoint. Useful for
	// tests to point at a fake server. Defaults to
	// https://api.github.com/repos/<repo>/releases.
	ReleasesAPI string
	// TarballURLTemplate overrides the source tarball URL. Receives the
	// repo and tag and must return a valid URL. Defaults to
	// https://github.com/<repo>/archive/refs/tags/<tag>.tar.gz.
	TarballURLTemplate func(repo, tag string) string
	// MakeTarget is the Makefile target invoked after extraction.
	// Defaults to "install-user".
	MakeTarget string
}

// Result is what an update run returns.
type Result struct {
	CurrentVersion string
	LatestVersion  string
	UpdateAvailable bool
	Installed       bool
	InstallDir      string
	Skipped         bool   // true when an update was available but the user declined
	SkippedReason   string // human-readable reason for Skipped
}

// release is the subset of the GitHub Releases JSON we care about.
type release struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	Draft      bool   `json:"draft"`
	PreRelease bool   `json:"prerelease"`
	HTMLURL    string `json:"html_url"`
}

// Run performs a full update cycle: check, download, install. It is the
// only entry point a CLI command needs to call.
func Run(ctx context.Context, cfg Config) (Result, error) {
	cfg = withDefaults(cfg)
	res := Result{CurrentVersion: cfg.CurrentVersion}

	latest, err := fetchLatestRelease(ctx, cfg)
	if err != nil {
		return res, err
	}
	res.LatestVersion = latest.TagName
	res.UpdateAvailable = isNewer(latest.TagName, cfg.CurrentVersion)

	if !res.UpdateAvailable {
		fmt.Fprintf(cfg.Stdout, "kerebrom is already up to date (current=%s, latest=%s).\n", cfg.CurrentVersion, latest.TagName)
		return res, nil
	}

	fmt.Fprintf(cfg.Stdout, "kerebrom update available: %s → %s\n", cfg.CurrentVersion, latest.TagName)
	fmt.Fprintf(cfg.Stdout, "release notes: %s\n", latest.HTMLURL)

	if cfg.CheckOnly {
		return res, nil
	}

	if !cfg.AssumeYes {
		if !confirm(cfg, fmt.Sprintf("Install %s now?", latest.TagName)) {
			res.Skipped = true
			res.SkippedReason = "user declined"
			fmt.Fprintln(cfg.Stdout, "update skipped.")
			return res, nil
		}
	}

	tmpDir, err := os.MkdirTemp("", "kerebrom-update-*")
	if err != nil {
		return res, fmt.Errorf("create tmp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tarballURL := cfg.TarballURLTemplate(cfg.Repo, latest.TagName)
	fmt.Fprintf(cfg.Stdout, "downloading %s\n", tarballURL)
	tarPath := filepath.Join(tmpDir, "source.tar.gz")
	if err := downloadFile(ctx, cfg, tarballURL, tarPath); err != nil {
		return res, fmt.Errorf("download release tarball: %w", err)
	}

	fmt.Fprintln(cfg.Stdout, "extracting source")
	extractedRoot, err := extractTarball(tarPath, tmpDir)
	if err != nil {
		return res, fmt.Errorf("extract tarball: %w", err)
	}

	installDir := filepath.Join(extractedRoot, cfg.VersionRoot)
	res.InstallDir = installDir

	fmt.Fprintf(cfg.Stdout, "running make %s in %s\n", cfg.MakeTarget, installDir)
	if err := runMake(ctx, cfg, installDir); err != nil {
		return res, fmt.Errorf("install via make: %w", err)
	}

	res.Installed = true
	fmt.Fprintf(cfg.Stdout, "kerebrom installed: %s\n", latest.TagName)
	fmt.Fprintln(cfg.Stdout, "restart any running clients (Claude Desktop, Code, Codex, etc.) to pick up the new MCP server.")
	return res, nil
}

func withDefaults(cfg Config) Config {
	if strings.TrimSpace(cfg.Repo) == "" {
		cfg.Repo = defaultRepo
	}
	if strings.TrimSpace(cfg.VersionRoot) == "" {
		cfg.VersionRoot = "versions/v2"
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.Stdin == nil {
		cfg.Stdin = os.Stdin
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultDownloadTimeout}
	}
	if strings.TrimSpace(cfg.ReleasesAPI) == "" {
		cfg.ReleasesAPI = fmt.Sprintf("https://api.github.com/repos/%s/releases", cfg.Repo)
	}
	if cfg.TarballURLTemplate == nil {
		cfg.TarballURLTemplate = func(repo, tag string) string {
			return fmt.Sprintf("https://github.com/%s/archive/refs/tags/%s.tar.gz", repo, url.PathEscape(tag))
		}
	}
	if strings.TrimSpace(cfg.MakeTarget) == "" {
		cfg.MakeTarget = "install-user"
	}
	return cfg
}

func fetchLatestRelease(ctx context.Context, cfg Config) (release, error) {
	endpoint := cfg.ReleasesAPI
	if !cfg.IncludePreReleases {
		// /releases/latest skips drafts and pre-releases server-side.
		endpoint = strings.TrimRight(endpoint, "/") + "/latest"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("query github releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("github releases returned %s", resp.Status)
	}

	if cfg.IncludePreReleases {
		var list []release
		if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
			return release{}, fmt.Errorf("decode releases list: %w", err)
		}
		for _, r := range list {
			if r.Draft {
				continue
			}
			return r, nil
		}
		return release{}, errors.New("no releases found")
	}

	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return release{}, fmt.Errorf("decode latest release: %w", err)
	}
	if strings.TrimSpace(r.TagName) == "" {
		return release{}, errors.New("latest release has empty tag")
	}
	return r, nil
}

func downloadFile(ctx context.Context, cfg Config, src string, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", resp.Status)
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	return nil
}

// extractTarball expands a gzip-compressed tar archive into dest. It
// returns the absolute path of the single top-level directory that
// GitHub source tarballs always contain (e.g. kerebrom-v2.0.0/).
func extractTarball(archivePath string, dest string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var topLevel string
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}

		clean := filepath.Clean(header.Name)
		if strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." || filepath.IsAbs(clean) {
			return "", fmt.Errorf("unsafe tar entry %q", header.Name)
		}
		if topLevel == "" {
			parts := strings.SplitN(clean, string(filepath.Separator), 2)
			topLevel = parts[0]
		}

		target := filepath.Join(dest, clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)|0o700); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode)|0o600)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return "", err
			}
			if err := out.Close(); err != nil {
				return "", err
			}
		case tar.TypeSymlink:
			// Source tarballs from GitHub may include symlinks; preserve them safely.
			linkTarget := filepath.Clean(header.Linkname)
			if filepath.IsAbs(linkTarget) || strings.HasPrefix(linkTarget, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("unsafe symlink %q -> %q", header.Name, header.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			_ = os.Remove(target)
			if err := os.Symlink(linkTarget, target); err != nil {
				return "", err
			}
		default:
			// Skip block, char, fifo, socket, etc.
		}
	}
	if topLevel == "" {
		return "", errors.New("empty tarball")
	}
	return filepath.Join(dest, topLevel), nil
}

func runMake(ctx context.Context, cfg Config, dir string) error {
	cmd := exec.CommandContext(ctx, "make", cfg.MakeTarget)
	cmd.Dir = dir
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr
	cmd.Env = append(os.Environ(), "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("make %s in %s (requires 'make' and 'go' in PATH): %w", cfg.MakeTarget, dir, err)
	}
	return nil
}

func confirm(cfg Config, prompt string) bool {
	fmt.Fprintf(cfg.Stdout, "%s [y/N]: ", prompt)
	buf := make([]byte, 16)
	n, _ := cfg.Stdin.Read(buf)
	answer := strings.ToLower(strings.TrimSpace(string(buf[:n])))
	return answer == "y" || answer == "yes"
}

// isNewer reports whether candidate is a strictly newer semver than current.
// Inputs without a leading "v" are normalized; invalid semver strings sort
// to zero and are treated as not-newer.
func isNewer(candidate string, current string) bool {
	return semver.Compare(canonicalSemver(candidate), canonicalSemver(current)) > 0
}

// canonicalSemver normalizes a version into the leading-"v" form expected
// by golang.org/x/mod/semver. Returns "" for unparseable input so the
// stdlib comparator treats it as the lowest possible version.
func canonicalSemver(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	if !semver.IsValid(s) {
		return ""
	}
	return s
}
