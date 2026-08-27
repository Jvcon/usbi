package render

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"usbi"
)

// renderTable is the width-aware fallback plain table for overview mode.
func renderTable(w io.Writer, devs []usbi.USBDevice, meta Meta) error {
	fmt.Fprintf(w, "Backend: %s\n", meta.BackendName)
	fmt.Fprintf(w, "Devices: %d\n\n", len(devs))

	if len(devs) == 0 {
		fmt.Fprintln(w, "(no devices)")
		return nil
	}

	maxName := len("Name")
	maxLoc := len("Location")
	idxWidth := len(fmt.Sprintf("%d", len(devs)))
	if idxWidth < 2 {
		idxWidth = 2
	}
	for _, d := range devs {
		if l := len(d.Name); l > maxName {
			maxName = l
		}
		if l := len(d.LocationID); l > maxLoc {
			maxLoc = l
		}
	}
	maxName = min(maxName, 48)
	maxLoc = min(maxLoc, 32)

	idxFmt := "%" + fmt.Sprintf("%d", idxWidth) + "s"
	header := fmt.Sprintf(idxFmt+"  %-*s  %-*s  %-9s  %-9s  %-9s  %-9s  %s",
		"#", maxName, "Name", maxLoc, "Location", "VID", "PID", "Speed", "Power",
		"Type-C / Cable")
	fmt.Fprintln(w, header)
	fmt.Fprintln(w, strings.Repeat("-", len(header)))

	idxNumFmt := "%" + fmt.Sprintf("%d", idxWidth) + "d"
	for i, d := range devs {
		name := trunc(d.Name, maxName)
		loc := trunc(d.LocationID, maxLoc)

		vid := d.VendorID
		if vid == "" {
			vid = "-"
		}
		pid := d.ProductID
		if pid == "" {
			pid = "-"
		}
		speed := d.Speed
		if speed == "" {
			speed = "-"
		}
		power := "-"
		if d.NegotiatedCurrent > 0 {
			power = fmt.Sprintf("%d mA", d.NegotiatedCurrent)
		}

		extra := formatTypeC(d)

		fmt.Fprintf(w, idxNumFmt+"  %-*s  %-*s  %-9s  %-9s  %-9s  %-9s  %s\n",
			i+1, maxName, name, maxLoc, loc, vid, pid, speed, power, extra)
	}
	return nil
}

// renderCardTable is the fallback plain rendering for a single device.
func renderCardTable(w io.Writer, dev usbi.USBDevice, meta Meta) error {
	fmt.Fprintf(w, "usbi @ %s/%s · backend: %s\n\n", meta.HostOS, meta.HostArch, meta.BackendName)
	fmt.Fprintf(w, "Device: %s\n", coalesce(dev.Name, "unnamed"))
	fmt.Fprintf(w, "Location: %s\n", coalesce(dev.LocationID, "-"))
	fmt.Fprintf(w, "Tier: %s\n", TierLabel(usbTier(dev)))
	fmt.Fprintf(w, "VID: %s\n", coalesce(dev.VendorID, "-"))
	fmt.Fprintf(w, "PID: %s\n", coalesce(dev.ProductID, "-"))
	fmt.Fprintf(w, "Speed: %s\n", coalesce(dev.Speed, "-"))
	if dev.NegotiatedCurrent > 0 {
		fmt.Fprintf(w, "Power: %d mA\n", dev.NegotiatedCurrent)
	}
	if dev.PortConnectorType != "" {
		fmt.Fprintf(w, "Connector: %s\n", dev.PortConnectorType)
	}
	if dev.CableType != "" || dev.CableMaxSpeed != "" {
		fmt.Fprintf(w, "Cable: %s\n", strings.TrimSpace(dev.CableType+" "+dev.CableMaxSpeed))
	}
	if len(dev.Altmodes) > 0 {
		alts := make([]string, 0, len(dev.Altmodes))
		for _, a := range dev.Altmodes {
			if a.Description != "" {
				alts = append(alts, a.Description)
			} else {
				alts = append(alts, a.SVID)
			}
		}
		sort.Strings(alts)
		fmt.Fprintf(w, "Alt Modes: %s\n", strings.Join(alts, ", "))
	}
	return nil
}

func formatTypeC(d usbi.USBDevice) string {
	parts := []string{}
	if d.PortConnectorType != "" {
		parts = append(parts, "port="+d.PortConnectorType)
	}
	if d.PowerOpMode != "" {
		parts = append(parts, "mode="+d.PowerOpMode)
	}
	if d.CableType != "" {
		parts = append(parts, "cable="+d.CableType)
	}
	if d.CableMaxSpeed != "" {
		parts = append(parts, "speed="+d.CableMaxSpeed)
	}
	if d.CableCurrent != "" {
		parts = append(parts, "I="+d.CableCurrent)
	}
	if d.CableUSB4 {
		parts = append(parts, "USB4")
	}
	if d.CableTBT3 {
		parts = append(parts, "TBT3")
	}
	if len(d.Altmodes) > 0 {
		alts := []string{}
		for _, a := range d.Altmodes {
			t := a.SVID
			if a.Description != "" {
				t = a.Description
			}
			alts = append(alts, t)
		}
		parts = append(parts, "alt=["+strings.Join(alts, ",")+"]")
	}
	if len(d.Unavailable) > 0 {
		parts = append(parts, "n/a="+strings.Join(d.Unavailable, ","))
	}
	return strings.Join(parts, " ")
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
