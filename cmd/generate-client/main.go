// generate-client preprocesses an OpenAPI spec and generates a Go HTTP client
// using ogen. It normalises OpenAPI 3.1 nullable type arrays (e.g.
// ["string","null"]) into the ogen-compatible form (type + nullable:true)
// before generation.
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed ogen.yml
var ogenConfig []byte

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

const (
	target = "./internal/api"
	pkg    = "api"
)

func run(args []string) error {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: generate-client <spec>")
		os.Exit(1)
	}
	spec := args[0]

	raw, err := os.ReadFile(spec)
	if err != nil {
		return fmt.Errorf("reading spec: %w", err)
	}

	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("parsing spec: %w", err)
	}

	normalise(parsed)

	out, err := json.Marshal(parsed)
	if err != nil {
		return fmt.Errorf("marshalling spec: %w", err)
	}

	tmp, err := os.CreateTemp("", "openapi-*.json")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(out); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	finalArgs := []string{"tool", "ogen",
		"-target", target,
		"-package", pkg,
		"-clean",
	}
	cfgFile, err := os.CreateTemp("", "ogen-config-*.yml")
	if err != nil {
		return fmt.Errorf("creating ogen config: %w", err)
	}
	defer os.Remove(cfgFile.Name())
	if _, err := cfgFile.Write(ogenConfig); err != nil {
		return fmt.Errorf("writing ogen config: %w", err)
	}
	if err := cfgFile.Close(); err != nil {
		return fmt.Errorf("closing ogen config: %w", err)
	}
	finalArgs = append(finalArgs, "-config", filepath.Clean(cfgFile.Name()))
	finalArgs = append(finalArgs, tmp.Name())

	cmd := exec.Command("go", finalArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// normalise recursively rewrites {"type": ["string","null"]} to
// {"type": "string", "nullable": true} so that ogen can parse the spec.
func normalise(v any) {
	obj, ok := v.(map[string]any)
	if !ok {
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				normalise(item)
			}
		}
		return
	}

	if raw, ok := obj["type"]; ok {
		if types, ok := raw.([]any); ok {
			var nonNull []string
			hasNull := false
			for _, t := range types {
				if s, ok := t.(string); ok {
					if s == "null" {
						hasNull = true
					} else {
						nonNull = append(nonNull, s)
					}
				}
			}
			if len(nonNull) == 1 {
				obj["type"] = nonNull[0]
				if hasNull {
					obj["nullable"] = true
				}
			}
		}
	}

	for _, child := range obj {
		normalise(child)
	}
}
