package core

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type repoMapCacheEntry struct {
	content string
	modTime time.Time
}

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
	if workspace == "" || workspace == "." {
		workspace, _ = os.Getwd()
	}
	cleanWs := filepath.Clean(workspace)

	fi, err := os.Stat(cleanWs)
	if err != nil || !fi.IsDir() {
		return ""
	}

	repoMapMu.RLock()
	cached, ok := repoMapCache[cleanWs]
	repoMapMu.RUnlock()
	if ok && fi.ModTime().Equal(cached.modTime) && cached.content != "" {
		return cached.content
	}

	maxBytes := maxTokens * 4
	if maxBytes <= 0 {
		maxBytes = 3200 // default ~800 tokens
	}

	type fileInfo struct {
		relPath string
		symbols []string
	}

	var files []fileInfo
	ignoredDirs := map[string]bool{
		".git": true, ".gemini": true, "node_modules": true, "vendor": true,
		"dist": true, "bin": true, "build": true, ".idea": true, ".vscode": true,
		"tmp": true, "target": true, "coverage": true, ".praimate": true,
	}

	_ = filepath.WalkDir(cleanWs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") && name != "." || ignoredDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(cleanWs, path)
		if err != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		ext := strings.ToLower(filepath.Ext(path))

		switch ext {
		case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".c", ".h", ".cpp":
			syms := extractSymbols(path, ext)
			files = append(files, fileInfo{relPath: rel, symbols: syms})
		case ".md", ".json", ".yaml", ".yml", ".toml", ".sql":
			files = append(files, fileInfo{relPath: rel})
		}
		if len(files) >= 120 {
			return fs.SkipAll
		}
		return nil
	})

	if len(files) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("[Ambient Repo Map: %s]\n", filepath.Base(cleanWs)))

	for _, f := range files {
		line := "  " + f.relPath
		if len(f.symbols) > 0 {
			line += " (" + strings.Join(f.symbols, ", ") + ")"
		}
		line += "\n"
		if b.Len()+len(line) > maxBytes-60 {
			b.WriteString(fmt.Sprintf("  ... [%d more files omitted for budget]\n", len(files)))
			break
		}
		b.WriteString(line)
	}

	result := strings.TrimRight(b.String(), "\n")

	repoMapMu.Lock()
	repoMapCache[cleanWs] = repoMapCacheEntry{content: result, modTime: fi.ModTime()}
	repoMapMu.Unlock()

	return result
}

func extractSymbols(fullPath, ext string) []string {
	data, err := os.ReadFile(fullPath)
	if err != nil || len(data) == 0 || len(data) > 256*1024 {
		return nil
	}
	var symbols []string
	if ext == ".go" {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, fullPath, data, parser.SkipObjectResolution)
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
	if workspace == "" || workspace == "." {
		workspace, _ = os.Getwd()
	}
	repoMapMu.Lock()
	delete(repoMapCache, filepath.Clean(workspace))
	repoMapMu.Unlock()
}
