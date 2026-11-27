package testfile

import (
	"bytes"
	"errors"
	"reflect"
	"regexp"
)

// takeOverLine matches a line that sets take_over_test_id at the top level of
// the frontmatter.
var takeOverLine = regexp.MustCompile(`^take_over_test_id[ \t]*:`)

// ErrCantRemoveTakeOver is returned when take_over_test_id can't be removed
// from a file by deleting the one line that sets it, e.g. because its value
// spans more than one line.
var ErrCantRemoveTakeOver = errors.New("take_over_test_id can't be removed from this file automatically")

// RemoveTakeOver returns the contents of a test file, data, without its
// take_over_test_id. Only the line that sets it is removed, so the rest of the
// file is left exactly as it was. It returns ErrCantRemoveTakeOver unless the
// result is the same test without a take_over_test_id.
func RemoveTakeOver(path string, data []byte) ([]byte, error) {
	before, errs := Parse(path, data)
	if len(errs) > 0 || before.TakeOverTestID == "" {
		return nil, ErrCantRemoveTakeOver
	}

	lines := bytes.SplitAfter(data, []byte("\n"))
	found := -1
	// Line 0 is the frontmatter's opening fence.
	for i := 1; i < len(lines); i++ {
		line := string(bytes.TrimRight(lines[i], "\r\n"))
		if isFence(line) {
			break
		}
		if takeOverLine.MatchString(line) {
			if found >= 0 {
				return nil, ErrCantRemoveTakeOver
			}
			found = i
		}
	}
	if found < 0 {
		return nil, ErrCantRemoveTakeOver
	}
	out := bytes.Join(append(lines[:found:found], lines[found+1:]...), nil)

	after, errs := Parse(path, out)
	if len(errs) > 0 {
		return nil, ErrCantRemoveTakeOver
	}
	want := before
	want.TakeOverTestID = ""
	// Removing a line before identifier moves it up a line.
	want.identifierLine, after.identifierLine = 0, 0
	if !reflect.DeepEqual(after, want) {
		return nil, ErrCantRemoveTakeOver
	}
	return out, nil
}
