package render

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"usbi"
)

// renderOverview prints the neofetch-style overview with banner, port grid,
// and Type-C summary.
func renderOverview(w io.Writer, devs []usbi.USBDevice, meta Meta, theme *Theme) error {
	parts := []string{renderUSBIHeader(theme, meta)}

	if len(devs) == 0 {
		parts = append(parts, "", theme.Muted.Render("(no devices)"))
		_, err := fmt.Fprintln(w, strings.Join(parts, "\n"))
		return err
	}

	// Port grid.
	grid := renderPortGrid(devs, theme)
	parts = append(parts, "", theme.Heading.Render("Ports"), grid)

	// Type-C summary block.
	if tc := renderTypeCSummary(devs, theme); tc != "" {
		parts = append(parts, "", theme.Heading.Render("Type-C"), tc)
	}

	_, err := fmt.Fprintln(w, lipgloss.JoinVertical(lipgloss.Left, parts...))
	return err
}

// renderPortGrid returns the compact glyph + location list.
func renderPortGrid(devs []usbi.USBDevice, theme *Theme) string {
	// Determine a fixed glyph column width from the widest inline glyph.
	maxGlyphWidth := 0
	for _, d := range devs {
		w := lipgloss.Width(InlineGlyph(usbTier(d)))
		if w > maxGlyphWidth {
			maxGlyphWidth = w
		}
	}
	if maxGlyphWidth < 6 {
		maxGlyphWidth = 6
	}

	// 1-based row index so users know what to pass to `--port <n>`.
	// Width is the number of digits in the largest index.
	idxWidth := len(fmt.Sprintf("%d", len(devs)))
	if idxWidth < 2 {
		idxWidth = 2
	}
	idxFmt := fmt.Sprintf("%%%dd", idxWidth)

	rows := make([]string, 0, len(devs))
	for i, d := range devs {
		tier := usbTier(d)
		glyph := theme.TierStyle(tier).Render(InlineGlyph(tier))
		glyph = lipgloss.Place(maxGlyphWidth, 1, lipgloss.Left, lipgloss.Center, glyph)

		idx := theme.Muted.Render(fmt.Sprintf(idxFmt, i+1))
		loc := theme.Value.Render(coalesce(d.LocationID, "-"))
		name := theme.Value.Render(trunc(coalesce(d.Name, "unnamed"), 38))
		speed := theme.Muted.Render(coalesce(d.Speed, "-"))

		extra := ""
		if d.PortConnectorType != "" {
			extra = theme.Muted.Render(d.PortConnectorType)
		}

		row := lipgloss.JoinHorizontal(lipgloss.Center,
			idx,
			"  ",
			glyph,
			"  ",
			loc,
			"  ",
			name,
			"  ",
			speed,
		)
		if extra != "" {
			row = lipgloss.JoinHorizontal(lipgloss.Center, row, "  ", extra)
		}
		rows = append(rows, row)
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// renderTypeCSummary returns a compact Type-C summary block or "" if none.
func renderTypeCSummary(devs []usbi.USBDevice, theme *Theme) string {
	var ports []usbi.USBDevice
	for _, d := range devs {
		if d.PortConnectorType == "Type-C" {
			ports = append(ports, d)
		}
	}
	if len(ports) == 0 {
		return ""
	}

	sort.SliceStable(ports, func(i, j int) bool {
		return ports[i].LocationID < ports[j].LocationID
	})

	lines := make([]string, 0, len(ports))
	for _, p := range ports {
		fields := []string{}
		if p.DataRole != "" {
			fields = append(fields, fmt.Sprintf("data=%s", p.DataRole))
		}
		if p.PowerRole != "" {
			fields = append(fields, fmt.Sprintf("power=%s", p.PowerRole))
		}
		if p.PowerOpMode != "" {
			fields = append(fields, fmt.Sprintf("mode=%s", p.PowerOpMode))
		}
		if p.CableType != "" {
			fields = append(fields, fmt.Sprintf("cable=%s", p.CableType))
		}
		if p.CableMaxSpeed != "" {
			fields = append(fields, fmt.Sprintf("speed=%s", p.CableMaxSpeed))
		}
		if p.CableCurrent != "" {
			fields = append(fields, fmt.Sprintf("current=%s", p.CableCurrent))
		}
		if len(p.Altmodes) > 0 {
			alts := make([]string, 0, len(p.Altmodes))
			for _, a := range p.Altmodes {
				if a.Description != "" {
					alts = append(alts, a.Description)
				} else {
					alts = append(alts, a.SVID)
				}
			}
			sort.Strings(alts)
			fields = append(fields, fmt.Sprintf("alt=%s", strings.Join(alts, ",")))
		}

		loc := theme.Value.Render(coalesce(p.LocationID, "-"))
		info := theme.Muted.Render(strings.Join(fields, " · "))
		if len(fields) == 0 {
			info = theme.Muted.Render("no partner/cable data")
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Center, loc, "  ", info))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}
