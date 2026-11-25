package cmd

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/semaloop/cli/internal/testfile"
)

// DashboardTest is one of the app's tests, with every setting a test file
// carries.
type DashboardTest struct {
	ID          string
	Name        string
	Description string
	Folder      string
	Draft       bool
	Tags        []string
	// Secrets are the test's secrets, by slug.
	Secrets []string
	// DisabledCapabilities are only the capabilities that are disabled. The
	// rest come from the test itself.
	DisabledCapabilities []string
	// ExternalID is the test's identifier when it's in code, and empty when
	// it's only in the dashboard.
	ExternalID string
}

// PullOptions configures PlanPull.
type PullOptions struct {
	// Force rewrites the files of tests that already have one.
	Force bool
	// Folders, when set, limits the tests to those in these folders.
	Folders []string
	// TestIDs, when set, limits the tests to these.
	TestIDs []string
}

// PulledFile is a test file to write.
type PulledFile struct {
	Path string
	Test testfile.Test
	// Replaces is true when the file exists and is this test's.
	Replaces bool
	// original is the file's contents when the pull was planned, if it
	// Replaces one. WritePull only replaces a file that still has them.
	original []byte
}

// PullPlan is what PlanPull would write.
type PullPlan struct {
	Files []PulledFile
	// Skipped are tests that already have a file.
	Skipped []PulledFile
}

// PlanPull works out a file for each of the app's dashboard tests that isn't
// in code yet, under dir. local are the files already there.
//
// Each file goes in a directory named after the test's folder, with an
// identifier and file name made from the test's name. Identifiers are made
// unique among the files and the tests already in code. Each file names its
// test in take_over_test_id, so the first sync takes the test over, keeping
// its history, instead of creating a new one. A test that already has a file,
// from an earlier pull, is skipped unless opts.Force is set.
//
// It returns an error if a file can't be planned, e.g. because a file is in
// the way of a folder's directory.
func PlanPull(dir string, remote []DashboardTest, local []testfile.Test, opts PullOptions) (PullPlan, error) {
	taken := map[string]bool{}
	paths := map[string]bool{}
	byTakeOver := map[string]testfile.Test{}
	for _, t := range local {
		taken[t.Identifier] = true
		paths[filepath.Clean(t.Path)] = true
		if t.TakeOverTestID != "" {
			byTakeOver[t.TakeOverTestID] = t
		}
	}
	for _, r := range remote {
		if r.ExternalID != "" {
			taken[r.ExternalID] = true
		}
	}

	// Sorted, so the same tests always get the same identifiers.
	remote = slices.Clone(remote)
	slices.SortStableFunc(remote, func(a, b DashboardTest) int {
		return cmp.Or(cmp.Compare(a.Folder, b.Folder), cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})

	var plan PullPlan
	for _, r := range remote {
		if r.ExternalID != "" ||
			(len(opts.Folders) > 0 && !slices.Contains(opts.Folders, r.Folder)) ||
			(len(opts.TestIDs) > 0 && !slices.Contains(opts.TestIDs, r.ID)) {
			continue
		}

		if existing, ok := byTakeOver[r.ID]; ok {
			f := PulledFile{Path: existing.Path, Test: fileTest(r, existing.Identifier, existing.Path), Replaces: true}
			if !opts.Force {
				plan.Skipped = append(plan.Skipped, f)
				continue
			}
			original, err := ownFile(existing.Path, r.ID)
			if err != nil {
				return PullPlan{}, err
			}
			f.original = original
			plan.Files = append(plan.Files, f)
			continue
		}

		folderDir := dir
		if r.Folder != "" {
			folderDir = filepath.Join(dir, cmp.Or(testfile.Slug(r.Folder), "folder"))
		}
		base := cmp.Or(testfile.Slug(r.Name), "test")
		id, path := base, ""
		for n := 2; ; n++ {
			path = filepath.Join(folderDir, id+".md")
			if !taken[id] && !paths[path] {
				used, err := occupied(path)
				if err != nil {
					return PullPlan{}, err
				}
				if !used {
					break
				}
			}
			id = base + "-" + strconv.Itoa(n)
		}
		taken[id] = true
		paths[path] = true
		plan.Files = append(plan.Files, PulledFile{Path: path, Test: fileTest(r, id, path)})
	}
	return plan, nil
}

func fileTest(r DashboardTest, identifier, path string) testfile.Test {
	draft := r.Draft
	return testfile.Test{
		Path:        path,
		Identifier:  identifier,
		Name:        r.Name,
		Description: r.Description,
		Folder:      r.Folder,
		Draft:       &draft,
		Tags:        r.Tags,
		// Written even when empty, so the file says exactly which secrets
		// the test has.
		Secrets:              orEmpty(r.Secrets),
		DisabledCapabilities: r.DisabledCapabilities,
		TakeOverTestID:       r.ID,
	}
}

// occupied reports whether something is at path, so a new file can't go
// there. Any error other than there being nothing at path, such as part of
// the path being a file, is returned: another file name wouldn't avoid it.
func occupied(path string) (bool, error) {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("can't write %s: %w", path, err)
	}
}

