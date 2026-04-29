package main

import (
	"context"
	"errors"

	"github.com/semaloop/cli/internal/client"
	icmd "github.com/semaloop/cli/internal/cmd"
	"github.com/semaloop/cli/internal/config"
)

type BuildCmd struct {
	Push BuildPushCmd `cmd:"" help:"Upload a build artifact."`
}

// BuildPushCmd uploads a build artifact to Semaloop.
type BuildPushCmd struct {
	File string `arg:"" help:"Path to the build artifact to upload." type:"path"`
}

func (c *BuildPushCmd) Run() error {
	apiKey, err := config.APIKey()
	if err != nil {
		log.Error("Could not load configuration.", "err", err)
		return nil
	}
	if apiKey == "" {
		log.Error("Not authenticated. Run `semaloop auth login` first.")
		return nil
	}

	result, err := icmd.Push(context.Background(), apiKey, client.ServerURL(), c.File)
	if err != nil {
		switch {
		case errors.Is(err, client.ErrUnauthorized):
			log.Error("Invalid API key. Run `semaloop auth login` to update it.")
		case errors.Is(err, client.ErrForbidden):
			log.Error("Your account does not have permission to upload builds.")
		default:
			log.Error("Build push failed.", "err", err)
		}
		return nil
	}

	log.Info("Build uploaded successfully.", "id", result.UploadID)
	return nil
}
