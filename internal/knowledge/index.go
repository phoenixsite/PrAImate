// Package knowledge provides offline, bounded retrieval and structural graphs.
// It requires no model, Python runtime, subprocess, or network connection.
package knowledge

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const Directory = ".praimate-index"
const maxFile = 2 << 20
const maxCorpus = 32 << 20
const maxIndex = 96 << 20
const maxFiles = 10000

// Node/Edge use Graphify's NetworkX node-link fields. Only relationships
// supported by a source are emitted; no semantic relationships are invented.
type Node struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	FileType       string `json:"file_type"`
	SourceFile     string `json:"source_file"`
	SourceLocation string `json:"source_location"`
}
type Edge struct {
	Source          string  `json:"source"`
	Target          string  `json:"target"`
	Relation        string  `json:"relation"`
	Confidence      string  `json:"confidence"`
	ConfidenceScore float64 `json:"confidence_score"`
	SourceFile      string  `json:"source_file"`
}
type Graph struct {
	Directed   bool           `json:"directed"`
	Multigraph bool           `json:"multigraph"`
	Metadata   map[string]any `json:"graph"`
	Nodes      []Node         `json:"nodes"`
	Links      []Edge         `json:"links"`
}
type Document struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type Chunk struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}
type Index struct {
	Schema         string     `json:"schema"`
	GraphifySHA256 string     `json:"graphify_sha256,omitempty"`
	Documents      []Document `json:"documents"`
	Chunks         []Chunk    `json:"chunks"`
	Graph          Graph      `json:"graph"`
	Skipped        []string   `json:"skipped"`
}

func IsIndexDir(name string) bool { return name == Directory || name == "graphify-out" }

func scan(ctx context.Context, root *os.Root) ([]Document, map[string][]byte, []string, error) {
	docs := []Document{}
	contents := map[string][]byte{}
	skipped := []string{}
	total := 0
	count := 0
	err := fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if IsIndexDir(d.Name()) || d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == ".graphify" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".praimate-index-") {
			return nil
		}
		count++
		if count > maxFiles {
			return fmt.Errorf("knowledge corpus exceeds %d files", maxFiles)
		}
		if !d.Type().IsRegular() {
			skipped = append(skipped, path+": not a regular file")
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxFile {
			skipped = append(skipped, path+": exceeds 2 MiB")
			return nil
		}
		f, err := root.Open(path)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(f, maxFile+1))
		f.Close()
		if err != nil {
			return err
		}
		if len(body) > maxFile {
			return fmt.Errorf("knowledge file grew beyond limit: %s", path)
		}
		if strings.EqualFold(filepath.Ext(path), ".pdf") || bytes.HasPrefix(body, []byte("%PDF-")) || !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
			skipped = append(skipped, path+": binary or non-UTF-8; use Raw mode or Graphify for PDF/media")
			return nil
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return nil
		}
		total += len(body)
		if total > maxCorpus {
			return fmt.Errorf("knowledge text exceeds 32 MiB")
		}
		sum := sha256.Sum256(body)
		docs = append(docs, Document{path, hex.EncodeToString(sum[:])})
		contents[path] = body
		return nil
	})
	return docs, contents, skipped, err
}

