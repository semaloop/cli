package cmd

import (
	"strings"
	"unicode"
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

// maxDiffCells bounds the work diffTokens does, after it trims what the two
// sides have in common at each end.
const maxDiffCells = 1 << 18

// diffTokens returns the edits that turn a into b, from their longest common
// subsequence. Within each run of changes, deletes come before inserts. ok is
// false when a and b differ too much to diff quickly.
func diffTokens(a, b []string) (edits []edit, ok bool) {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	am, bm := a[pre:len(a)-suf], b[pre:len(b)-suf]
	if (len(am)+1)*(len(bm)+1) > maxDiffCells {
		return nil, false
	}

	for _, t := range a[:pre] {
		edits = append(edits, edit{editEqual, t})
	}
	edits = append(edits, lcsEdits(am, bm)...)
	for _, t := range a[len(a)-suf:] {
		edits = append(edits, edit{editEqual, t})
	}
	return edits, true
}

func lcsEdits(a, b []string) []edit {
	n, m := len(a), len(b)
	w := m + 1
	// l[i*w+j] is the length of the longest common subsequence of a[i:] and
	// b[j:].
	l := make([]int, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				l[i*w+j] = l[(i+1)*w+j+1] + 1
			} else {
				l[i*w+j] = max(l[(i+1)*w+j], l[i*w+j+1])
			}
		}
	}

	var edits, deletes, inserts []edit
	flush := func() {
		edits = append(append(edits, deletes...), inserts...)
		deletes, inserts = deletes[:0], inserts[:0]
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			flush()
			edits = append(edits, edit{editEqual, a[i]})
			i++
			j++
		case j == m || (i < n && l[(i+1)*w+j] >= l[i*w+j+1]):
			deletes = append(deletes, edit{editDelete, a[i]})
			i++
		default:
			inserts = append(inserts, edit{editInsert, b[j]})
			j++
		}
	}
	flush()
	return edits
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
	edits, ok = diffTokens(words(a), words(b))
	if !ok {
		return nil, false
	}
	edits = joinChanges(edits)
	same := 0
	for _, e := range edits {
		if e.kind == editEqual {
			same += visible(e.text)
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

// joinChanges makes the spaces between two changed words part of the change,
// so replaced words read as one phrase, e.g. [-every workspace-]{+all open
// workspaces+}, rather than word by word.
func joinChanges(edits []edit) []edit {
	var out, deletes, inserts []edit
	flush := func() {
		// Spaces both sides of the change share aren't part of it.
		lead := 0
		for lead < min(len(deletes), len(inserts)) && deletes[lead].text == inserts[lead].text && isSpace(deletes[lead].text) {
			out = append(out, edit{editEqual, deletes[lead].text})
			lead++
		}
		deletes, inserts = deletes[lead:], inserts[lead:]
		var trail []edit
		for len(deletes) > 0 && len(inserts) > 0 && deletes[len(deletes)-1].text == inserts[len(inserts)-1].text && isSpace(deletes[len(deletes)-1].text) {
			trail = append([]edit{{editEqual, deletes[len(deletes)-1].text}}, trail...)
			deletes, inserts = deletes[:len(deletes)-1], inserts[:len(inserts)-1]
		}
		out = append(append(append(out, deletes...), inserts...), trail...)
		deletes, inserts = nil, nil
	}
	for i, e := range edits {
		switch {
		case e.kind == editDelete:
			deletes = append(deletes, e)
		case e.kind == editInsert:
			inserts = append(inserts, e)
		case len(deletes)+len(inserts) > 0 && i+1 < len(edits) && edits[i+1].kind != editEqual && strings.TrimSpace(e.text) == "":
			deletes = append(deletes, edit{editDelete, e.text})
			inserts = append(inserts, edit{editInsert, e.text})
		default:
			flush()
			out = append(out, e)
		}
	}
	flush()
	return out
}
