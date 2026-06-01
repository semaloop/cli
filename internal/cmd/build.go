package cmd

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/semaloop/cli/internal/api"
	"github.com/semaloop/cli/internal/client"
)

// PushResult holds the outcome of a successful Push.
type PushResult struct {
	UploadID string
}

// PushOptions configures optional behavior for Push.
type PushOptions struct {
	// DryRun performs all local input validation and packaging but skips every
	// network call (create upload, PUT file, finalize).
	DryRun bool
}

// Push creates a build upload and streams the file to the returned URL.
// If filePath is a directory it is zipped into a temporary file first.
func Push(ctx context.Context, apiKey, serverURL, filePath string, opts PushOptions) (PushResult, error) {
	filePath = filepath.Clean(filePath)
	info, err := os.Stat(filePath)
	if err != nil {
		return PushResult{}, fmt.Errorf("could not read path: %w", err)
	}

	log.Debug("Validating artifact.", "path", filePath)
	if err := validateArtifact(filePath, info); err != nil {
		return PushResult{}, err
	}

	log.Debug("Artifact is valid.")

	uploadPath := filePath
	if info.IsDir() {
		log.Info("Packaging app bundle.", "path", filePath)
		tmp, err := zipDir(filePath)
		if err != nil {
			return PushResult{}, fmt.Errorf("could not package directory: %w", err)
		}
		defer os.Remove(tmp)
		uploadPath = tmp

		log.Debug("App bundle packaged.", "tmp", tmp)
	}

	if opts.DryRun {
		log.Info("Dry run: artifact validated; skipping upload.", "path", filePath)
		return PushResult{}, nil
	}

	c, err := api.NewClient(serverURL, api.WithClient(&client.AuthClient{APIKey: apiKey}))
	if err != nil {
		return PushResult{}, fmt.Errorf("could not initialise API client: %w", err)
	}

	log.Info("Beginning upload.")

	res, err := c.PostCreateUpload(ctx)
	if err != nil {
		return PushResult{}, fmt.Errorf("could not connect to Semaloop: %w", err)
	}

	createRes, ok := res.(*api.PostCreateUploadOK)
	if !ok {
		switch res.(type) {
		case *api.PostCreateUploadUnauthorized:
			return PushResult{}, client.ErrUnauthorized
		case *api.PostCreateUploadForbidden:
			return PushResult{}, client.ErrForbidden
		default:
			return PushResult{}, fmt.Errorf("unexpected response from server (%T)", res)
		}
	}

	status, err := UploadFile(uploadPath, createRes.Result.UploadUrl)
	if err != nil {
		return PushResult{}, err
	}
	if status != http.StatusOK {
		return PushResult{}, fmt.Errorf("upload failed, please try again (status %d)", status)
	}

	log.Debug("Finalizing upload.", "upload_id", createRes.Result.UploadId)

	finalizeRes, err := c.PostFinalizeUpload(ctx, api.NewOptPostFinalizeUploadReq(api.PostFinalizeUploadReq{
		ID: createRes.Result.UploadId,
	}))
	if err != nil {
		return PushResult{}, fmt.Errorf("could not finalize upload: %w", err)
	}

	switch r := finalizeRes.(type) {
	case *api.FinalizeUploadSuccessResponse:
		return PushResult{UploadID: createRes.Result.UploadId}, nil
	case *api.FinalizeUploadFailureResponse:
		logger := log.With()

		if r.BundleId.Set {
			logger = logger.With("bundle_id", r.BundleId.Value)
		}
		if r.VersionLabel.Set {
			logger = logger.With("version_label", r.VersionLabel.Value)
		}
		if r.VersionName.Set {
			logger = logger.With("version_name", r.VersionName.Value)
		}

		logger.Errorf("Finalizing upload failed: %s.", r.Message)

		return PushResult{}, fmt.Errorf("finalize failed: %s", r.Message)
	case *api.PostFinalizeUploadUnauthorized:
		return PushResult{}, client.ErrUnauthorized
	case *api.PostFinalizeUploadForbidden:
		return PushResult{}, client.ErrForbidden
	case *api.PostFinalizeUploadNotFound:
		return PushResult{}, fmt.Errorf("upload not found")
	default:
		return PushResult{}, fmt.Errorf("unexpected response from server (%T)", finalizeRes)
	}
}

// UploadFile streams the file at path to the given pre-signed URL via PUT.
// Returns the HTTP status code and any error.
func UploadFile(path, uploadURL string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("could not open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("could not read file: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, uploadURL, f)
	if err != nil {
		return 0, fmt.Errorf("could not prepare upload: %w", err)
	}
	req.ContentLength = info.Size()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	log.Debug("Recieved HTTP response.", "url", uploadURL, "status", resp.StatusCode)
	return resp.StatusCode, nil
}

// validateArtifact checks that path is a .app bundle or .ipa file.
func validateArtifact(path string, info os.FileInfo) error {
	ext := filepath.Ext(path)
	switch ext {
	case ".app":
		if !info.IsDir() {
			return fmt.Errorf("%q is not a valid .app bundle (expected a directory)", path)
		}
		if _, err := os.Stat(filepath.Join(path, "Info.plist")); err != nil {
			return fmt.Errorf("%q does not appear to be a valid .app bundle (Info.plist not found)", path)
		}
	case ".ipa":
		if info.IsDir() {
			return fmt.Errorf("%q is not a valid .ipa file (expected a file, not a directory)", path)
		}
		if err := validateIPA(path); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%q is not a supported iOS artifact (expected .app or .ipa)", path)
	}
	return nil
}

// validateIPA verifies that path is a zip archive containing a Payload/ entry,
// which is the canonical structural marker of an iOS .ipa archive.
func validateIPA(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("%q does not appear to be a valid .ipa file (not a zip archive): %w", path, err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Name == "Payload/" || strings.HasPrefix(f.Name, "Payload/") {
			return nil
		}
	}
	return fmt.Errorf("%q does not appear to be a valid .ipa file (Payload/ not found)", path)
}

// zipDir creates a temporary zip archive of the directory at src and returns its path.
func zipDir(src string) (string, error) {
	tmp, err := os.CreateTemp("", "semaloop-upload-*.zip")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	zw := zip.NewWriter(tmp)
	defer zw.Close()

	base := filepath.Dir(src)
	err = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			_, err = zw.Create(rel + "/")
			return err
		}
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	return tmp.Name(), nil
}
