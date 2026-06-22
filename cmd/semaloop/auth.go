package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/semaloop/cli/internal/config"
	"golang.org/x/term"
)

type AuthCmd struct {
	Login  AuthLoginCmd  `cmd:"" help:"Save an API key to authenticate with Semaloop."`
	Logout AuthLogoutCmd `cmd:"" help:"Remove stored credentials."`
	Status AuthStatusCmd `cmd:"" help:"Show the current authentication status."`
}

// AuthLoginCmd stores an API key on disk.
type AuthLoginCmd struct {
	APIKey string `help:"API key to store. Reads from stdin if omitted." name:"api-key"`
}

func (c *AuthLoginCmd) Run() error {
	key := c.APIKey
	if key == "" {
		if !term.IsTerminal(int(syscall.Stdin)) {
			return fmt.Errorf("no terminal available to prompt for an API key, use --api-key or the SEMALOOP_API_KEY environment variable")
		}
		fmt.Fprint(os.Stderr, "Enter your Semaloop API key: ")
		raw, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("reading API key: %w", err)
		}
		key = strings.TrimSpace(string(raw))
	}
	if key == "" {
		return fmt.Errorf("API key must not be empty")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.APIKey = key
	if err := config.Save(cfg); err != nil {
		return err
	}

	path, _ := config.Path()
	log.Info("API key saved.", "path", path)
	return nil
}

// AuthLogoutCmd removes stored credentials.
type AuthLogoutCmd struct{}

func (c *AuthLogoutCmd) Run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.APIKey == "" {
		log.Warn("No stored credentials found.")
		return nil
	}
	cfg.APIKey = ""
	if err := config.Save(cfg); err != nil {
		return err
	}
	log.Info("Logged out. Stored API key removed.")
	return nil
}

// AuthStatusCmd reports the current authentication state.
type AuthStatusCmd struct{}

func (c *AuthStatusCmd) Run() error {
	if key := os.Getenv("SEMALOOP_API_KEY"); key != "" {
		log.Info("Authenticated via SEMALOOP_API_KEY environment variable.")
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.APIKey != "" {
		path, _ := config.Path()
		log.Info("Authenticated via stored credentials.", "path", path)
		return nil
	}

	log.Error("Not authenticated. Run `semaloop auth login` to get started.")
	os.Exit(1)
	return nil
}
