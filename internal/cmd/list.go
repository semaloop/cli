package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/semaloop/cli/internal/api"
	"github.com/semaloop/cli/internal/client"
)

// FetchTests lists the app's tests, both in code and in the dashboard.
func FetchTests(ctx context.Context, apiKey, serverURL string) ([]DashboardTest, error) {
	c, err := api.NewClient(serverURL, api.WithClient(&client.AuthClient{APIKey: apiKey}))
	if err != nil {
		return nil, fmt.Errorf("could not initialise API client: %w", err)
	}
	return listTests(ctx, c)
}

func listTests(ctx context.Context, c *api.Client) ([]DashboardTest, error) {
	res, err := c.GetListTests(ctx)
	if err != nil {
		switch code := statusCode(err); {
		case code == http.StatusForbidden:
			return nil, client.ErrForbidden
		case code != 0:
			return nil, fmt.Errorf("unexpected response from Semaloop: %d %s", code, http.StatusText(code))
		}
		return nil, fmt.Errorf("could not get the tests from Semaloop: %w", err)
	}
	switch r := res.(type) {
	case *api.ListTestsResponse:
		tests := make([]DashboardTest, len(r.Tests))
		for i, t := range r.Tests {
			tests[i] = dashboardTest(t)
		}
		return tests, nil
	case *api.GetListTestsForbidden:
		return nil, client.ErrForbidden
	case *api.GetListTestsUnauthorized:
		return nil, client.ErrUnauthorized
	default:
		return nil, fmt.Errorf("unexpected response from server (%T)", res)
	}
}

func dashboardTest(t api.ListedTest) DashboardTest {
	d := DashboardTest{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		Folder:      t.Folder.Value,
		Draft:       t.Draft,
		Tags:        t.Tags,
		Secrets:     t.Secrets,
		ExternalID:  t.ExternalId.Value,
	}
	for _, c := range t.Capabilities {
		if c.Disabled {
			d.DisabledCapabilities = append(d.DisabledCapabilities, snakeCase(string(c.Name)))
		}
	}
	return d
}

// ListFilter limits which tests ListTests shows. Each filter that's set must
// match.
type ListFilter struct {
	// Tags, when set, keeps tests with any of these tags.
	Tags []string
	// Folders, when set, keeps tests in any of these folders.
	Folders     []string
	InCode      bool
	InDashboard bool
}

func (f ListFilter) matches(t DashboardTest) bool {
	switch {
	case len(f.Tags) > 0 && !slices.ContainsFunc(t.Tags, func(tag string) bool { return slices.Contains(f.Tags, tag) }):
		return false
	case len(f.Folders) > 0 && !slices.Contains(f.Folders, t.Folder):
		return false
	case f.InCode && t.ExternalID == "":
		return false
	case f.InDashboard && t.ExternalID != "":
		return false
	}
	return true
}

// FilterTests returns the tests that match f, in folder then name order.
func FilterTests(tests []DashboardTest, f ListFilter) []DashboardTest {
	var out []DashboardTest
	for _, t := range tests {
		if f.matches(t) {
			out = append(out, t)
		}
	}
	slices.SortStableFunc(out, func(a, b DashboardTest) int {
		if c := strings.Compare(a.Folder, b.Folder); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

type jsonListedTest struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Folder     string   `json:"folder,omitempty"`
	Draft      bool     `json:"draft"`
	Tags       []string `json:"tags"`
	Identifier string   `json:"identifier,omitempty"`
}

// WriteTestList writes tests as a table, or as JSON.
func WriteTestList(out io.Writer, tests []DashboardTest, asJSON bool) {
	if asJSON {
		res := []jsonListedTest{}
		for _, t := range tests {
			res = append(res, jsonListedTest{t.ID, t.Name, t.Folder, t.Draft, orEmpty(t.Tags), t.ExternalID})
		}
		writeJSON(out, res)
		return
	}
	if len(tests) == 0 {
		fmt.Fprintln(out, "No tests.")
		return
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tFOLDER\tTAGS\tIN CODE AS")
	for _, t := range tests {
		name := t.Name
		if t.Draft {
			name += " (draft)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.ID, name, t.Folder, strings.Join(t.Tags, ", "), t.ExternalID)
	}
	tw.Flush()
	for line := range strings.Lines(b.String()) {
		fmt.Fprintln(out, strings.TrimRight(line, " \n"))
	}
}
