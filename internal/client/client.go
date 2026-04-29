package client

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/charmbracelet/log"
)

const DefaultAPIURL = "https://api.semaloop.com"

var (
	ErrUnauthorized = errors.New("invalid API key")
	ErrForbidden    = errors.New("account does not have permission to upload builds")
)

// ServerURL returns the API server URL, falling back to DefaultAPIURL.
func ServerURL() string {
	if url := os.Getenv("SEMALOOP_API_URL"); url != "" {
		return url
	}
	return DefaultAPIURL
}

// AuthClient wraps an HTTP client and injects authentication headers.
type AuthClient struct {
	APIKey string
}

func (a *AuthClient) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	for _, pair := range strings.Split(os.Getenv("SEMALOOP_EXTRA_HEADERS"), ",") {
		name, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		if name = strings.TrimSpace(name); name != "" {
			req.Header.Set(name, strings.TrimSpace(value))
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	log.Debug("Response received", "url", req.URL.String(), "status", resp.StatusCode)

	return resp, nil
}
