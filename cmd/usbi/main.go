// Command usbi lists USB and Type-C devices on the current host.
//
// The output is aligned across Linux, macOS and Windows: each row is a
// USBDevice with as many of the common fields filled in as the host platform
// can expose.  Fields the platform could not read are simply omitted from the
// JSON output (or, in text mode, shown as "-" / "N/A").
//
// Usage:
//
//	usbi                # human-readable table
//	usbi -json          # machine-readable JSON array
//	usbi -json -indent  # pretty-printed JSON
//	usbi -h             # help
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"

	"usbi"
)

// backendFactory returns the platform-specific USBBackend.  Each platform
// implementation lives in its own _<goos>.go file (selected by build tag) and
// is wired in here.  On unsupported platforms, ListDevices returns an error
// but the program still prints a friendly message and exits non-zero.
type backendFactory func() (usbi.USBBackend, string)

var (
	jsonFlag    = flag.Bool("json", false, "emit JSON array of USBDevice instead of a human-readable table")
	indentFlag  = flag.Bool("indent", false, "pretty-print JSON output (only meaningful with -json)")
	filterHub   = flag.Bool("include-hubs", true, "include USB hubs in the output")
	filterTC    = flag.Bool("include-typec", true, "include Type-C port records in the output")
	showVersion = flag.Bool("version", false, "print version and exit")
)

func main() {
	flag.Usage = func() { usage(os.Stderr) }
	flag.Parse()

	if *showVersion {
		fmt.Printf("usbi %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return
	}

	factory, name := pickBackend()
	if factory == nil {
		fmt.Fprintf(os.Stderr, "usbi: no backend available on %s\n", runtime.GOOS)
		os.Exit(2)
	}

	backend, _ := factory()
	devs, err := backend.ListDevices()
	if err != nil {
		fmt.Fprintf(os.Stderr, "usbi: list devices: %v\n", err)
		os.Exit(1)
	}

	// Filter
	filtered := devs[:0]
	for _, d := range devs {
		if d.IsHub && !*filterHub {
			continue
		}
		if d.PortConnectorType == "Type-C" && !*filterTC {
			continue
		}
		filtered = append(filtered, d)
	}

	// Stable order: by location, then name
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].LocationID != filtered[j].LocationID {
			return filtered[i].LocationID < filtered[j].LocationID
		}
		return filtered[i].Name < filtered[j].Name
	})

	if *jsonFlag {
		out := os.Stdout
		var (
			b   []byte
			err error
		)
		if *indentFlag {
			b, err = json.MarshalIndent(filtered, "", "  ")
		} else {
			b, err = json.Marshal(filtered)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "usbi: marshal json: %v\n", err)
			os.Exit(1)
		}
		out.Write(b)
		if len(b) == 0 || b[len(b)-1] != '\n' {
			out.Write([]byte("\n"))
		}
		return
	}

	printHuman(os.Stdout, filtered, name)
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "usbi — cross-platform USB & Type-C device information\n\n")
	fmt.Fprintf(w, "Usage: %s [flags]\n\nFlags:\n", os.Args[0])
	flag.CommandLine.SetOutput(w)
	flag.PrintDefaults()
}

// pickBackend selects the platform backend.  The concrete backend type is
// provided by a build-tagged NewBackend() defined in the platform files, so
// this file never references a type that does not exist on the current target.
func pickBackend() (backendFactory, string) {
	backend, name := usbi.NewBackend()
	if backend == nil {
		return nil, ""
	}
	return func() (usbi.USBBackend, string) { return backend, name }, name
}

func printHuman(w io.Writer, devs []usbi.USBDevice, backendName string) {
	fmt.Fprintf(w, "Backend: %s\n", backendName)
	fmt.Fprintf(w, "Devices: %d\n\n", len(devs))

	if len(devs) == 0 {
		fmt.Fprintln(w, "(no devices)")
		return
	}

	// Wide first pass to size columns.
	maxName := len("Name")
	maxLoc := len("Location")
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

	header := fmt.Sprintf("%-*s  %-*s  %-9s  %-9s  %-9s  %-9s  %s",
		maxName, "Name", maxLoc, "Location", "VID", "PID", "Speed", "Power",
		"Type-C / Cable")
	fmt.Fprintln(w, header)
	fmt.Fprintln(w, strings.Repeat("-", len(header)))

	for _, d := range devs {
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

		fmt.Fprintf(w, "%-*s  %-*s  %-9s  %-9s  %-9s  %-9s  %s\n",
			maxName, name, maxLoc, loc, vid, pid, speed, power, extra)
	}
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