// ownFile reads the file at path, checking it's still the file of the
// dashboard test with id testID.
func ownFile(path, testID string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}
	if t, errs := testfile.Parse(path, data); len(errs) > 0 || t.TakeOverTestID != testID {
		return nil, fmt.Errorf("%s has changed and is no longer the file of test %s, so it won't be replaced", path, testID)
	}
	return data, nil
}

// ErrPullConflict is returned when a file to replace has changed since the
// pull was planned.
var ErrPullConflict = errors.New("changed since the pull was planned, so it wasn't replaced")

// WritePull writes the plan's files. It never replaces a file that isn't the
// test's own: a new file is only written where there's nothing, and a file is
// only replaced if it's unchanged since PlanPull read it.
func WritePull(plan PullPlan) error {
	for _, f := range plan.Files {
		data := testfile.Format(f.Test)
		if f.Replaces {
			if err := replaceFile(f.Path, f.original, data); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(f.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fmt.Errorf("could not write %s: %w", f.Path, err)
		}
		_, err = file.Write(data)
		if cerr := file.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("could not write %s: %w", f.Path, err)
		}
	}
	return nil
}

// replaceFile writes data to the file at path if it still has the contents
// original.
//
// The contents are checked through the same open file that's written, and
// that file is checked to be the one at path, not a link to another. So if
// the file is replaced after the check, the replacement isn't written to.
func replaceFile(path string, original, data []byte) (err error) {
	if original == nil {
		return fmt.Errorf("%s: %w", path, ErrPullConflict)
	}
	before, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", path, ErrPullConflict)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	defer func() {
		if cerr := file.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("could not write %s: %w", path, cerr)
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	if !os.SameFile(before, opened) {
		return fmt.Errorf("%s: %w", path, ErrPullConflict)
	}
	current, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("could not read %s: %w", path, err)
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("%s: %w", path, ErrPullConflict)
	}
	// Write over the old contents before truncating, so a failed write
	// doesn't leave the file empty.
	if _, err := file.WriteAt(data, 0); err != nil {
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	if err := file.Truncate(int64(len(data))); err != nil {
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	return nil
}

// Pull writes a file under dir for each of remote's tests that isn't in code
// yet, then writes what it did to out. The test files already in dir must be
// valid, so new identifiers can't clash with them.
func Pull(dir string, remote []DashboardTest, out io.Writer, opts PullOptions, asJSON bool) (PullPlan, error) {
	var local []testfile.Test
	if _, err := os.Stat(dir); err == nil {
		if local, err = LoadTests(dir, out, asJSON); err != nil {
			return PullPlan{}, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return PullPlan{}, err
	}

	plan, err := PlanPull(dir, remote, local, opts)
	if err != nil {
		return PullPlan{}, err
	}
	if err := WritePull(plan); err != nil {
		return PullPlan{}, err
	}
	if asJSON {
		writePullJSON(out, plan)
	} else {
		writePullText(out, plan)
	}
	return plan, nil
}

func writePullText(out io.Writer, plan PullPlan) {
	for _, f := range plan.Files {
		symbol := "+"
		if f.Replaces {
			symbol = "~"
		}
		fmt.Fprintf(out, "  %s %s\n", symbol, f.Path)
	}
	if len(plan.Files) > 0 {
		fmt.Fprintln(out)
	}
	if n := len(plan.Skipped); n > 0 {
		fmt.Fprintf(out, "Skipped %s that already have a file. Pass --force to rewrite them.\n", plural(n, "test"))
	}
	if len(plan.Files) == 0 {
		if len(plan.Skipped) == 0 {
			fmt.Fprintln(out, "No dashboard tests to pull.")
		}
		return
	}
	fmt.Fprintf(out, "Wrote %s. Commit them, then run `semaloop test sync` to move the tests into code.\n", plural(len(plan.Files), "file"))
}

type jsonPulledFile struct {
	Path       string `json:"path"`
	Identifier string `json:"identifier"`
	TestID     string `json:"testId"`
	Replaced   bool   `json:"replaced,omitempty"`
}

func writePullJSON(out io.Writer, plan PullPlan) {
	res := struct {
		Written []jsonPulledFile `json:"written"`
		Skipped []jsonPulledFile `json:"skipped"`
	}{[]jsonPulledFile{}, []jsonPulledFile{}}
	for _, f := range plan.Files {
		res.Written = append(res.Written, jsonPulledFile{f.Path, f.Test.Identifier, f.Test.TakeOverTestID, f.Replaces})
	}
	for _, f := range plan.Skipped {
		res.Skipped = append(res.Skipped, jsonPulledFile{f.Path, f.Test.Identifier, f.Test.TakeOverTestID, false})
	}
	writeJSON(out, res)
}