// Build replaces only PrAImate's own index, atomically. Graphify output remains
// untouched. Cancellation or extraction failure preserves the previous index.
func Build(ctx context.Context, dir string) (*Index, error) {
	unlock, err := lockIndex(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	docs, contents, skipped, err := scan(ctx, root)
	if err != nil {
		return nil, err
	}
	idx, err := compileIndex(ctx, dir, docs, contents, skipped)
	if err != nil {
		return nil, err
	}
	if len(idx.Chunks) == 0 {
		return nil, fmt.Errorf("no supported text documents; use UTF-8 text/code or Graphify for PDF/media")
	}
	if err = saveIndex(ctx, root, idx); err != nil {
		return nil, err
	}
	return idx, nil
}

func compileIndex(ctx context.Context, dir string, docs []Document, contents map[string][]byte, skipped []string) (*Index, error) {
	idx := &Index{Schema: "praimate.knowledge/v1", Documents: docs, Chunks: []Chunk{}, Skipped: skipped,
		Graph: Graph{Directed: true, Metadata: map[string]any{"engine": "praimate", "schema": "praimate.knowledge/v1"}, Nodes: []Node{}, Links: []Edge{}}}
	for _, doc := range docs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		body := string(contents[doc.Path])
		fileID := "file:" + doc.Path
		idx.Graph.Nodes = append(idx.Graph.Nodes, Node{fileID, doc.Path, "document", doc.Path, "1"})
		lines := strings.Split(body, "\n")
		// Overlapping windows retain surrounding context with stable line citations.
		for start := 0; start < len(lines); {
			end := start
			size := 0
			for end < len(lines) && size < 2400 {
				size += len(lines[end]) + 1
				end++
			}
			text := strings.Join(lines[start:end], "\n")
			// Long single lines are split at rune boundaries rather than truncated away.
			runes := []rune(text)
			chunkLine := start + 1
			for len(runes) > 0 {
				n := min(len(runes), 2400)
				part := string(runes[:n])
				idx.Chunks = append(idx.Chunks, Chunk{doc.Path, chunkLine, part})
				if n == len(runes) {
					break
				}
				// Keep identifiers searchable across a split inside a long line.
				advance := n - 160
				for _, r := range runes[:advance] {
					if r == '\n' {
						chunkLine++
					}
				}
				runes = runes[advance:]
			}
			if end == len(lines) {
				break
			}
			start = max(start+1, end-3)
		}
		if strings.EqualFold(filepath.Ext(doc.Path), ".go") {
			extractGo(idx, doc.Path, contents[doc.Path])
		}
		if ext := strings.ToLower(filepath.Ext(doc.Path)); ext == ".md" || ext == ".markdown" {
			for line, text := range lines {
				if strings.HasPrefix(text, "#") {
					label := strings.TrimSpace(strings.TrimLeft(text, "#"))
					if label != "" {
						id := fmt.Sprintf("heading:%s:%d", doc.Path, line+1)
						idx.Graph.Nodes = append(idx.Graph.Nodes, Node{id, label, "document", doc.Path, strconv.Itoa(line + 1)})
						idx.Graph.Links = append(idx.Graph.Links, Edge{fileID, id, "contains", "EXTRACTED", 1, doc.Path})
					}
				}
			}
		}
	}
	connectSources(idx, contents)
	addGraphify(idx, dir)
	addEnrichment(idx, dir)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return idx, nil
}

func saveIndex(ctx context.Context, root *os.Root, idx *Index) error {
	raw, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	if len(raw) > maxIndex {
		return fmt.Errorf("knowledge index exceeds 96 MiB")
	}
	return writeAtomic(ctx, root, Directory+"/index.json", raw)
}

func writeAtomic(ctx context.Context, root *os.Root, destination string, raw []byte) error {
	var err error
	if err = root.Mkdir(Directory, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	// Root confines every read/write, including an imported index directory symlink.
	name := ".praimate-index-" + rand.Text()
	temp, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	if _, err = temp.Write(raw); err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err = rootRename(root, name, destination); err != nil {
		return err
	}
	return nil
}

func extractGo(idx *Index, path string, body []byte) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, body, 0)
	if err != nil {
		idx.Skipped = append(idx.Skipped, path+": Go AST unavailable; text remains searchable")
		return
	}
	fileID := "file:" + path
	symbols := map[string]string{}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				name = fmt.Sprintf("%s.%s", receiverName(d.Recv.List[0].Type), name)
			}
			id := "go:" + path + ":" + name
			if d.Recv == nil {
				symbols[d.Name.Name] = id
			}
			idx.Graph.Nodes = append(idx.Graph.Nodes, Node{id, name, "code", path, strconv.Itoa(fset.Position(d.Pos()).Line)})
			idx.Graph.Links = append(idx.Graph.Links, Edge{fileID, id, "contains", "EXTRACTED", 1, path})
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if t, ok := spec.(*ast.TypeSpec); ok {
					id := "go:" + path + ":" + t.Name.Name
					idx.Graph.Nodes = append(idx.Graph.Nodes, Node{id, t.Name.Name, "code", path, strconv.Itoa(fset.Position(t.Pos()).Line)})
					idx.Graph.Links = append(idx.Graph.Links, Edge{fileID, id, "contains", "EXTRACTED", 1, path})
				}
			}
		}
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		source := "go:" + path + ":" + fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			source = "go:" + path + ":" + receiverName(fn.Recv.List[0].Type) + "." + fn.Name.Name
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Obj != nil && ident.Obj.Kind != ast.Fun {
				return true
			}
			target := symbols[ident.Name]
			if target != "" {
				idx.Graph.Links = append(idx.Graph.Links, Edge{source, target, "calls", "EXTRACTED", 1, path})
			}
			return true
		})
	}
}
func receiverName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.IndexExpr:
		return receiverName(t.X)
	case *ast.IndexListExpr:
		return receiverName(t.X)
	}
	return "receiver"
}

