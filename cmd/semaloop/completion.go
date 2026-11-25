package main

import (
	"fmt"

	"github.com/alecthomas/kong"
	"github.com/posener/complete"
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

// completionPredictors are referenced from struct tags with
// `completion-predictor:"<name>"`. Arguments without one get no file
// suggestions, so tag any argument that takes a path.
var completionPredictors = map[string]complete.Predictor{
	"file": complete.PredictFiles("*"),
	"dir":  complete.PredictDirs("*"),
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
	// The enum tag means Kong has already rejected any other shell.
	_, err := fmt.Fprint(ctx.Stdout, completionScripts[c.Shell])
	return err
}

// The scripts name the binary literally rather than using os.Args[0], which
// could contain characters that would need quoting, e.g. `semaloop (1)`.
var completionScripts = map[string]string{
	"bash": `complete -C semaloop semaloop
`,

	// Works both when sourced and when autoloaded from $fpath as _semaloop
	// (which is how Homebrew installs it). The line is rebuilt from $words
	// rather than taken from $BUFFER, which also holds any earlier commands
	// (e.g. "cd x && semaloop ..."), and is cut at the cursor via $PREFIX.
	"zsh": `#compdef semaloop

_semaloop() {
  local line="${(j: :)words[1,CURRENT-1]} ${PREFIX}"
  local -a candidates
  candidates=("${(@f)$(COMP_LINE="$line" COMP_POINT=${#line} semaloop 2>/dev/null)}")
  candidates=(${candidates:#})
  compadd -a candidates
}

if [[ "${zsh_eval_context[-1]}" == loadautofunc ]]; then
  _semaloop "$@"
else
  compdef _semaloop semaloop
fi
`,

	"fish": `function __complete_semaloop
    set -lx COMP_LINE (commandline -cp)
    test -z (commandline -ct)
    and set COMP_LINE "$COMP_LINE "
    semaloop
end
complete -c semaloop -f -a "(__complete_semaloop)"
`,
}
