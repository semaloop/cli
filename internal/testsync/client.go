package testsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/semaloop/cli/internal/client"
)

// Change is one line of the plan the API returns.
type Change struct {
	ID     string   `json:"id"`
	Action string   `json:"action"`
	TestID *string  `json:"testId"`
	Fields []string `json:"fields,omitempty"`
}

type syncTest struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Folder      *string `json:"folder"`
	Draft       bool    `json:"draft"`
}

type syncRequest struct {
	Tests        []syncTest `json:"tests"`
	DryRun       bool       `json:"dryRun"`
	BaseRevision *int       `json:"baseRevision,omitempty"`
}

// Result is what a sync did, or would do on a dry run.
type Result struct {
	Changes []Change
	// Revision is the app's test revision after the sync. Pass a dry run's
	// revision as SyncOptions.BaseRevision to apply that plan only if nothing
	// synced in between.
	Revision int
}

// SyncOptions configures a Sync.
type SyncOptions struct {
	DryRun       bool
	BaseRevision *int
}

// ErrConflict means another sync is running, or the base revision is stale.
var ErrConflict = errors.New("sync conflict")

type syncResponse struct {
	Changes  []Change `json:"changes"`
	Revision int      `json:"revision"`
	Errors   []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// Sync makes the app's code-managed tests match tests.
//
// The endpoint is called by hand rather than through internal/api: regenerating
// that client from the current API spec changes the upload response types.
func Sync(ctx context.Context, apiKey, serverURL string, tests []Test, opts SyncOptions) (Result, error) {
	body := syncRequest{Tests: make([]syncTest, len(tests)), DryRun: opts.DryRun, BaseRevision: opts.BaseRevision}
	for i, t := range tests {
		var folder *string
		if t.Folder != "" {
			folder = &t.Folder
		}
		body.Tests[i] = syncTest{ID: t.ID, Name: t.Name, Description: t.Description, Folder: folder, Draft: t.Draft}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Result{}, err
	}

	url := strings.TrimSuffix(serverURL, "/") + "/api/v1/tests/sync"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := (&client.AuthClient{APIKey: apiKey}).Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("could not connect to Semaloop: %w", err)
	}
	defer res.Body.Close()

	var decoded syncResponse
	decodeErr := json.NewDecoder(res.Body).Decode(&decoded)

	message := ""
	if decodeErr == nil && len(decoded.Errors) > 0 {
		message = decoded.Errors[0].Message
	}

	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return Result{}, client.ErrUnauthorized
	case res.StatusCode == http.StatusConflict:
		return Result{}, fmt.Errorf("%w: %s", ErrConflict, message)
	case res.StatusCode != http.StatusOK && message != "":
		return Result{}, fmt.Errorf("sync rejected: %s", message)
	case res.StatusCode != http.StatusOK:
		return Result{}, fmt.Errorf("unexpected response from server (HTTP %d)", res.StatusCode)
	case decodeErr != nil:
		return Result{}, fmt.Errorf("could not read response: %w", decodeErr)
	}
	return Result{Changes: decoded.Changes, Revision: decoded.Revision}, nil
}
