package core

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type repoMapCacheEntry struct {
	files  []repoMapFile
	usedAt time.Time
}

type repoMapFile struct {
	relPath string
	size    int64
	modTime time.Time
	symbols []string
}

const maxRepoMapWorkspaces = 16

var (
	repoMapMu    sync.RWMutex
	repoMapCache = make(map[string]repoMapCacheEntry)
)

var (
	pyClassOrDef = regexp.MustCompile(`^(class|def)\s+([a-zA-Z0-9_]+)`)
	tsExport     = regexp.MustCompile(`^export\s+(?:default\s+)?(?:async\s+)?(?:function|class|interface|type|const|enum)\s+([a-zA-Z0-9_]+)`)
	rsPub        = regexp.MustCompile(`^pub(?:\([^)]+\))?\s+(?:async\s+)?(?:fn|struct|enum|trait|type)\s+([a-zA-Z0-9_]+)`)
)

// GenerateRepoMap scans a workspace directory and builds a compact ambient
// map of files and public symbols (types, functions, exports), bounded by maxTokens.
func GenerateRepoMap(workspace string, maxTokens int) string {
	cleanWs, err := repoMapWorkspace(workspace)
	if err != nil {
		return ""
	}
	root, err := os.OpenRoot(cleanWs)
	if err != nil {
		return ""
	}
	defer root.Close()

	repoMapMu.RLock()
	cached := repoMapCache[cleanWs]
	repoMapMu.RUnlock()
	previous := make(map[string]repoMapFile, len(cached.files))
	for _, file := range cached.files {
		previous[file.relPath] = file
	}

	if maxTokens <= 0 {
		maxTokens = 800
	}

	var files []repoMapFile
	ignoredDirs := map[string]bool{
		".git": true, ".gemini": true, "node_modules": true, "vendor": true,
		"dist": true, "bin": true, "build": true, ".idea": true, ".vscode": true,
		"tmp": true, "target": true, "coverage": true, ".praimate": true,
	}

	// Directory mtime does not change when an existing nested file is edited.
	// Recheck bounded metadata, reusing parsed symbols only for unchanged files.
	visited := 0
	_ = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		visited++
		if visited > 4096 {
			return fs.SkipAll
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != "." && (strings.HasPrefix(name, ".") || ignoredDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		// Ambient discovery never follows symlinks or opens special files.
		if !d.Type().IsRegular() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".c", ".h", ".cpp", ".md", ".json", ".yaml", ".yml", ".toml", ".sql":
		default:
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		file := repoMapFile{relPath: path, size: info.Size(), modTime: info.ModTime()}
		if old, ok := previous[path]; ok && old.size == file.size && old.modTime.Equal(file.modTime) {
			file.symbols = old.symbols
		} else {
			switch ext {
			case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs":
				file.symbols = extractSymbols(root, path, ext)
			}
		}
		files = append(files, file)
		if len(files) >= 120 {
			return fs.SkipAll
		}
		return nil
	})

	repoMapMu.Lock()
	if _, exists := repoMapCache[cleanWs]; !exists && len(repoMapCache) >= maxRepoMapWorkspaces {
		var oldest string
		for key, entry := range repoMapCache {
			if oldest == "" || entry.usedAt.Before(repoMapCache[oldest].usedAt) {
				oldest = key
			}
		}
		delete(repoMapCache, oldest)
	}
	repoMapCache[cleanWs] = repoMapCacheEntry{files: files, usedAt: time.Now()}
	repoMapMu.Unlock()

	if len(files) == 0 {
		return ""
	}

	var b strings.Builder
	header := fmt.Sprintf("[Ambient Repo Map: %s]\n", filepath.Base(cleanWs))
	used := nativeTextTokens(header)
	if used > maxTokens {
		return ""
	}
	b.WriteString(header)

	for i, f := range files {
		line := "  " + f.relPath
		if len(f.symbols) > 0 {
			line += " (" + strings.Join(f.symbols, ", ") + ")"
		}
		line += "\n"
		cost := nativeTextTokens(line)
		if used+cost > maxTokens {
			omitted := fmt.Sprintf("  ... [%d more files omitted for budget]\n", len(files)-i)
			if used+nativeTextTokens(omitted) <= maxTokens {
				b.WriteString(omitted)
			}
			break
		}
		b.WriteString(line)
		used += cost
	}

	return strings.TrimRight(b.String(), "\n")
}

func extractSymbols(root *os.Root, path, ext string) []string {
	f, err := root.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	const maxSize = 256 * 1024
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSize {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil || len(data) == 0 || len(data) > maxSize {
		return nil
	}
	var symbols []string
	if ext == ".go" {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, data, parser.SkipObjectResolution)
		if err == nil {
			for _, decl := range f.Decls {
				if len(symbols) >= 6 {
					break
				}
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.IsExported() {
					symbols = append(symbols, fn.Name.Name)
				}
			}
		}
		return symbols
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	lines := 0
	for scanner.Scan() && lines < 300 && len(symbols) < 6 {
		line := strings.TrimSpace(scanner.Text())
		lines++
		if ext == ".py" {
			if m := pyClassOrDef.FindStringSubmatch(line); len(m) > 2 {
				symbols = append(symbols, m[2])
			}
		} else if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
			if m := tsExport.FindStringSubmatch(line); len(m) > 1 {
				symbols = append(symbols, m[1])
			}
		} else if ext == ".rs" {
			if m := rsPub.FindStringSubmatch(line); len(m) > 1 {
				symbols = append(symbols, m[1])
			}
		}
	}
	return symbols
}

// WorkspaceRepoMap returns the ambient repo map for a given workspace.
func (c *Core) WorkspaceRepoMap(workspace string, maxTokens int) string {
	return GenerateRepoMap(workspace, maxTokens)
}

// InvalidateRepoMap clears the cached repo map for a workspace.
func InvalidateRepoMap(workspace string) {
	path, err := repoMapWorkspace(workspace)
	if err != nil {
		return
	}
	repoMapMu.Lock()
	delete(repoMapCache, path)
	repoMapMu.Unlock()
}

func repoMapWorkspace(workspace string) (string, error) {
	if workspace == "" {
		workspace = "."
	}
	path, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}
