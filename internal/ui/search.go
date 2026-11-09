package ui

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/FoamScience/beads-tui/internal/bd"
)

// searchIndex ranks issues with BM25 over title, description and notes. Rows whose
// picker label contains the raw query (id, title or label substring) come first, so
// everything the old substring filter found still leads, followed by body matches.
type searchIndex struct {
	heads  []string                   // lowercased picker row per issue, for substring hits
	tf     map[string]map[int]float64 // term -> issue index -> weighted term frequency
	length []float64
	vocab  []string // sorted terms, for prefix expansion of the last query word
	avgLen float64
}

const (
	bm25K1      = 1.5
	bm25B       = 0.75
	titleWeight = 2.0
)

func newSearchIndex(issues []*bd.Issue, heads []string) *searchIndex {
	x := &searchIndex{heads: make([]string, len(issues)), tf: map[string]map[int]float64{}, length: make([]float64, len(issues))}
	for i, is := range issues {
		x.heads[i] = strings.ToLower(heads[i])
		x.add(i, is.Title, titleWeight)
		x.add(i, is.Description, 1)
		x.add(i, is.Notes, 1)
		x.avgLen += x.length[i]
	}
	if len(issues) > 0 {
		x.avgLen /= float64(len(issues))
	}
	x.vocab = make([]string, 0, len(x.tf))
	for t := range x.tf {
		x.vocab = append(x.vocab, t)
	}
	sort.Strings(x.vocab)
	return x
}

func (x *searchIndex) add(doc int, text string, w float64) {
	for _, t := range searchTokens(text) {
		m := x.tf[t]
		if m == nil {
			m = map[int]float64{}
			x.tf[t] = m
		}
		m[doc] += w
		x.length[doc] += w
	}
}

// searchTokens lowercases and splits on anything that is not a letter or digit.
func searchTokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// rank returns issue indices best first: substring hits on the row, then BM25 body
// hits; issues matching neither are left out. An empty query keeps every issue in order.
func (x *searchIndex) rank(q string) []int {
	typing := !strings.HasSuffix(q, " ") // the last word is still being typed, so it matches as a prefix
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		all := make([]int, len(x.heads))
		for i := range all {
			all[i] = i
		}
		return all
	}
	scores := map[int]float64{}
	words := searchTokens(q)
	for i, w := range words {
		postings := x.tf[w]
		if typing && i == len(words)-1 {
			postings = x.prefixPostings(w)
		}
		if len(postings) == 0 {
			continue
		}
		idf := math.Log(1 + (float64(len(x.heads))-float64(len(postings))+0.5)/(float64(len(postings))+0.5))
		for doc, tf := range postings {
			norm := bm25K1 * (1 - bm25B + bm25B*x.length[doc]/x.avgLen)
			scores[doc] += idf * tf * (bm25K1 + 1) / (tf + norm)
		}
	}
	var out []int
	hit := map[int]bool{}
	for i, h := range x.heads {
		if strings.Contains(h, q) {
			hit[i] = true
		}
		if hit[i] || scores[i] > 0 {
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if hit[out[a]] != hit[out[b]] {
			return hit[out[a]]
		}
		return scores[out[a]] > scores[out[b]]
	})
	return out
}

// prefixPostings merges the postings of every term starting with p, so the word still
// being typed matches as a prefix.
func (x *searchIndex) prefixPostings(p string) map[int]float64 {
	lo := sort.SearchStrings(x.vocab, p)
	hi := lo
	for hi < len(x.vocab) && strings.HasPrefix(x.vocab[hi], p) {
		hi++
	}
	if hi-lo == 1 {
		return x.tf[x.vocab[lo]]
	}
	merged := map[int]float64{}
	for _, t := range x.vocab[lo:hi] {
		for doc, tf := range x.tf[t] {
			merged[doc] += tf
		}
	}
	return merged
}
