package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load()
	if err != nil || cfg.APIKey != "" {
		t.Fatalf("expected empty config, got cfg=%+v err=%v", cfg, err)
	}
}

func TestSaveAndLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save(&Config{APIKey: "test-key"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil || cfg.APIKey != "test-key" {
		t.Fatalf("expected test-key, got cfg=%+v err=%v", cfg, err)
	}
}

func TestSaveFilePermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save(&Config{APIKey: "test-key"}); err != nil {
		t.Fatal(err)
	}
	path, _ := Path()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600, got %o", info.Mode().Perm())
	}
}

func TestAPIKeyEnvVarTakesPriority(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := Save(&Config{APIKey: "stored-key"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEMALOOP_API_KEY", "env-key")
	key, err := APIKey()
	if err != nil || key != "env-key" {
		t.Fatalf("expected env-key, got key=%q err=%v", key, err)
	}
}

func TestSaveDirPermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save(&Config{APIKey: "x"}); err != nil {
		t.Fatal(err)
	}
	path, _ := Path()
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("expected 0700, got %o", info.Mode().Perm())
	}
}
