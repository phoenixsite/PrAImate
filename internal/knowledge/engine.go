package knowledge

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// SetEngine records explicit selection separately from automatic index refresh.
// Both indexes survive switching. Empty means the backward-compatible default.
func SetEngine(dir, engine string) error {
	if engine != "native" && engine != "graphify" {
		return fmt.Errorf("unknown knowledge engine %q", engine)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.Mkdir(Directory, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	name := Directory + "/.engine-" + rand.Text()
	defer root.Remove(name)
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(engine + "\n")
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return rootRename(root, name, Directory+"/engine")
}
func UseNative(dir string) bool {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer root.Close()
	f, err := openRegular(root, Directory+"/engine", 64)
	if err == nil {
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, 64))
		if err == nil {
			switch strings.TrimSpace(string(raw)) {
			case "native":
				return true
			case "graphify":
				return false
			}
		}
	}
	return HasIndex(dir)
}

func rootRename(root *os.Root, oldName, newName string) error {
	if root == nil {
		return errors.New("nil root")
	}
	// Preserve Root's symlink/path checks. Falling back to an absolute Rename
	// would bypass a rejected destination and could write outside knowledge.
	return root.Rename(oldName, newName)
}
