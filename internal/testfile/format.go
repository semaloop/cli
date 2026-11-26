package testfile

import (
	"bytes"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
	"golang.org/x/text/unicode/norm"
)

// Format renders t as a test file that Parse reads back as t. Every setting is
// written out, apart from a folder or take_over_test_id that isn't set, so the
// file says exactly what a sync will send. Secrets are left out only when
// they're nil, which keeps a test's secrets as they are.
func Format(t Test) []byte {
	m := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, value *yaml.Node) {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
	}
	str := func(s string) *yaml.Node {
		n := &yaml.Node{}
		// Encoding a string can't fail.
		_ = n.Encode(strings.TrimSpace(s))
		return n
	}
	list := func(items []string) *yaml.Node {
		n := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Content: []*yaml.Node{}}
		for _, item := range items {
			n.Content = append(n.Content, str(item))
		}
		return n
	}

	add("identifier", str(t.Identifier))
	add("name", str(t.Name))
	if t.Folder != "" {
		add("folder", str(t.Folder))
	}
	draft := "false"
	if t.Draft != nil && *t.Draft {
		draft = "true"
	}
	add("draft", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: draft})
	add("tags", list(t.Tags))
	if t.Secrets != nil {
		add("secrets", list(t.Secrets))
	}
	add("disabled_capabilities", list(t.DisabledCapabilities))
	if t.TakeOverTestID != "" {
		add("take_over_test_id", str(t.TakeOverTestID))
	}

	var b bytes.Buffer
	b.WriteString("---\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	// Encoding nodes built from strings can't fail.
	_ = enc.Encode(m)
	_ = enc.Close()
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(t.Description))
	b.WriteString("\n")
	return b.Bytes()
}

// Slug turns a name into lowercase words joined by dashes, e.g. "Sign in
// with email" into "sign-in-with-email", for identifiers and file and
// directory names. Accents are dropped, so "Café" becomes "cafe". It's empty
// when name has no ASCII letters or digits.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFD.String(strings.ToLower(name)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	return b.String()
}
