package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthClientSetsAuthorizationHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	c := &AuthClient{APIKey: "test-key"}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	c.Do(req)

	if got != "Bearer test-key" {
		t.Errorf("expected 'Bearer test-key', got %q", got)
	}
}

func TestAuthClientSetsExtraHeaders(t *testing.T) {
	t.Setenv("SEMALOOP_EXTRA_HEADERS", "X-Foo=foo-value, X-Bar=bar-value")

	var gotFoo, gotBar string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFoo = r.Header.Get("X-Foo")
		gotBar = r.Header.Get("X-Bar")
	}))
	defer srv.Close()

	c := &AuthClient{APIKey: "key"}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	c.Do(req)

	if gotFoo != "foo-value" {
		t.Errorf("expected X-Foo 'foo-value', got %q", gotFoo)
	}
	if gotBar != "bar-value" {
		t.Errorf("expected X-Bar 'bar-value', got %q", gotBar)
	}
}

func TestAuthClientOmitsExtraHeadersWhenUnset(t *testing.T) {
	t.Setenv("SEMALOOP_EXTRA_HEADERS", "")

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Foo")
	}))
	defer srv.Close()

	c := &AuthClient{APIKey: "key"}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	c.Do(req)

	if got != "" {
		t.Errorf("expected no X-Foo header, got %q", got)
	}
}
