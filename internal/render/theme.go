package render

import (
	"image/color"
	"os"

	"charm.land/lipgloss/v2"
)

// Theme bundles all style decisions for one render pass.  It is built once
// from the detected terminal background and the --no-color flag.
type Theme struct {
	Dark        bool
	LightDark   lipgloss.LightDarkFunc
	Width       int
	Stdout      *os.File
	NoColor     bool
	IsTTY       bool
	Heading     lipgloss.Style
	Subheading  lipgloss.Style
	Label       lipgloss.Style
	Value       lipgloss.Style
	Muted       lipgloss.Style
	Error       lipgloss.Style
	Banner      lipgloss.Style
	InlineGlyph lipgloss.Style
}

// NewTheme detects the terminal background and width, then builds the style
// palette.  If noColor is true, all color styling is suppressed.
func NewTheme(noColor bool, stdout *os.File, forceWidth int) *Theme {
	// Skip the terminal background query when color is disabled or stdout is
	// not a TTY; default to a dark palette.
	dark := true
	if !noColor && isTerminal(stdout) {
		dark = lipgloss.HasDarkBackground(os.Stdin, stdout)
	}
	ld := lipgloss.LightDark(dark)

	width := forceWidth
	if width <= 0 {
		if stdout != nil {
			if w, _, err := termSize(stdout); err == nil && w > 0 {
				width = w
			}
		}
		if width <= 0 {
			width = 80
		}
	}

	isTTY := isTerminal(stdout)

	t := &Theme{
		Dark:      dark,
		LightDark: ld,
		Width:     width,
		Stdout:    stdout,
		NoColor:   noColor,
		IsTTY:     isTTY,
	}

	// Helper that applies a foreground color unless color is disabled.
	fg := func(c color.Color) color.Color {
		if noColor {
			return lipgloss.NoColor{}
		}
		return c
	}

	// Base styles.
	t.Heading = lipgloss.NewStyle().
		Foreground(fg(ld(lipgloss.Color("#1f2937"), lipgloss.Color("#f3f4f6")))).
		Bold(true)
	t.Subheading = lipgloss.NewStyle().
		Foreground(fg(ld(lipgloss.Color("#4b5563"), lipgloss.Color("#9ca3af")))).
		Bold(true)
	t.Label = lipgloss.NewStyle().
		Foreground(fg(ld(lipgloss.Color("#6b7280"), lipgloss.Color("#6b7280")))).
		Width(14)
	t.Value = lipgloss.NewStyle().
		Foreground(fg(ld(lipgloss.Color("#111827"), lipgloss.Color("#e5e7eb"))))
	t.Muted = lipgloss.NewStyle().
		Foreground(fg(ld(lipgloss.Color("#9ca3af"), lipgloss.Color("#4b5563"))))
	t.Error = lipgloss.NewStyle().
		Foreground(fg(ld(lipgloss.Color("#b91c1c"), lipgloss.Color("#f87171")))).
		Bold(true)
	t.Banner = lipgloss.NewStyle().
		Foreground(fg(ld(lipgloss.Color("#2563eb"), lipgloss.Color("#60a5fa")))).
		Bold(true)
	t.InlineGlyph = lipgloss.NewStyle().
		Foreground(fg(ld(lipgloss.Color("#6b7280"), lipgloss.Color("#9ca3af"))))

	return t
}

// TierColor returns a light/dark-aware color for a USB tier.
func (t *Theme) TierColor(tier Tier) color.Color {
	if t.NoColor {
		return lipgloss.NoColor{}
	}
	switch tier {
	case TierUSB1:
		return t.LightDark(lipgloss.Color("#9ca3af"), lipgloss.Color("#6b7280"))
	case TierUSB2:
		return t.LightDark(lipgloss.Color("#4b5563"), lipgloss.Color("#d1d5db"))
	case TierUSB3_0:
		return t.LightDark(lipgloss.Color("#2563eb"), lipgloss.Color("#60a5fa"))
	case TierUSB3_1:
		return t.LightDark(lipgloss.Color("#1d4ed8"), lipgloss.Color("#3b82f6"))
	case TierUSB3_2:
		return t.LightDark(lipgloss.Color("#7c3aed"), lipgloss.Color("#a78bfa"))
	case TierUSB4G2:
		return t.LightDark(lipgloss.Color("#0891b2"), lipgloss.Color("#22d3ee"))
	case TierUSB4G3:
		return t.LightDark(lipgloss.Color("#059669"), lipgloss.Color("#34d399"))
	case TierUSB4G4:
		return t.LightDark(lipgloss.Color("#b45309"), lipgloss.Color("#fbbf24"))
	case TierTBT3:
		return t.LightDark(lipgloss.Color("#c2410c"), lipgloss.Color("#fb923c"))
	case TierEmpty:
		return t.LightDark(lipgloss.Color("#d1d5db"), lipgloss.Color("#374151"))
	default:
		return t.LightDark(lipgloss.Color("#9ca3af"), lipgloss.Color("#6b7280"))
	}
}

// TierStyle returns a style with the tier's foreground color applied.
func (t *Theme) TierStyle(tier Tier) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.TierColor(tier)).Bold(true)
}

// ShouldRenderFancy reports whether the terminal is wide enough and TTY enough
// for the new neofetch renderer.  Otherwise callers should fall back to the
// plain table.
func (t *Theme) ShouldRenderFancy() bool {
	if !t.IsTTY {
		return false
	}
	if t.Width < 60 {
		return false
	}
	return true
}
