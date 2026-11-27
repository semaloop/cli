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

// style renders s with the style that with makes, or leaves it as it is
// without colours.
func (t Theme) style(s string, with func(lipgloss.Style) lipgloss.Style) string {
	if t.r == nil || s == "" {
		return s
	}
	return with(t.r.NewStyle()).Render(s)
}

func foreground(color string) func(lipgloss.Style) lipgloss.Style {
	return func(st lipgloss.Style) lipgloss.Style { return st.Foreground(lipgloss.Color(color)) }
}

// The terminal's own basic colours, so the output suits its theme.
func (t Theme) green(s string) string   { return t.style(s, foreground("2")) }
func (t Theme) yellow(s string) string  { return t.style(s, foreground("3")) }
func (t Theme) red(s string) string     { return t.style(s, foreground("1")) }
func (t Theme) magenta(s string) string { return t.style(s, foreground("5")) }

func (t Theme) bold(s string) string {
	return t.style(s, func(st lipgloss.Style) lipgloss.Style { return st.Bold(true) })
}

func (t Theme) dim(s string) string {
	return t.style(s, func(st lipgloss.Style) lipgloss.Style { return st.Faint(true) })
}

// removed and added mark the words that changed within a line. Without
// colours, they're marked like `git diff --word-diff`: [-removed-]{+added+}.
func (t Theme) removed(s string) string {
	if t.r == nil {
		return "[-" + s + "-]"
	}
	return t.style(s, func(st lipgloss.Style) lipgloss.Style { return foreground("1")(st).Reverse(true) })
}

func (t Theme) added(s string) string {
	if t.r == nil {
		return "{+" + s + "+}"
	}
	return t.style(s, func(st lipgloss.Style) lipgloss.Style { return foreground("2")(st).Reverse(true) })
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
