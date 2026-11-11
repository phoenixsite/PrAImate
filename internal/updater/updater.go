// Package updater checks GitHub Releases for a newer praimate build,
// downloads the matching archive for the current OS/arch, extracts the
// binary, and swaps it in place of the running executable.
//
// Designed to be called from the maintenance CLI via the -update /
// -check-update flags. Never runs implicitly.
package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/version"
)

// Release is the slice of the GitHub /releases/latest payload we need.
type Release struct {
	TagName    string  `json:"tag_name"`
	Name       string  `json:"name"`
	HTMLURL    string  `json:"html_url"`
	Body       string  `json:"body"`
	Assets     []Asset `json:"assets"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	// AssetsIncomplete means the API was unavailable and the web fallback
	// could only infer the main archive, not enumerate standalone assets.
	AssetsIncomplete bool `json:"-"`
}

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// httpClient is shared so callers can swap it in tests. Default timeout
// is generous because release archives can be tens of MB.
var httpClient = &http.Client{Timeout: 60 * time.Second}

// LatestURL is the GitHub API endpoint queried for the most recent
// release of the configured repo.
func LatestURL() string {
	if version.Edition == "server" {
		return "https://api.github.com/repos/" + version.Repo + "/releases?per_page=100"
	}
	return version.ReleaseLatestAPIURL
}

// FetchLatest hits the GitHub API and returns the most recent release.
// Returns a not-found error if the repo has no releases yet (GitHub
// answers 404 in that case).
func FetchLatest() (*Release, error) {
	req, err := http.NewRequest("GET", LatestURL(), nil)
	if err == nil {
		req.Header.Set("Accept", "application/vnd.github.v3+json")
		req.Header.Set("User-Agent", "praimate-updater/"+version.Current)
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			req.Header.Set("Authorization", "token "+token)
		}

		resp, err := httpClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				if version.Edition == "server" {
					var releases []Release
					if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&releases); err != nil {
						return nil, fmt.Errorf("decode server releases: %w", err)
					}
					var latest *Release
					for i := range releases {
						r := &releases[i]
						serverRelease := strings.HasPrefix(r.TagName, "server-v")
						if strings.HasPrefix(r.TagName, "v") {
							for _, asset := range r.Assets {
								if strings.HasPrefix(asset.Name, "praimate-server-") && (strings.HasSuffix(asset.Name, ".tar.gz") || strings.HasSuffix(asset.Name, ".zip")) {
									serverRelease = true
									break
								}
							}
						}
						if !r.Draft && serverRelease &&
							(latest == nil || IsNewer(r.TagName, latest.TagName)) {
							latest = r
						}
					}
					if latest == nil {
						return nil, errors.New("no published PrAImate server release found; desktop releases cannot update this edition")
					}
					return latest, nil
				}
				var rel Release
				if jerr := json.NewDecoder(resp.Body).Decode(&rel); jerr == nil && strings.HasPrefix(rel.TagName, "v") && !rel.Draft && !rel.Prerelease {
					return &rel, nil
				}
			}
		}
	}
	if version.Edition == "server" {
		return nil, errors.New("unable to query PrAImate server releases; refusing desktop channel fallback")
	}

	// Fall back to web redirect on /releases/latest (immune to GitHub API rate limits / 403 Forbidden)
	if fallbackRel, ferr := fetchLatestWebFallback(); ferr == nil && fallbackRel != nil {
		return fallbackRel, nil
	}

	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	return nil, errors.New("unable to resolve latest release from GitHub API or web endpoint")
}

func fetchLatestWebFallback() (*Release, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 15 * time.Second,
	}
	resp, err := client.Get(version.RepoURL + "/releases/latest")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return nil, errors.New("no redirect location returned")
	}
	idx := strings.LastIndex(loc, "/tag/")
	if idx == -1 {
		return nil, errors.New("invalid release redirect URL")
	}
	tag := strings.TrimSpace(loc[idx+len("/tag/"):])
	if tag == "" {
		return nil, errors.New("empty tag in release redirect")
	}
	if !strings.HasPrefix(tag, "v") {
		return nil, errors.New("latest release does not belong to the desktop channel")
	}
	triplet := runtime.GOOS + "-" + runtime.GOARCH
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	assetName := fmt.Sprintf("praimate-%s%s", triplet, ext)
	downloadURL := fmt.Sprintf("%s/releases/download/%s/%s", version.RepoURL, tag, assetName)

	return &Release{
		TagName:          tag,
		Name:             "PrAImate " + tag,
		HTMLURL:          fmt.Sprintf("%s/releases/tag/%s", version.RepoURL, tag),
		AssetsIncomplete: true,
		Assets: []Asset{
			{
				Name:               assetName,
				BrowserDownloadURL: downloadURL,
			},
		},
	}, nil
}

// IsNewer reports whether remote (e.g. "v0.2.0") is strictly newer than
// local (e.g. "0.1.0"). Leading "v" is tolerated on either side. A
// non-parseable segment is treated as zero so we never panic on a tag
// like "v0.2.0-rc1" — that gets compared as 0.2.0 and the user can
// still see the tag in the printout.
func IsNewer(remoteTag, localVer string) bool {
	r := parseSemver(remoteTag)
	l := parseSemver(localVer)
	for i := 0; i < 3; i++ {
		if r[i] != l[i] {
			return r[i] > l[i]
		}
	}
	return false
}

func parseSemver(s string) [3]int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "server-")
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	// Strip pre-release suffix ("-rc1", "-beta.2") before splitting.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.SplitN(s, ".", 3)
	var out [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		n, err := strconv.Atoi(p)
		if err == nil {
			out[i] = n
		}
	}
	return out
}

// AssetForHost picks the asset whose filename matches the current OS
// and architecture. Windows builds ship as .zip, everything else as
// .tar.gz — matching scripts/build.sh's naming.
func AssetForHost(rel *Release) (*Asset, error) {
	return assetForPlatform(rel, runtime.GOOS, runtime.GOARCH)
}

func assetForPlatform(rel *Release, goos, goarch string) (*Asset, error) {
	if rel == nil {
		return nil, errors.New("no release selected")
	}
	if goos != "linux" && goos != "windows" && goos != "darwin" {
		return nil, fmt.Errorf("unsupported operating system %s; PrAImate supports Linux, Windows, and macOS only", goos)
	}
	triplet := goos + "-" + goarch
	wantExt := ".tar.gz"
	if goos == "windows" {
		wantExt = ".zip"
	}
	canonical := version.AssetPrefix + "-" + triplet + wantExt
	legacy := version.AssetPrefix + "-" + strings.TrimPrefix(strings.TrimPrefix(rel.TagName, "server-"), "v") + "-" + triplet + wantExt
	for _, name := range []string{canonical, legacy} {
		for i := range rel.Assets {
			if rel.Assets[i].Name == name {
				return &rel.Assets[i], nil
			}
		}
	}
	return nil, fmt.Errorf("no release asset for %s found in %s", triplet, rel.TagName)
}

// Apply downloads the asset, extracts the praimate binary, and swaps it
// in place of the currently-running executable. On success the caller
// should print a "restart now" line and return — the next invocation
// runs the new binary.
//
// On Windows the running .exe can be renamed but not deleted; we
// rename the live binary to <name>.old and place the new one at the
// original path. The .old artifact is harmless and gets overwritten
// on the next update.
func Apply(asset *Asset, progress func(stage string)) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own executable: %w", err)
	}
	exePath, _ = filepath.EvalSymlinks(exePath)
	return applyAt(exePath, asset, progress)
}

func applyAt(exePath string, asset *Asset, progress func(stage string)) error {
	if progress == nil {
		progress = func(string) {}
	}
	if err := validateUpdateAsset(asset); err != nil {
		return err
	}
	if err := validateInstalledEdition(filepath.Dir(exePath)); err != nil {
		return err
	}

	if err := checkUpdateDirectory(exePath); err != nil {
		return err
	}
	progress("downloading " + asset.Name)
	archivePath, err := downloadToTemp(asset)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)
	if err := validateArchiveEdition(archivePath); err != nil {
		return fmt.Errorf("validate update edition: %w", err)
	}

	progress("extracting binary")
	binName := filepath.Base(exePath)
	if binName == "" || binName == "." {
		binName = "praimate"
		if runtime.GOOS == "windows" {
			binName = "praimate.exe"
		}
	}
	stagedBin, err := extractBinary(archivePath, binName)
	if err != nil {
		return err
	}
	defer os.Remove(stagedBin)

	progress("installing")
	if err := swapBinary(exePath, stagedBin); err != nil {
		if os.IsPermission(err) || strings.Contains(err.Error(), "permission denied") {
			return fmt.Errorf("insufficient permissions to overwrite %s: directory is owned by root/system (run 'sudo praimate -update' in a terminal, or install in ~/.local/bin)", exePath)
		}
		return fmt.Errorf("swap binary: %w", err)
	}

	// Refresh the sibling binaries shipped in the same archive
	// (praimate, praimate-gui, wpc, praimate-code) so `praimate -update` or GUI
	// updater keeps them in step with the main binary — matching what the installer does.
	exeDir := filepath.Dir(exePath)
	allSiblings := []string{"praimate", "praimate-gui", "wpc", "praimate-code", "praimate-code-baseline", "praimate-cli"}
	for _, sib := range allSiblings {
		name := sib
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if name == binName {
			continue
		}
		dst := filepath.Join(exeDir, name)
		// Only refresh a sibling the user actually has installed.
		if _, err := os.Stat(dst); err != nil {
			continue
		}
		staged, err := extractBinary(archivePath, name)
		if err != nil {
			// Not in this archive (other platforms) — leave the existing one.
			continue
		}
		progress("updating " + sib)
		if err := swapBinary(dst, staged); err != nil {
			os.Remove(staged)
			// Non-fatal: the main binary already updated.
			progress("warning: could not update " + sib + ": " + err.Error())
			continue
		}
		os.Remove(staged)
	}
	if err := recordInstalledEdition(exeDir); err != nil {
		return fmt.Errorf("binaries updated but the edition marker could not be saved: %w", err)
	}
	return nil
}

func downloadToTemp(asset *Asset) (string, error) {
	req, err := http.NewRequest("GET", asset.BrowserDownloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "praimate-updater/"+version.Current)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	f, err := os.CreateTemp("", "praimate-update-*"+filepath.Ext(asset.Name))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// extractBinary pulls just the praimate executable out of the archive,
// writes it to a temp file marked executable, and returns the path.
// Archive layout (from scripts/build.sh): <triplet>/<binName> at the
// top level — we accept the binary at any depth though, in case the
// layout shifts.
func extractBinary(archivePath, binName string) (string, error) {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractFromZip(archivePath, binName)
	}
	return extractFromTarGz(archivePath, binName)
}

func extractFromZip(archivePath, binName string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		// Normalize separators first: zips produced by PowerShell's
		// Compress-Archive carry backslash entry names, which
		// path.Base (forward-slash semantics) doesn't split.
		name := strings.ReplaceAll(zf.Name, `\`, "/")
		if path.Base(name) != binName {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return "", err
		}
		out, err := writeStagedBinary(rc, binName)
		rc.Close()
		return out, err
	}
	return "", fmt.Errorf("%s not found in archive", binName)
}

func extractFromTarGz(archivePath, binName string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if path.Base(hdr.Name) != binName {
			continue
		}
		return writeStagedBinary(tr, binName)
	}
	return "", fmt.Errorf("%s not found in archive", binName)
}

func writeStagedBinary(r io.Reader, binName string) (string, error) {
	out, err := os.CreateTemp("", "praimate-staged-*-"+binName)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		os.Remove(out.Name())
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(out.Name())
		return "", err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(out.Name(), 0o755); err != nil {
			os.Remove(out.Name())
			return "", err
		}
	}
	return out.Name(), nil
}

// swapBinary places newBin at exePath. On Unix we can simply rename
// (the running process keeps its inode). On Windows the running .exe
// is locked for delete but allows rename, so we sidestep with a
// rename-rename pattern.
func swapBinary(exePath, newBin string) error {
	if runtime.GOOS == "windows" {
		old := exePath + ".old"
		_ = os.Remove(old) // best-effort: clean up leftover from previous update
		if err := os.Rename(exePath, old); err != nil {
			return err
		}
		if err := os.Rename(newBin, exePath); err != nil {
			// Restore on failure so we don't leave the user without a binary.
			_ = os.Rename(old, exePath)
			return err
		}
		return nil
	}
	// Cross-device renames fail on some Linux setups (/tmp on a
	// different fs). Fall back to copy+replace if so.
	if err := os.Rename(newBin, exePath); err != nil {
		return copyOver(newBin, exePath)
	}
	return nil
}

// copyOver is the cross-filesystem fallback for swapBinary. It can't
// just open(dst, O_WRONLY|O_TRUNC) because on Linux that returns
// ETXTBSY ("text file busy") whenever dst is the running executable —
// the very case the self-updater always hits. Instead we copy to a
// sibling temp file (same filesystem as dst, so the final rename is
// always intra-fs) and atomic-rename over the running binary. The
// rename re-points the path; the running process keeps its open file
// descriptor and finishes happily.
func copyOver(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied in directory %s: %w", filepath.Dir(dst), err)
		}
		return err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
