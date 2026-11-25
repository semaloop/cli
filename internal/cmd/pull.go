package cmd

import (
	"cmp"
	"errors"
	"fmt"
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
func PlanPull(dir string, remote []DashboardTest, local []testfile.Test, opts PullOptions) PullPlan {
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
			if opts.Force {
				plan.Files = append(plan.Files, f)
			} else {
				plan.Skipped = append(plan.Skipped, f)
			}
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
			if !taken[id] && !paths[path] && !exists(path) {
				break
			}
			id = base + "-" + strconv.Itoa(n)
		}
		taken[id] = true
		paths[path] = true
		plan.Files = append(plan.Files, PulledFile{Path: path, Test: fileTest(r, id, path)})
	}
	return plan
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

func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

// WritePull writes the plan's files. It never replaces a file that isn't the
// test's own.
func WritePull(plan PullPlan) error {
	for _, f := range plan.Files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return err
		}
		flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
		if f.Replaces {
			flags = os.O_WRONLY | os.O_TRUNC
		}
		file, err := os.OpenFile(f.Path, flags, 0o644)
		if err != nil {
			return fmt.Errorf("could not write %s: %w", f.Path, err)
		}
		_, err = file.Write(testfile.Format(f.Test))
		if cerr := file.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("could not write %s: %w", f.Path, err)
		}
	}
	return nil
}
