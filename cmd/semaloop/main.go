package main

import (
	"io"
	"os"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/lipgloss"
	charmlog "github.com/charmbracelet/log"
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

type CLI struct {
	Version kong.VersionFlag `short:"v" help:"Print version."`
	Debug   bool             `help:"Enable debug logging."`
	JSON    bool             `help:"Output logs as JSON." name:"json"`
	Quiet   bool             `short:"q" help:"Suppress all log output; rely on exit code for status."`
	Auth    AuthCmd          `cmd:"" help:"Manage authentication."`
	Build   BuildCmd         `cmd:"" help:"Manage builds."`
}

func main() {
	cli := CLI{}
	ctx := kong.Parse(&cli, kong.Vars{"version": version.Version})
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
	if err := ctx.Run(); err != nil {
		if !cli.Quiet {
			ctx.FatalIfErrorf(err)
		}
		os.Exit(1)
	}
}