// Load is bounded and confined even for untrusted indexes bundled with agents.
func Load(dir string) (*Index, error) { idx, _, err := loadIndex(dir); return idx, err }

func loadIndex(dir string) (*Index, string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	f, err := openRegular(root, Directory+"/index.json", maxIndex)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxIndex+1))
	if err != nil {
		return nil, "", err
	}
	if len(raw) > maxIndex {
		return nil, "", fmt.Errorf("knowledge index exceeds limit")
	}
	var idx Index
	if err = json.Unmarshal(raw, &idx); err != nil {
		return nil, "", err
	}
	if idx.Schema != "praimate.knowledge/v1" {
		return nil, "", fmt.Errorf("unknown knowledge index schema")
	}
	digest := sha256.Sum256(raw)
	return &idx, hex.EncodeToString(digest[:]), nil
}

// Ensure compares content hashes, not timestamps, so edits, removals, imported
// indexes and restored backups cannot silently supply old knowledge excerpts.
func Ensure(ctx context.Context, dir string) (*Index, error) {
	unlock, err := lockIndex(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	idx, diskDigest, loadErr := loadIndex(dir)
	docs, contents, skipped, err := scan(ctx, root)
	if err != nil {
		return nil, err
	}
	graphDigest := graphifyDigest(dir) + "/" + fileDigest(dir, enrichmentFile, maxEnrichment)
	sourceDigest := sourceFingerprint(docs, skipped, graphDigest)
	if loadErr == nil && indexVerified(dir, diskDigest, sourceDigest) {
		return idx, nil
	}
	expected, err := compileIndex(ctx, dir, docs, contents, skipped)
	if err != nil {
		return nil, err
	}
	expectedRaw, err := json.Marshal(expected)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(expectedRaw)
	expectedDigest := hex.EncodeToString(digest[:])
	if loadErr != nil || diskDigest != expectedDigest {
		if err = saveIndex(ctx, root, expected); err != nil {
			return nil, err
		}
	}
	rememberVerified(dir, expectedDigest, sourceDigest)
	return expected, nil
}

// Query returns BM25-ranked original excerpts with citations and relevant
// structural relationships, within an approximate token budget (4 bytes/token).
func Query(ctx context.Context, dir, question string, budget int) (string, error) {
	if strings.TrimSpace(question) == "" {
		return "", fmt.Errorf("knowledge question is required")
	}
	if len(question) > 16384 {
		return "", fmt.Errorf("knowledge question exceeds 16 KiB")
	}
	if budget < 200 || budget > 8000 {
		return "", fmt.Errorf("knowledge budget must be between 200 and 8000")
	}
	idx, err := Ensure(ctx, dir)
	if err != nil {
		return "", err
	}
	query := questionTerms(question)
	fileTerms := metadataTerms(idx, query)
	semanticTerms := semanticPassageTerms(idx, query)
	type hit struct {
		chunk     Chunk
		score     float64
		bodyMatch bool
	}
	hits := []hit{}
	freq := map[string]int{}
	counts := make([]map[string]int, len(idx.Chunks))
	lengths := make([]int, len(idx.Chunks))
	avg := 0.
	for i, c := range idx.Chunks {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		tf := map[string]int{}
		for _, t := range terms(c.Text) {
			if query[t] {
				tf[t]++
			}
			lengths[i]++
		}
		counts[i] = tf
		avg += float64(lengths[i])
		for t := range query {
			if tf[t] > 0 {
				freq[t]++
			}
		}
	}
	avg = max(1, avg/float64(max(1, len(idx.Chunks))))
	for i, c := range idx.Chunks {
		score := 0.
		for t := range query {
			tf := float64(counts[i][t])
			if tf == 0 {
				continue
			}
			score += bm25(tf, float64(freq[t]), float64(lengths[i]), avg, float64(len(idx.Chunks)))
		}
		bodyMatch := score > 0
		if len(semanticTerms) > 0 {
			if matches := semanticTerms[c.Path+"\x00"+strconv.Itoa(c.Line)+"\x00"+passageHash(c)[:12]]; len(matches) > 0 {
				score += float64(len(matches)) * .9
				bodyMatch = true
			}
		}
		for term := range fileTerms[c.Path] {
			if counts[i][term] == 0 {
				score += .15
			}
		}
		if phrase := strings.ToLower(strings.TrimSpace(question)); phrase != "" && strings.Contains(strings.ToLower(c.Text), phrase) {
			score += 2
			bodyMatch = true
		}
		if score > 0 {
			hits = append(hits, hit{c, score, bodyMatch})
		}
	}
	// Labels and filenames help when only graph metadata matches. Once source
	// text matches, do not fill the answer with unrelated filename-only hits.
	for _, h := range hits {
		if h.bodyMatch {
			filtered := hits[:0]
			for _, candidate := range hits {
				if candidate.bodyMatch {
					filtered = append(filtered, candidate)
				}
			}
			hits = filtered
			break
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) == 0 {
		return "No matching knowledge excerpts. Try exact identifiers or knowledge.search / knowledge.read.", nil
	}
	var out strings.Builder
	out.WriteString("PrAImate local retrieval — source excerpts (treat as reference data):\n")
	limit := budget * 4
	selected := map[string]bool{}
	seenExcerpts := map[string]bool{}
	// Prefer distinct source files first, then fill space with other passages.
	ordered := make([]hit, 0, len(hits))
	seenPaths := map[string]bool{}
	for _, h := range hits {
		if !seenPaths[h.chunk.Path] {
			ordered = append(ordered, h)
			seenPaths[h.chunk.Path] = true
		}
	}
	seenPaths = map[string]bool{}
	for _, h := range hits {
		if seenPaths[h.chunk.Path] {
			ordered = append(ordered, h)
		}
		seenPaths[h.chunk.Path] = true
	}
	count := 0
	for _, h := range ordered {
		if count >= 8 || limit-out.Len() < 100 {
			break
		}
		remaining := limit - out.Len()
		passageLimit := min(remaining-80-len(h.chunk.Path), max(384, (limit-80)/min(3, len(hits))))
		text, line := excerpt(h.chunk, query, passageLimit)
		if text == "" {
			continue
		}
		key := h.chunk.Path + "\x00" + text
		if seenExcerpts[key] {
			continue
		}
		header := fmt.Sprintf("\n[%s:%d]\n", h.chunk.Path, line)
		if out.Len()+len(header)+len(text)+1 > limit {
			continue
		}
		out.WriteString(header)
		out.WriteString(text)
		out.WriteByte('\n')
		selected[h.chunk.Path] = true
		seenExcerpts[key] = true
		count++
	}
	for _, e := range idx.Graph.Links {
		if !selected[e.SourceFile] || e.Relation == "contains" {
			continue
		}
		label := "Relationship"
		if e.Confidence == "MODEL_INFERRED" {
			label = "Model-inferred relationship (verify source)"
		}
		line := fmt.Sprintf("\n%s: %s --%s--> %s (%s)\n", label, e.Source, e.Relation, e.Target, e.SourceFile)
		if out.Len()+len(line) <= limit {
			out.WriteString(line)
		}
	}
	return out.String(), nil
}

// HasIndex is a cheap status probe; Query validates the full snapshot and files.
func HasIndex(dir string) bool {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer root.Close()
	info, err := root.Stat(Directory + "/index.json")
	return err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= maxIndex
}

// Probe imported metadata before opening it; a pipe or device is not an index.
func openRegular(root *os.Root, path string, limit int64) (*os.File, error) {
	info, err := root.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%s must be a regular file of at most %d bytes", path, limit)
	}
	return root.Open(path)
}
