// Package testfile reads Semaloop test files: one Markdown file per test,
// with its settings in YAML frontmatter and the test itself, written in
// natural language, as the body.
//
//	---
//	identifier: sign-in
//	name: Sign in with email
//	folder: Smoke
//	tags: [smoke]
//	---
//
//	Open the app and sign in with the test account.
//
// Everything here works offline, so `semaloop test check` can run without an
// API key. Rules that need the app's data (secret slugs, capability names,
// takeovers) are left to the API.
package testfile

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// DefaultDir is where test files live, relative to the repository root.
const DefaultDir = ".semaloop/tests"

// FindDir returns the default test directory for a command run from wd:
// DefaultDir at the root of the Git repository containing wd, relative to wd
// where possible. Outside a repository it returns DefaultDir, relative to wd.
func FindDir(wd string) string {
	for dir := wd; ; {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			path := filepath.Join(dir, DefaultDir)
			if rel, err := filepath.Rel(wd, path); err == nil {
				return rel
			}
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return DefaultDir
		}
		dir = parent
	}
}

// Limits on tags, matching the dashboard's.
const (
	MaxTags      = 20
	MaxTagLength = 40
)

// Test is a parsed test file.
type Test struct {
	// Path is the file's path, as found under the directory passed to Load.
	Path string

	Identifier  string
	Name        string
	Description string

	// Folder is empty when the file doesn't set one.
	Folder string
	// Draft is nil when the file doesn't set it.
	Draft *bool

	// Tags and DisabledCapabilities are nil when the file leaves them out,
	// which means the test has none.
	Tags                 []string
	DisabledCapabilities []string
	// Secrets is nil when the file leaves it out, which is different from an
	// empty list: a new test gets the app's default secrets, and an existing
	// test keeps the ones it had.
	Secrets []string

	TakeOverTestID string

	// identifierLine is where identifier is set, for reporting duplicates.
	identifierLine int
}

// Error is a problem with a test file, at a given line.
type Error struct {
	Path    string
	Line    int
	Message string
}

func (e Error) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.Path, e.Line, e.Message)
}

// fields lists the frontmatter fields in the order they're documented.
var fields = []string{
	"identifier",
	"name",
	"folder",
	"draft",
	"tags",
	"secrets",
	"disabled_capabilities",
	"take_over_test_id",
}

// Load reads every .md file under dir, in lexical order, and checks each one
// and the set as a whole. It returns the tests that parsed and every error it
// found. The error result is only for problems reading dir itself.
func Load(dir string) ([]Test, []Error, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("%s does not exist", dir)
		}
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a directory", dir)
	}

	// parsed includes files with errors, so a duplicate identifier is
	// reported alongside them rather than after they're fixed.
	var tests, parsed []Test
	var errs []Error
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		t, fileErrs := Parse(path, data)
		errs = append(errs, fileErrs...)
		if t.Identifier != "" {
			parsed = append(parsed, t)
		}
		if len(fileErrs) == 0 {
			tests = append(tests, t)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	errs = append(errs, checkUnique(parsed)...)
	slices.SortStableFunc(errs, func(a, b Error) int {
		return cmp.Or(strings.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line))
	})
	return tests, errs, nil
}

// checkUnique reports identifiers used by more than one file.
func checkUnique(tests []Test) []Error {
	var errs []Error
	first := map[string]string{}
	for _, t := range tests {
		if prev, ok := first[t.Identifier]; ok {
			errs = append(errs, Error{
				Path:    t.Path,
				Line:    t.identifierLine,
				Message: fmt.Sprintf("identifier %q is already used by %s", t.Identifier, prev),
			})
			continue
		}
		first[t.Identifier] = t.Path
	}
	return errs
}

var yamlLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// Parse parses a single test file. path is only used in errors.
func Parse(path string, data []byte) (Test, []Error) {
	t := Test{Path: path}
	var errs []Error
	fail := func(line int, format string, args ...any) {
		errs = append(errs, Error{Path: path, Line: line, Message: fmt.Sprintf(format, args...)})
	}

	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	if len(lines) == 0 || !isFence(lines[0]) {
		fail(1, "missing frontmatter: start the file with a line containing --- followed by the test's settings")
		return t, errs
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if isFence(lines[i]) {
			end = i
			break
		}
	}
	if end < 0 {
		fail(1, "frontmatter is never closed: add a line containing --- after the test's settings")
		return t, errs
	}

	// Line n of the frontmatter is line n+1 of the file.
	const offset = 1
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &doc); err != nil {
		if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
			n, _ := strconv.Atoi(m[1])
			fail(n+offset, "invalid frontmatter: %s", m[2])
		} else {
			fail(1, "invalid frontmatter: %s", strings.TrimPrefix(err.Error(), "yaml: "))
		}
		return t, errs
	}

	// An empty frontmatter has no content node.
	root := &yaml.Node{Kind: yaml.MappingNode, Line: 1}
	if len(doc.Content) > 0 {
		root = doc.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		fail(root.Line+offset, "frontmatter must be a list of settings, one `key: value` per line")
		return t, errs
	}

	seen := map[string]int{}
	values := map[string]*yaml.Node{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		key := k.Value
		line := k.Line + offset
		if prev, ok := seen[key]; ok {
			fail(line, "%s is set more than once (first on line %d)", key, prev)
			continue
		}
		seen[key] = line
		if !slices.Contains(fields, key) {
			fail(line, "%s", unknownField(key))
			continue
		}
		values[key] = v
	}

	str := func(key string, required bool) string {
		v, ok := values[key]
		if !ok || isNull(v) {
			if required {
				line := 1
				if l, ok := seen[key]; ok {
					line = l
				}
				fail(line, "missing %s", key)
			}
			return ""
		}
		s, ok := scalarString(v)
		if !ok {
			fail(v.Line+offset, "%s must be text", key)
			return ""
		}
		s = strings.TrimSpace(s)
		if s == "" && required {
			fail(v.Line+offset, "%s must not be empty", key)
		}
		return s
	}
	list := func(key string) []string {
		v, ok := values[key]
		if !ok || isNull(v) {
			return nil
		}
		if v.Kind != yaml.SequenceNode {
			fail(v.Line+offset, "%s must be a list, e.g. %s: [first, second]", key, key)
			return nil
		}
		out := make([]string, 0, len(v.Content))
		for _, item := range v.Content {
			s, ok := scalarString(item)
			s = strings.TrimSpace(s)
			if !ok || s == "" {
				fail(item.Line+offset, "each item in %s must be non-empty text", key)
				continue
			}
			out = append(out, s)
		}
		return out
	}

	t.Identifier = str("identifier", true)
	t.identifierLine = seen["identifier"]
	t.Name = str("name", true)
	t.Folder = str("folder", false)
	t.TakeOverTestID = str("take_over_test_id", false)

	if v, ok := values["draft"]; ok && !isNull(v) {
		var b bool
		if v.Kind != yaml.ScalarNode || v.Tag != "!!bool" || v.Decode(&b) != nil {
			fail(v.Line+offset, "draft must be true or false")
		} else {
			t.Draft = &b
		}
	}

	t.Tags = list("tags")
	checkTags(t.Tags, seen["tags"], fail)
	t.Secrets = list("secrets")
	t.DisabledCapabilities = list("disabled_capabilities")

	t.Description = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	if t.Description == "" {
		fail(end+1, "missing test: write the test in natural language below the frontmatter")
	}

	slices.SortStableFunc(errs, func(a, b Error) int { return cmp.Compare(a.Line, b.Line) })
	return t, errs
}

func checkTags(tags []string, line int, fail func(int, string, ...any)) {
	distinct := map[string]string{}
	for _, tag := range tags {
		if utf8.RuneCountInString(tag) > MaxTagLength {
			fail(line, "tag %q is longer than %d characters", tag, MaxTagLength)
		}
		key := strings.ToLower(tag)
		if prev, ok := distinct[key]; ok {
			if prev == tag {
				fail(line, "tag %q is listed more than once", tag)
			} else {
				fail(line, "tags %q and %q are the same tag: tags ignore case", prev, tag)
			}
			continue
		}
		distinct[key] = tag
	}
	if len(distinct) > MaxTags {
		fail(line, "a test can have at most %d tags, but this one has %d", MaxTags, len(distinct))
	}
}

// isFence reports whether line opens or closes the frontmatter. Both fences
// must start at the beginning of the line; trailing whitespace is allowed.
func isFence(line string) bool {
	return strings.TrimRight(line, " \t") == "---"
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// scalarString accepts any scalar except booleans and null, so that numbers
// such as `identifier: 404` are read as text.
func scalarString(n *yaml.Node) (string, bool) {
	if n.Kind != yaml.ScalarNode {
		return "", false
	}
	switch n.Tag {
	case "!!bool", "!!null":
		return "", false
	}
	return n.Value, true
}

// unknownField explains an unknown frontmatter key, suggesting the field that
// was probably meant.
func unknownField(key string) string {
	switch key {
	case "id":
		return "unknown field id: use identifier instead"
	case "description", "spec", "steps", "body":
		return fmt.Sprintf("unknown field %s: write the test below the frontmatter instead", key)
	}
	if s := suggest(key); s != "" {
		return fmt.Sprintf("unknown field %s: did you mean %s?", key, s)
	}
	return fmt.Sprintf("unknown field %s: expected one of %s", key, strings.Join(fields, ", "))
}

// suggest returns the known field closest to key, or "" if none is close.
// Keys written in camelCase or with dashes match their snake_case field.
func suggest(key string) string {
	norm := normalise(key)
	best, bestDist := "", -1
	for _, f := range fields {
		if normalise(f) == norm {
			return f
		}
		d := levenshtein(norm, normalise(f))
		if bestDist < 0 || d < bestDist {
			best, bestDist = f, d
		}
	}
	// Allow roughly one typo per three characters, so `tag` matches `tags`
	// but `foo` doesn't match `name`.
	if bestDist <= max(1, utf8.RuneCountInString(norm)/3) {
		return best
	}
	return ""
}

// normalise lowercases key and drops underscores and dashes.
func normalise(key string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
}

func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}
