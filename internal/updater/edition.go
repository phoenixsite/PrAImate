package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/sPROFFEs/PrAImate/internal/version"
)

const editionMarker = "praimate-edition"

func validateUpdateAsset(asset *Asset) error {
	if asset == nil {
		return errors.New("no update asset selected")
	}
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	suffix := "-" + runtime.GOOS + "-" + runtime.GOARCH + ext
	if asset.Name == version.AssetPrefix+suffix {
		return nil
	}
	// Older releases put a version between the product and host triplet.
	legacy := strings.TrimSuffix(strings.TrimPrefix(asset.Name, version.AssetPrefix+"-"), suffix)
	if strings.HasPrefix(asset.Name, version.AssetPrefix+"-") && strings.HasSuffix(asset.Name, suffix) &&
		regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`).MatchString(legacy) {
		return nil
	}
	return fmt.Errorf("refusing update asset %q: expected a %s archive for %s/%s", asset.Name, version.Edition, runtime.GOOS, runtime.GOARCH)
}

func validateInstalledEdition(directory string) error {
	data, err := os.ReadFile(filepath.Join(directory, editionMarker))
	if errors.Is(err, os.ErrNotExist) {
		return nil // Old installations did not record an edition.
	}
	if err != nil {
		return fmt.Errorf("read installed edition: %w", err)
	}
	if actual := strings.TrimSpace(string(data)); actual != version.Edition {
		return fmt.Errorf("refusing to update a %q installation from a %s process; reinstall the intended edition or use a separate installation directory", actual, version.Edition)
	}
	return nil
}

// Validate the bundle before any live binary is replaced. A legacy unmarked
// bundle is Desktop-only; Server bundles have always carried this marker.
func validateArchiveEdition(archive string) error {
	actual, found := "desktop", false
	read := func(reader io.Reader) error {
		if found {
			return errors.New("update archive contains multiple edition markers")
		}
		found = true
		data, err := io.ReadAll(io.LimitReader(reader, 65))
		if err != nil {
			return err
		}
		actual = strings.TrimSpace(string(data))
		if len(data) > 64 || actual != "desktop" && actual != "server" {
			return errors.New("update archive contains an invalid edition marker")
		}
		return nil
	}
	if strings.HasSuffix(archive, ".zip") {
		reader, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer reader.Close()
		for _, entry := range reader.File {
			if path.Base(strings.ReplaceAll(entry.Name, `\`, "/")) != editionMarker {
				continue
			}
			if !entry.Mode().IsRegular() {
				return errors.New("update edition marker is not a regular file")
			}
			stream, err := entry.Open()
			if err != nil {
				return err
			}
			err = read(stream)
			stream.Close()
			if err != nil {
				return err
			}
		}
	} else {
		file, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer file.Close()
		compressed, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer compressed.Close()
		reader := tar.NewReader(compressed)
		for {
			entry, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if path.Base(entry.Name) != editionMarker {
				continue
			}
			if entry.Typeflag != tar.TypeReg && entry.Typeflag != tar.TypeRegA {
				return errors.New("update edition marker is not a regular file")
			}
			if err := read(reader); err != nil {
				return err
			}
		}
	}
	if actual != version.Edition {
		return fmt.Errorf("refusing %s update bundle for this %s installation; no binaries were replaced", actual, version.Edition)
	}
	return nil
}

func recordInstalledEdition(directory string) error {
	file, err := os.CreateTemp(directory, ".praimate-edition-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = io.WriteString(file, version.Edition+"\n"); err == nil {
		err = file.Chmod(0o644)
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), filepath.Join(directory, editionMarker))
}
