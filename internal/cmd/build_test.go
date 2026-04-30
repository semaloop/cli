package cmd

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semaloop/cli/internal/client"
)

func TestUploadFileSendsPUT(t *testing.T) {
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
	}))
	defer srv.Close()

	f := writeTempFile(t, "hello")
	UploadFile(f, srv.URL)

	if method != http.MethodPut {
		t.Errorf("expected PUT, got %s", method)
	}
}

func TestUploadFileSetsContentLength(t *testing.T) {
	content := "hello world"
	var contentLength int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentLength = r.ContentLength
	}))
	defer srv.Close()

	f := writeTempFile(t, content)
	UploadFile(f, srv.URL)

	if contentLength != int64(len(content)) {
		t.Errorf("expected Content-Length %d, got %d", len(content), contentLength)
	}
}

func TestUploadFileSendsFileContents(t *testing.T) {
	content := "build artifact contents"
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}))
	defer srv.Close()

	f := writeTempFile(t, content)
	UploadFile(f, srv.URL)

	if body != content {
		t.Errorf("expected body %q, got %q", content, body)
	}
}

func TestUploadFileReturnsStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	f := writeTempFile(t, "data")
	status, err := UploadFile(f, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", status)
	}
}

func TestUploadFileReturns200OnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := writeTempFile(t, "data")
	status, err := UploadFile(f, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("expected status 200, got %d", status)
	}
}

// pushServer builds an httptest.Server that handles the three legs of the push
// command: creating the upload, uploading the file, and finalizing the upload.
// The create response's uploadUrl is patched to point at the test server.
type pushServer struct {
	createStatus   int
	createBody     func(uploadURL string) string
	uploadStatus   int
	finalizeStatus int
	finalizeBody   string
}

func (ps pushServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/uploads":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(ps.createStatus)
			fmt.Fprint(w, ps.createBody(srv.URL+"/upload"))
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			w.WriteHeader(ps.uploadStatus)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/uploads/finalize":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(ps.finalizeStatus)
			fmt.Fprint(w, ps.finalizeBody)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func createOKBody(uploadID, uploadURL string) string {
	b, _ := json.Marshal(map[string]any{
		"success": true,
		"result":  map[string]string{"uploadId": uploadID, "uploadUrl": uploadURL},
	})
	return string(b)
}

func finalizeOKBody() string {
	b, _ := json.Marshal(map[string]any{
		"success":      true,
		"appId":        "app-1",
		"bundleId":     "com.example.app",
		"versionLabel": "1.0",
		"versionName":  "1.0.0",
	})
	return string(b)
}

func errorBody(msg string) string {
	b, _ := json.Marshal(map[string]any{
		"success": false,
		"errors":  []map[string]any{{"code": 0, "message": msg}},
	})
	return string(b)
}

// makeAppBundle creates a minimal .app directory with Info.plist in t.TempDir.
func makeAppBundle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "App.app")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Info.plist"), []byte("<plist/>"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// makeIPAFile creates a minimal valid .ipa (a zip archive containing a
// Payload/Demo.app/ entry) at a path inside t.TempDir.
func makeIPAFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "App.ipa")
	writeIPA(t, path, func(zw *zip.Writer) {
		if _, err := zw.Create("Payload/Demo.app/"); err != nil {
			t.Fatal(err)
		}
		w, err := zw.Create("Payload/Demo.app/Info.plist")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("<plist/>")); err != nil {
			t.Fatal(err)
		}
	})
	return path
}

// writeIPA writes a zip archive at path, calling fn to populate its entries.
func writeIPA(t *testing.T, path string, fn func(*zip.Writer)) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	fn(zw)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateArtifactValidApp(t *testing.T) {
	path := makeAppBundle(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateArtifact(path, info); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestValidateArtifactValidIPA(t *testing.T) {
	path := makeIPAFile(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateArtifact(path, info); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestValidateArtifactAppAsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "App.app")
	if err := os.WriteFile(path, []byte("not a bundle"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	err = validateArtifact(path, info)
	if err == nil || !strings.Contains(err.Error(), "expected a directory") {
		t.Errorf("expected 'expected a directory' error, got %v", err)
	}
}

func TestValidateArtifactIPAAsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "App.ipa")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	err = validateArtifact(path, info)
	if err == nil || !strings.Contains(err.Error(), "expected a file") {
		t.Errorf("expected 'expected a file' error, got %v", err)
	}
}

func TestValidateArtifactIPANotZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "App.ipa")
	if err := os.WriteFile(path, []byte("not a zip"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	err = validateArtifact(path, info)
	if err == nil || !strings.Contains(err.Error(), "not a zip archive") {
		t.Errorf("expected 'not a zip archive' error, got %v", err)
	}
}

func TestValidateArtifactIPAMissingPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "App.ipa")
	writeIPA(t, path, func(zw *zip.Writer) {
		w, err := zw.Create("README.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
	})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	err = validateArtifact(path, info)
	if err == nil || !strings.Contains(err.Error(), "Payload/ not found") {
		t.Errorf("expected 'Payload/ not found' error, got %v", err)
	}
}

func TestValidateArtifactUnsupportedExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "App.zip")
	if err := os.WriteFile(path, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	err = validateArtifact(path, info)
	if err == nil || !strings.Contains(err.Error(), "expected .app or .ipa") {
		t.Errorf("expected 'expected .app or .ipa' error, got %v", err)
	}
}

