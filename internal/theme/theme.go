// Package theme centralizes every visual choice used by the TUI.
package theme

import (
	"image/color"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

type Theme struct {
	Border   lipgloss.Style
	Header   lipgloss.Style
	Selected lipgloss.Style
	Success  lipgloss.Style
	Danger   lipgloss.Style
	Warning  lipgloss.Style
	Muted    lipgloss.Style
	HelpBar  lipgloss.Style
	Input    lipgloss.Style
	Plain    bool
	Profile  colorprofile.Profile
}

func New(out io.Writer, env []string, noColor, dark bool) Theme {
	profile := colorprofile.Detect(out, env)
	termDumb := false
	envNoColor := false
	for _, value := range env {
		termDumb = termDumb || strings.EqualFold(value, "TERM=dumb")
		envNoColor = envNoColor || strings.HasPrefix(value, "NO_COLOR=")
	}
	plain := noColor || envNoColor || termDumb || profile <= colorprofile.ASCII
	if plain {
		return Theme{Plain: true, Profile: profile}
	}

	// Lip Gloss v2 replaced the old AdaptiveColor value with LightDark plus an
	// explicit output color profile. Keeping this selection here gives the same
	// light/dark semantics while colorprofile.Convert provides the required
	// TrueColor -> ANSI256 -> ANSI degradation.
	adaptive := func(light, darkValue color.Color) color.Color {
		return profile.Convert(lipgloss.LightDark(dark)(light, darkValue))
	}
	accent := adaptive(lipgloss.Color("#0060D0"), lipgloss.Color("#4FC1FF"))
	muted := adaptive(lipgloss.Color("#6B6B6B"), lipgloss.Color("#9A9A9A"))
	border := adaptive(lipgloss.Color("#8A8A8A"), lipgloss.Color("#626262"))
	// Status roles stay fixed (green/red/yellow); only their tone adapts so the
	// semantic meaning survives both light and dark backgrounds.
	success := adaptive(lipgloss.Color("#0A7D2C"), lipgloss.Color("#3ECF5E"))
	danger := adaptive(lipgloss.Color("#B00020"), lipgloss.Color("#FF6B6B"))
	warning := adaptive(lipgloss.Color("#8A6D00"), lipgloss.Color("#E5C100"))

	return Theme{
		Border:   lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border),
		Header:   lipgloss.NewStyle().Bold(true).Foreground(accent),
		Selected: lipgloss.NewStyle().Bold(true).Foreground(accent).Reverse(true),
		Success:  lipgloss.NewStyle().Foreground(success),
		Danger:   lipgloss.NewStyle().Foreground(danger),
		Warning:  lipgloss.NewStyle().Foreground(warning),
		Muted:    lipgloss.NewStyle().Foreground(muted),
		HelpBar:  lipgloss.NewStyle().Foreground(muted),
		Input:    lipgloss.NewStyle().Foreground(accent),
		Profile:  profile,
	}
}

func NewTheme() Theme {
	return New(os.Stdout, os.Environ(), false, true)
}
