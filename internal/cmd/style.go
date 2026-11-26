package cmd

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Theme styles text output. Its zero value styles nothing, which is what
// output without colours gets.
type Theme struct {
	r *lipgloss.Renderer
}

// NewTheme styles text for r, in colour if r's color profile has any.
func NewTheme(r *lipgloss.Renderer) Theme {
	if r.ColorProfile() == termenv.Ascii {
		return Theme{}
	}
	return Theme{r}
}

func (t Theme) fg(color string, s string) string {
	if t.r == nil || s == "" {
		return s
	}
	return t.r.NewStyle().Foreground(lipgloss.Color(color)).Render(s)
}

// The terminal's own basic colours, so the output suits its theme.
func (t Theme) green(s string) string   { return t.fg("2", s) }
func (t Theme) yellow(s string) string  { return t.fg("3", s) }
func (t Theme) red(s string) string     { return t.fg("1", s) }
func (t Theme) magenta(s string) string { return t.fg("5", s) }

func (t Theme) bold(s string) string {
	if t.r == nil || s == "" {
		return s
	}
	return t.r.NewStyle().Bold(true).Render(s)
}

func (t Theme) dim(s string) string {
	if t.r == nil || s == "" {
		return s
	}
	return t.r.NewStyle().Faint(true).Render(s)
}

// removed and added mark the words that changed within a line. Without
// colours, they're marked like `git diff --word-diff`: [-removed-]{+added+}.
func (t Theme) removed(s string) string {
	if t.r == nil {
		return "[-" + s + "-]"
	}
	return t.r.NewStyle().Foreground(lipgloss.Color("1")).Reverse(true).Render(s)
}

func (t Theme) added(s string) string {
	if t.r == nil {
		return "{+" + s + "+}"
	}
	return t.r.NewStyle().Foreground(lipgloss.Color("2")).Reverse(true).Render(s)
}

// action colours text in the colour of action's symbol.
func (t Theme) action(action syncAction, s string) string {
	switch lookupAction(action).symbol {
	case "+":
		return t.green(s)
	case "~":
		return t.yellow(s)
	case "-":
		return t.red(s)
	case "?":
		return t.magenta(s)
	default:
		return s
	}
}
