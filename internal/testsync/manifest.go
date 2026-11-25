// Package testsync reads code-managed Semaloop tests from Markdown files.
package testsync

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"gopkg.in/yaml.v3"
)

// Test is one Markdown file: frontmatter for metadata, body for the spec.
type Test struct {
	Path        string
	ID          string
	Name        string
	Description string
	Folder      string
	Draft       bool
}

type frontmatter struct {
	ID     string `yaml:"id"`
	Name   string `yaml:"name"`
	Folder string `yaml:"folder"`
	Draft  bool   `yaml:"draft"`
}

// Matches the API's rule for external ids.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

const maxFolderLength = 40

// Load reads every .md file under paths (files or directories, recursively),
// sorted by path. It reports every invalid file rather than stopping at the first.
func Load(paths []string) ([]Test, error) {
	files, err := markdownFiles(paths)
	if err != nil {
		return nil, err
	}

	var tests []Test
	var errs []error
	seen := map[string]string{}
	for _, path := range files {
		t, err := parseFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		if other, ok := seen[t.ID]; ok {
			errs = append(errs, fmt.Errorf("%s: id %q is already used by %s", path, t.ID, other))
			continue
		}
		seen[t.ID] = path
		tests = append(tests, t)
	}
	return tests, errors.Join(errs...)
}

func markdownFiles(paths []string) ([]string, error) {
	var files []string
	for _, root := range paths {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// Skipping files without frontmatter instead would let a test whose
			// frontmatter got mangled silently drop out and be archived.
			if strings.EqualFold(d.Name(), "README.md") {
				return nil
			}
			if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".md") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("could not read %s: %w", root, err)
		}
	}
	sort.Strings(files)
	return files, nil
}

func parseFile(path string) (Test, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Test{}, err
	}
	return Parse(path, raw)
}

// Parse reads one test from Markdown with a leading `---` YAML frontmatter block.
func Parse(path string, raw []byte) (Test, error) {
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(raw, []byte("---\n"))
	if !ok {
		return Test{}, errors.New("missing frontmatter (the file must start with ---)")
	}
	head, body, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		head, ok = bytes.CutSuffix(rest, []byte("\n---"))
		if !ok {
			return Test{}, errors.New("unterminated frontmatter (no closing ---)")
		}
	}

	var fm frontmatter
	dec := yaml.NewDecoder(bytes.NewReader(head))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil {
		return Test{}, fmt.Errorf("invalid frontmatter: %w", err)
	}

	t := Test{
		Path:        path,
		ID:          strings.TrimSpace(fm.ID),
		Name:        strings.TrimSpace(fm.Name),
		Description: strings.TrimSpace(string(body)),
		Folder:      strings.TrimSpace(fm.Folder),
		Draft:       fm.Draft,
	}

	switch {
	case t.ID == "":
		return Test{}, errors.New("frontmatter is missing `id`")
	case !idPattern.MatchString(t.ID):
		return Test{}, fmt.Errorf("id %q may only contain letters, digits, '.', '_' and '-' (max 128)", t.ID)
	case t.Name == "":
		return Test{}, errors.New("frontmatter is missing `name`")
	case t.Description == "":
		return Test{}, errors.New("the test has no description (the body after the frontmatter is empty)")
	// The API counts UTF-16 code units (JavaScript's string length), not runes.
	case len(utf16.Encode([]rune(t.Folder))) > maxFolderLength:
		return Test{}, fmt.Errorf("folder %q is longer than %d characters", t.Folder, maxFolderLength)
	}
	return t, nil
}
