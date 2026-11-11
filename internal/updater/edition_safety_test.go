package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sPROFFEs/PrAImate/internal/version"
)

type archiveEntry struct{ name, content string }

func editionArchive(t *testing.T, extension string, entries []archiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if extension == ".zip" {
		writer := zip.NewWriter(&buffer)
		for _, entry := range entries {
			file, err := writer.Create(entry.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write([]byte(entry.content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		compressed := gzip.NewWriter(&buffer)
		writer := tar.NewWriter(compressed)
		for _, entry := range entries {
			if err := writer.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o755, Size: int64(len(entry.content))}); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write([]byte(entry.content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := compressed.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return buffer.Bytes()
}

func oppositeEdition() string {
	if version.Edition == "desktop" {
		return "server"
	}
	return "desktop"
}

func hostExtension() string {
	if runtime.GOOS == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

func testBinary(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func TestArchiveEditionIsCheckedForTarAndWindowsZip(t *testing.T) {
	for _, extension := range []string{".tar.gz", ".zip"} {
		for _, tc := range []struct {
			name    string
			markers []archiveEntry
			valid   bool
		}{
			{"matching", []archiveEntry{{"bundle/" + editionMarker, version.Edition + "\n"}}, true},
			{"other edition", []archiveEntry{{"bundle/" + editionMarker, oppositeEdition()}}, false},
			{"legacy", nil, version.Edition == "desktop"},
			{"invalid marker", []archiveEntry{{editionMarker, "unknown"}}, false},
			{"oversized marker", []archiveEntry{{editionMarker, strings.Repeat(" ", 65) + version.Edition}}, false},
			{"conflicting markers", []archiveEntry{{"one/" + editionMarker, version.Edition}, {"two/" + editionMarker, oppositeEdition()}}, false},
		} {
			t.Run(extension+"/"+tc.name, func(t *testing.T) {
				file := filepath.Join(t.TempDir(), "update"+extension)
				if err := os.WriteFile(file, editionArchive(t, extension, tc.markers), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := validateArchiveEdition(file); (err == nil) != tc.valid {
					t.Fatalf("validation = %v, valid=%v", err, tc.valid)
				}
			})
		}
	}
}

func TestUpdateRefusesAnotherEditionBeforeReplacingAnyBinary(t *testing.T) {
	for _, tc := range []struct {
		name, marker                 string
		wrongName, wrongInstallation bool
		valid                        bool
	}{
		{"matching bundle", version.Edition, false, false, true},
		{"mislabeled bundle", oppositeEdition(), false, false, false},
		{"wrong asset name", version.Edition, true, false, false},
		{"stale process in another installation", version.Edition, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			main := filepath.Join(directory, testBinary("praimate"))
			gui := filepath.Join(directory, testBinary("praimate-gui"))
			for _, file := range []string{main, gui} {
				if err := os.WriteFile(file, []byte("original"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			installed := version.Edition
			if tc.wrongInstallation {
				installed = oppositeEdition()
			}
			if err := os.WriteFile(filepath.Join(directory, editionMarker), []byte(installed+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			data := editionArchive(t, hostExtension(), []archiveEntry{{"bundle/" + editionMarker, tc.marker}, {"bundle/" + testBinary("praimate"), "updated"}, {"bundle/" + testBinary("praimate-gui"), "updated"}})
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; _, _ = w.Write(data) }))
			defer server.Close()
			prefix := version.AssetPrefix
			if tc.wrongName {
				if prefix == "praimate" {
					prefix = "praimate-server"
				} else {
					prefix = "praimate"
				}
			}
			asset := &Asset{Name: prefix + "-" + runtime.GOOS + "-" + runtime.GOARCH + hostExtension(), BrowserDownloadURL: server.URL}
			err := applyAt(main, asset, nil)
			if (err == nil) != tc.valid {
				t.Fatalf("apply = %v, valid=%v", err, tc.valid)
			}
			if (tc.wrongName || tc.wrongInstallation) && requests != 0 {
				t.Fatal("invalid update downloaded an archive")
			}
			expect := "original"
			if tc.valid {
				expect = "updated"
			}
			for _, file := range []string{main, gui} {
				data, err := os.ReadFile(file)
				if err != nil || string(data) != expect {
					t.Fatalf("%s changed unexpectedly: %q, %v", file, data, err)
				}
			}
			marker, err := os.ReadFile(filepath.Join(directory, editionMarker))
			if err != nil || string(marker) != installed+"\n" {
				t.Fatalf("installation identity changed: %q, %v", marker, err)
			}
		})
	}
}

func TestCanonicalEditionArchiveWinsRegardlessOfAssetOrder(t *testing.T) {
	for _, platform := range []struct{ os, arch, ext string }{{"linux", "amd64", ".tar.gz"}, {"darwin", "arm64", ".tar.gz"}, {"windows", "amd64", ".zip"}, {"windows", "arm64", ".zip"}} {
		suffix := "-" + platform.os + "-" + platform.arch + platform.ext
		rel := &Release{TagName: "v1.3.3", Assets: []Asset{{Name: version.AssetPrefix + "-1.3.3" + suffix}, {Name: version.AssetPrefix + suffix}}}
		got, err := assetForPlatform(rel, platform.os, platform.arch)
		if err != nil || got.Name != version.AssetPrefix+suffix {
			t.Fatalf("wrong archive: %#v, %v", got, err)
		}
	}
	if err := validateUpdateAsset(nil); err == nil {
		t.Fatal("accepted a missing asset")
	}
}
