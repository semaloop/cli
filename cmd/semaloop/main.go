package main

import (
	"io"
	"os"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/lipgloss"
	charmlog "github.com/charmbracelet/log"
	kongcompletion "github.com/jotaen/kong-completion"
	"github.com/semaloop/cli/internal/version"
)

var log = initLogger()

func initLogger() *charmlog.Logger {
	l := charmlog.Default()
	styles := charmlog.DefaultStyles()
	styles.Timestamp = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	l.SetStyles(styles)
	return l
}

type Globals struct {
	Debug  bool `help:"Enable debug logging."`
	JSON   bool `help:"Output logs as JSON." name:"json"`
	Quiet  bool `short:"q" help:"Suppress all log output; rely on exit code for status."`
	DryRun bool `help:"Validate inputs but skip all network calls." name:"dry-run"`
}

type CLI struct {
	Globals
	Version kong.VersionFlag `short:"v" help:"Print version."`
	Auth    AuthCmd          `cmd:"" help:"Manage authentication."`
	Build   BuildCmd         `cmd:"" help:"Manage builds."`

	Completion CompletionCmd `cmd:"" help:"Print a shell completion script."`
}

func main() {
	cli := CLI{}
	parser := kong.Must(&cli, kong.Vars{"version": version.Version}, kong.Bind(&cli.Globals))

	// Answer shell completion requests (Tab presses) and exit before anything
	// else runs, so nothing but the completions reaches the terminal.
	kongcompletion.Register(parser, kongcompletion.WithPredictors(completionPredictors))

	ctx, err := parser.Parse(os.Args[1:])
	parser.FatalIfErrorf(err)

	if cli.Debug {
		log.SetLevel(charmlog.DebugLevel)
	}
	if cli.JSON {
		log.SetFormatter(charmlog.JSONFormatter)
		log.SetReportTimestamp(true)
	}
	if cli.Quiet {
		log.SetOutput(io.Discard)
	}
	// Skip the banner for `completion`, whose output is sourced by the shell
	// (and run by Homebrew at install time).
	if _, ok := ctx.Selected().Target.Addr().Interface().(*CompletionCmd); !ok {
		log.Info("Running Semaloop CLI", "version", version.Version)
	}

	if err := ctx.Run(); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}
