package main

import (
	"fmt"
	"strings"

	"github.com/alecthomas/kong"
)

// CompletionCmd prints a shell completion script.
//
// The scripts don't contain any completion data themselves. When the user
// presses Tab, the shell runs `semaloop` with COMP_LINE set, and
// kongcompletion.Register (called in main) answers from the Kong model. The
// scripts call `semaloop` by name rather than by absolute path so they keep
// working after the binary moves, e.g. across `brew upgrade`.
type CompletionCmd struct {
	Shell string `arg:"" help:"Shell to print a completion script for (${enum})." enum:"bash,zsh,fish"`
}

func (c *CompletionCmd) Help() string {
	return `Print a completion script for your shell to stdout.

To enable completions for the current session:

  bash:  source <(semaloop completion bash)
  zsh:   source <(semaloop completion zsh)
  fish:  semaloop completion fish | source

To enable them permanently, add that line to ~/.bashrc, ~/.zshrc or
~/.config/fish/config.fish. Homebrew installs completions automatically.`
}

func (c *CompletionCmd) Run(ctx *kong.Context) error {
	script, ok := completionScripts[c.Shell]
	if !ok {
		return fmt.Errorf("unsupported shell %q", c.Shell)
	}
	_, err := fmt.Fprint(ctx.Stdout, strings.ReplaceAll(script, "{{bin}}", ctx.Model.Name))
	return err
}

var completionScripts = map[string]string{
	// -o default falls back to filename completion, e.g. for `build push <file>`.
	"bash": `complete -o default -o bashdefault -C {{bin}} {{bin}}
`,

	// Works both when sourced and when autoloaded from $fpath as _{{bin}}
	// (which is how Homebrew installs it).
	"zsh": `#compdef {{bin}}

_{{bin}}() {
  local line="${(j: :)words[1,CURRENT]}"
  local -a candidates
  candidates=("${(@f)$(COMP_LINE="$line" COMP_POINT=${#line} {{bin}} 2>/dev/null)}")
  candidates=(${candidates:#})
  if (( ${#candidates} )); then
    compadd -a candidates
  else
    _files
  fi
}

if [[ "${zsh_eval_context[-1]}" == loadautofunc ]]; then
  _{{bin}} "$@"
else
  compdef _{{bin}} {{bin}}
fi
`,

	"fish": `function __complete_{{bin}}
    set -lx COMP_LINE (commandline -cp)
    test -z (commandline -ct)
    and set COMP_LINE "$COMP_LINE "
    {{bin}}
end
complete -c {{bin}} -f -a "(__complete_{{bin}})"
`,
}
