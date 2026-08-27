package render

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"usbi"
)

// renderCard prints one device in neofetch card form.
func renderCard(w io.Writer, dev usbi.USBDevice, meta Meta, theme *Theme) error {
	tier := usbTier(dev)
	glyph := theme.TierStyle(tier).Render(BigGlyph(tier))

	sections := []string{}
	sections = append(sections, renderSection("Device", deviceRows(dev, theme), theme))
	if r := connectorRows(dev, theme); len(r) > 0 {
		sections = append(sections, renderSection("Connector", r, theme))
	}
	if r := cableRows(dev, theme); len(r) > 0 {
		sections = append(sections, renderSection("Cable", r, theme))
	}
	if r := altmodeRows(dev, theme); len(r) > 0 {
		sections = append(sections, renderSection("Alt Modes", r, theme))
	}

	right := lipgloss.JoinVertical(lipgloss.Left, sections...)

	// Ensure the glyph is at least as tall as the content for alignment.
	gh := lipgloss.Height(glyph)
	rh := lipgloss.Height(right)
	if gh < rh {
		glyph = lipgloss.JoinVertical(lipgloss.Top, glyph, strings.Repeat("\n", rh-gh))
	}

	card := lipgloss.JoinHorizontal(lipgloss.Top, glyph, "  ", right)

	header := renderUSBIHeader(theme, meta)
	out := lipgloss.JoinVertical(lipgloss.Left, header, "", card)
	_, err := fmt.Fprintln(w, out)
	return err
}

// renderSection returns one titled block of key/value rows.
func renderSection(title string, rows []string, theme *Theme) string {
	if len(rows) == 0 {
		return ""
	}
	parts := []string{
		"",
		theme.Subheading.Render(title),
		strings.Join(rows, "\n"),
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func deviceRows(dev usbi.USBDevice, theme *Theme) []string {
	rows := []string{
		row("Name", coalesce(dev.Name, "unnamed"), theme),
		row("Location", coalesce(dev.LocationID, "-"), theme),
		row("Tier", TierLabel(usbTier(dev)), theme),
		row("VID", coalesce(dev.VendorID, "-"), theme),
		row("PID", coalesce(dev.ProductID, "-"), theme),
		row("Speed", coalesce(dev.Speed, "-"), theme),
	}
	if dev.NegotiatedCurrent > 0 {
		rows = append(rows, row("Power", fmt.Sprintf("%d mA", dev.NegotiatedCurrent), theme))
	}
	return rows
}

func connectorRows(dev usbi.USBDevice, theme *Theme) []string {
	rows := []string{}
	if dev.PortConnectorType != "" {
		rows = append(rows, row("Type", dev.PortConnectorType, theme))
	}
	if dev.DataRole != "" {
		rows = append(rows, row("Data role", dev.DataRole, theme))
	}
	if dev.PowerRole != "" {
		rows = append(rows, row("Power role", dev.PowerRole, theme))
	}
	if dev.PowerOpMode != "" {
		rows = append(rows, row("Power mode", dev.PowerOpMode, theme))
	}
	if dev.Orientation != "" {
		rows = append(rows, row("Orientation", dev.Orientation, theme))
	}
	if dev.PDRevision != "" {
		rows = append(rows, row("PD revision", dev.PDRevision, theme))
	}
	if dev.USBCapability != "" {
		rows = append(rows, row("USB capable", dev.USBCapability, theme))
	}
	return rows
}

func cableRows(dev usbi.USBDevice, theme *Theme) []string {
	rows := []string{}
	if dev.CablePresent {
		rows = append(rows, row("Present", "yes", theme))
	}
	if dev.CableType != "" {
		rows = append(rows, row("Type", dev.CableType, theme))
	}
	if dev.CablePlugType != "" {
		rows = append(rows, row("Plug", dev.CablePlugType, theme))
	}
	if dev.CableMaxSpeed != "" {
		rows = append(rows, row("Max speed", dev.CableMaxSpeed, theme))
	}
	if dev.CableCurrent != "" {
		rows = append(rows, row("Current", dev.CableCurrent, theme))
	}
	if dev.CableVBUSThrough != "" {
		rows = append(rows, row("VBUS", dev.CableVBUSThrough, theme))
	}
	if dev.CableUSB4 {
		rows = append(rows, row("USB4", "yes", theme))
	}
	if dev.CableUSB32 {
		rows = append(rows, row("USB 3.2", "yes", theme))
	}
	if dev.CableTBT3 {
		rows = append(rows, row("TBT3", "yes", theme))
	}
	if dev.CableVendorID != "" {
		rows = append(rows, row("Cable VID", dev.CableVendorID, theme))
	}
	if dev.CableProductID != "" {
		rows = append(rows, row("Cable PID", dev.CableProductID, theme))
	}
	return rows
}

func altmodeRows(dev usbi.USBDevice, theme *Theme) []string {
	if len(dev.Altmodes) == 0 {
		return nil
	}
	modes := make([]usbi.Altmode, len(dev.Altmodes))
	copy(modes, dev.Altmodes)
	sort.SliceStable(modes, func(i, j int) bool {
		if modes[i].Description != modes[j].Description {
			return modes[i].Description < modes[j].Description
		}
		return modes[i].SVID < modes[j].SVID
	})

	rows := []string{}
	for _, a := range modes {
		label := coalesce(a.Description, a.SVID)
		status := "-"
		if a.Active {
			status = "active"
		}
		if a.Mode > 0 {
			status = fmt.Sprintf("%s · mode %d", status, a.Mode)
		}
		if a.VDO != "" {
			status = fmt.Sprintf("%s · %s", status, a.VDO)
		}
		rows = append(rows, row(label, status, theme))
	}
	return rows
}

// row returns one aligned key/value line.
func row(label, value string, theme *Theme) string {
	return lipgloss.JoinHorizontal(lipgloss.Top,
		theme.Label.Render(label),
		"  ",
		theme.Value.Render(value),
	)
}
