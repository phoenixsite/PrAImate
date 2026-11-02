package knowledge

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

type termPosition struct {
	name       string
	start, end int
}

// Split identifiers as well as prose: JWTRefreshInterval, JWT_REFRESH_INTERVAL
// and "JWT refresh interval" should point to the same source vocabulary.
func positionedTerms(text string) []termPosition {
	var out []termPosition
	start := -1
	previous := rune(0)
	for offset, r := range text {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			if start >= 0 {
				out = append(out, termPosition{strings.ToLower(text[start:offset]), start, offset})
				start = -1
			}
			previous = 0
			continue
		}
		if start < 0 {
			start = offset
		} else if unicode.IsUpper(r) {
			next, _ := utf8.DecodeRuneInString(text[offset+utf8.RuneLen(r):])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) || unicode.IsUpper(previous) && unicode.IsLower(next) {
				out = append(out, termPosition{strings.ToLower(text[start:offset]), start, offset})
				start = offset
			}
		}
		previous = r
	}
	if start >= 0 {
		out = append(out, termPosition{strings.ToLower(text[start:]), start, len(text)})
	}
	return out
}
func terms(text string) []string {
	positions := positionedTerms(text)
	out := make([]string, 0, len(positions))
	for _, p := range positions {
		out = append(out, p.name)
	}
	return out
}

var questionFillers = map[string]bool{"a": true, "an": true, "the": true, "and": true, "or": true, "is": true, "are": true, "of": true, "to": true, "in": true, "on": true, "how": true, "does": true, "do": true, "what": true, "which": true, "where": true, "please": true, "el": true, "la": true, "los": true, "las": true, "un": true, "una": true, "y": true, "o": true, "de": true, "del": true, "en": true, "que": true, "qué": true, "como": true, "cómo": true, "es": true, "son": true, "por": true, "para": true}

func questionTerms(question string) map[string]bool {
	out := map[string]bool{}
	for _, t := range terms(question) {
		if !questionFillers[t] {
			out[t] = true
		}
	}
	return out
}

// File labels help discover conceptual graph matches, but must not outweigh
// a match in the actual passage or make every chunk in a file equally relevant.
func metadataTerms(idx *Index, query map[string]bool) map[string]map[string]bool {
	labels := make(map[string]*strings.Builder)
	for _, n := range idx.Graph.Nodes {
		sb := labels[n.SourceFile]
		if sb == nil {
			sb = &strings.Builder{}
			labels[n.SourceFile] = sb
		}
		if sb.Len() >= 8192 {
			continue
		}
		label := n.Label
		if len(label) > 1024 {
			label = label[:1024]
		}
		sb.WriteByte(' ')
		sb.WriteString(label)
	}
	out := make(map[string]map[string]bool, len(idx.Documents))
	for _, doc := range idx.Documents {
		m := map[string]bool{}
		text := doc.Path
		if sb := labels[doc.Path]; sb != nil {
			text += sb.String()
		}
		for _, t := range terms(text) {
			if query[t] {
				m[t] = true
			}
		}
		out[doc.Path] = m
	}
	return out
}
func bm25(tf, df, length, average, total float64) float64 {
	return math.Log(1+(total-df+.5)/(df+.5)) * tf * 2.2 / (tf + 1.2*(.25+.75*length/average))
}

func semanticPassageTerms(idx *Index, query map[string]bool) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, n := range idx.Graph.Nodes {
		if n.FileType != "semantic" {
			continue
		}
		prefix := "semantic:" + n.SourceFile + ":" + n.SourceLocation + ":"
		hash, _, _ := strings.Cut(strings.TrimPrefix(n.ID, prefix), ":")
		key := n.SourceFile + "\x00" + n.SourceLocation + "\x00" + hash
		if out[key] == nil {
			out[key] = map[string]bool{}
		}
		for _, term := range terms(n.Label) {
			if query[term] {
				out[key][term] = true
			}
		}
	}
	return out
}

// excerpt selects a UTF-8 window around the best matching terms instead of
// dropping the useful part of a passage when the output budget is small.
func excerpt(chunk Chunk, query map[string]bool, limit int) (string, int) {
	if len(chunk.Text) <= limit {
		return chunk.Text, chunk.Line
	}
	if limit < 16 {
		return "", chunk.Line
	}
	width := limit - 8
	positions := positionedTerms(chunk.Text)
	start := 0
	bestScore := -1
	for _, hit := range positions {
		if !query[hit.name] {
			continue
		}
		candidate := max(0, hit.start-width/4)
		candidate = min(candidate, max(0, len(chunk.Text)-width))
		score := 0
		seen := map[string]bool{}
		for _, p := range positions {
			if p.start >= candidate && p.end <= candidate+width && query[p.name] && !seen[p.name] {
				seen[p.name] = true
				score++
			}
		}
		if score > bestScore {
			start = candidate
			bestScore = score
		}
	}
	for start > 0 && !utf8.RuneStart(chunk.Text[start]) {
		start--
	}
	end := min(len(chunk.Text), start+width)
	for end > start && end < len(chunk.Text) && !utf8.RuneStart(chunk.Text[end]) {
		end--
	}
	text := chunk.Text[start:end]
	if start > 0 {
		text = "… " + text
	}
	if end < len(chunk.Text) {
		text += " …"
	}
	return text, chunk.Line + strings.Count(chunk.Text[:start], "\n")
}
