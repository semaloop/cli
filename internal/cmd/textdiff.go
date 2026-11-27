package cmd

import (
	"strings"
	"time"
	"unicode"

	"github.com/sergi/go-diff/diffmatchpatch"
)

type editKind int

const (
	editEqual editKind = iota
	editDelete
	editInsert
)

type edit struct {
	kind editKind
	text string
}

// newDiffer returns a diff-match-patch that gives up on finding the smallest
// diff quickly, so no description holds up a plan. Its diff is still correct,
// only longer.
func newDiffer() *diffmatchpatch.DiffMatchPatch {
	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = 100 * time.Millisecond
	return dmp
}

// diffTokens diffs a and b as sequences of tokens, such as lines or words,
// and returns one edit per run of tokens. With semantic set, it shifts and
// merges the edits so they read as whole phrases rather than scattered
// tokens, e.g. [-every workspace-]{+all open workspaces+}.
func diffTokens(a, b []string, semantic bool) []edit {
	// diff-match-patch diffs runes, so give each distinct token a rune of its
	// own.
	ids := map[string]rune{}
	var tokens []string
	encode := func(ts []string) []rune {
		rs := make([]rune, len(ts))
		for i, t := range ts {
			id, ok := ids[t]
			if !ok {
				id = rune(len(tokens))
				ids[t] = id
				tokens = append(tokens, t)
			}
			rs[i] = id
		}
		return rs
	}
	dmp := newDiffer()
	diffs := dmp.DiffMainRunes(encode(a), encode(b), false)

	var edits []edit
	for _, d := range diffs {
		kind := kindOf(d.Type)
		if semantic {
			var text strings.Builder
			for _, id := range d.Text {
				text.WriteString(tokens[id])
			}
			edits = append(edits, edit{kind, text.String()})
			continue
		}
		for _, id := range d.Text {
			edits = append(edits, edit{kind, tokens[id]})
		}
	}
	if semantic {
		edits = cleanupSemantic(dmp, edits)
	}
	return edits
}

// ops are the diff-match-patch operations, by editKind.
var ops = [...]diffmatchpatch.Operation{
	editEqual:  diffmatchpatch.DiffEqual,
	editDelete: diffmatchpatch.DiffDelete,
	editInsert: diffmatchpatch.DiffInsert,
}

func kindOf(op diffmatchpatch.Operation) editKind {
	for kind, o := range ops {
		if o == op {
			return editKind(kind)
		}
	}
	return editEqual
}

func cleanupSemantic(dmp *diffmatchpatch.DiffMatchPatch, edits []edit) []edit {
	diffs := make([]diffmatchpatch.Diff, len(edits))
	for i, e := range edits {
		diffs[i] = diffmatchpatch.Diff{Type: ops[e.kind], Text: e.text}
	}
	diffs = dmp.DiffCleanupSemantic(diffs)
	out := make([]edit, len(diffs))
	for i, d := range diffs {
		out[i] = edit{kindOf(d.Type), d.Text}
	}
	return out
}

// words splits s into words, runs of spaces, and punctuation, so a diff of
// them can be joined back into the text.
func words(s string) []string {
	var tokens []string
	start, class := 0, -1
	for i, r := range s {
		c := runeClass(r)
		// Punctuation is a token of its own, so a word that gains a full
		// stop, or loses one, is still the same word.
		if i > start && (c != class || c == classOther) {
			tokens = append(tokens, s[start:i])
			start = i
		}
		class = c
	}
	if start < len(s) {
		tokens = append(tokens, s[start:])
	}
	return tokens
}

const (
	classSpace = iota
	classWord
	classOther
)

func runeClass(r rune) int {
	switch {
	case unicode.IsSpace(r):
		return classSpace
	case unicode.IsLetter(r), unicode.IsDigit(r), r == '_', r == '\'':
		return classWord
	default:
		return classOther
	}
}

// lines splits text into its lines, ignoring the spaces around it and
// carriage returns, which a file's description never has. Empty text has
// none.
func lines(text string) []string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// wordDiff diffs two versions of a line by word. ok is false when the line was
// mostly rewritten, so a word diff wouldn't help: less than half of the
// longer version's text is in both.
func wordDiff(a, b string) (edits []edit, ok bool) {
	same := 0
	for _, e := range diffTokens(words(a), words(b), true) {
		if e.kind != editEqual {
			edits = append(edits, e)
			continue
		}
		same += visible(e.text)
		// Split what's unchanged back into words, so a few of them can be
		// shown either side of a change.
		for _, w := range words(e.text) {
			edits = append(edits, edit{editEqual, w})
		}
	}
	if longest := max(visible(a), visible(b)); longest > 0 && same*2 < longest {
		return nil, false
	}
	return edits, true
}

// visible counts s's characters that aren't spaces.
func visible(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

func isSpace(s string) bool {
	return strings.TrimFunc(s, unicode.IsSpace) == ""
}