func TestPushReturnsUploadID(t *testing.T) {
	const uploadID = "abc-123"
	srv := pushServer{
		createStatus:   http.StatusOK,
		createBody:     func(u string) string { return createOKBody(uploadID, u) },
		uploadStatus:   http.StatusOK,
		finalizeStatus: http.StatusOK,
		finalizeBody:   finalizeOKBody(),
	}.start(t)

	result, err := Push(context.Background(), "key", srv.URL, makeAppBundle(t), PushOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.UploadID != uploadID {
		t.Errorf("expected UploadID %q, got %q", uploadID, result.UploadID)
	}
}

func TestPushReturnsUploadID_IPA(t *testing.T) {
	const uploadID = "ipa-123"

	var uploaded bytes.Buffer
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/uploads":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, createOKBody(uploadID, srv.URL+"/upload"))
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			io.Copy(&uploaded, r.Body)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/uploads/finalize":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, finalizeOKBody())
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	ipaPath := makeIPAFile(t)
	expected, err := os.ReadFile(ipaPath)
	if err != nil {
		t.Fatal(err)
	}

	result, err := Push(context.Background(), "key", srv.URL, ipaPath, PushOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.UploadID != uploadID {
		t.Errorf("expected UploadID %q, got %q", uploadID, result.UploadID)
	}
	if !bytes.Equal(uploaded.Bytes(), expected) {
		t.Errorf("expected uploaded body to equal raw .ipa bytes (%d bytes), got %d bytes", len(expected), uploaded.Len())
	}
}

func TestPushCreateUploadUnauthorized(t *testing.T) {
	srv := pushServer{
		createStatus: http.StatusUnauthorized,
		createBody:   func(string) string { return errorBody("unauthorized") },
	}.start(t)

	_, err := Push(context.Background(), "bad-key", srv.URL, makeAppBundle(t), PushOptions{})
	if !errors.Is(err, client.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestPushCreateUploadForbidden(t *testing.T) {
	srv := pushServer{
		createStatus: http.StatusForbidden,
		createBody:   func(string) string { return errorBody("forbidden") },
	}.start(t)

	_, err := Push(context.Background(), "key", srv.URL, makeAppBundle(t), PushOptions{})
	if !errors.Is(err, client.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestPushUploadFileFails(t *testing.T) {
	srv := pushServer{
		createStatus:   http.StatusOK,
		createBody:     func(u string) string { return createOKBody("id-1", u) },
		uploadStatus:   http.StatusInternalServerError,
		finalizeStatus: http.StatusOK,
		finalizeBody:   finalizeOKBody(),
	}.start(t)

	_, err := Push(context.Background(), "key", srv.URL, makeAppBundle(t), PushOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestPushFinalizeUnauthorized(t *testing.T) {
	srv := pushServer{
		createStatus:   http.StatusOK,
		createBody:     func(u string) string { return createOKBody("id-1", u) },
		uploadStatus:   http.StatusOK,
		finalizeStatus: http.StatusUnauthorized,
		finalizeBody:   errorBody("unauthorized"),
	}.start(t)

	_, err := Push(context.Background(), "key", srv.URL, makeAppBundle(t), PushOptions{})
	if !errors.Is(err, client.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestPushFinalizeForbidden(t *testing.T) {
	srv := pushServer{
		createStatus:   http.StatusOK,
		createBody:     func(u string) string { return createOKBody("id-1", u) },
		uploadStatus:   http.StatusOK,
		finalizeStatus: http.StatusForbidden,
		finalizeBody:   errorBody("forbidden"),
	}.start(t)

	_, err := Push(context.Background(), "key", srv.URL, makeAppBundle(t), PushOptions{})
	if !errors.Is(err, client.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestPushFinalizeFailure(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"success": false, "message": "processing error"})
	srv := pushServer{
		createStatus:   http.StatusOK,
		createBody:     func(u string) string { return createOKBody("id-1", u) },
		uploadStatus:   http.StatusOK,
		finalizeStatus: http.StatusUnprocessableEntity,
		finalizeBody:   string(body),
	}.start(t)

	_, err := Push(context.Background(), "key", srv.URL, makeAppBundle(t), PushOptions{})
	if err == nil || !strings.Contains(err.Error(), "processing error") {
		t.Errorf("expected 'processing error' in error, got %v", err)
	}
}

func TestPushFinalizeNotFound(t *testing.T) {
	srv := pushServer{
		createStatus:   http.StatusOK,
		createBody:     func(u string) string { return createOKBody("id-1", u) },
		uploadStatus:   http.StatusOK,
		finalizeStatus: http.StatusNotFound,
		finalizeBody:   errorBody("not found"),
	}.start(t)

	_, err := Push(context.Background(), "key", srv.URL, makeAppBundle(t), PushOptions{})
	if err == nil || !strings.Contains(err.Error(), "upload not found") {
		t.Errorf("expected 'upload not found' error, got %v", err)
	}
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "upload-*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := io.Copy(f, strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}
