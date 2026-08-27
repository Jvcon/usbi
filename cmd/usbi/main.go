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
	"time"

	"usbi"
	"usbi/internal/render"
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
	portFlag    = flag.String("port", "", "show a single device as a neofetch-style card (also -p)")
	allFlag     = flag.Bool("all", true, "show the overview of all devices (default)")
	noColorFlag = flag.Bool("no-color", false, "disable color output")
)

func main() {
	flag.StringVar(portFlag, "p", "", "alias for -port")
	flag.Usage = func() { usage(os.Stderr) }
	flag.Parse()

	// Honor --no-color and the NO_COLOR env var. Setting the env var ensures
	// lipgloss's color layer also skips coloring before any style is rendered.
	if *noColorFlag || os.Getenv("NO_COLOR") != "" {
		os.Setenv("NO_COLOR", "1")
		*noColorFlag = true
	}

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

	meta := render.Meta{
		HostOS:      runtime.GOOS,
		HostArch:    runtime.GOARCH,
		BackendName: name,
		Now:         time.Now(),
	}
	opts := render.Options{
		NoColor: *noColorFlag,
		Stdout:  os.Stdout,
	}

	if *portFlag != "" {
		dev, ok := findDevice(filtered, *portFlag)
		if !ok {
			fmt.Fprintf(os.Stderr, "usbi: no unique device matches location %q\n", *portFlag)
			os.Exit(1)
		}
		if err := render.RenderCard(os.Stdout, dev, meta, opts); err != nil {
			fmt.Fprintf(os.Stderr, "usbi: render card: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := render.RenderOverview(os.Stdout, filtered, meta, opts); err != nil {
		fmt.Fprintf(os.Stderr, "usbi: render overview: %v\n", err)
		os.Exit(1)
	}
}

// findDevice matches by LocationID exact (case-insensitive), then by prefix if
// exactly one prefix match exists.
func findDevice(devs []usbi.USBDevice, loc string) (usbi.USBDevice, bool) {
	want := strings.ToLower(loc)
	var exact usbi.USBDevice
	exactOK := false
	for _, d := range devs {
		got := strings.ToLower(d.LocationID)
		if got == want {
			if exactOK {
				return usbi.USBDevice{}, false
			}
			exact = d
			exactOK = true
		}
	}
	if exactOK {
		return exact, true
	}

	var prefix usbi.USBDevice
	prefixOK := false
	for _, d := range devs {
		got := strings.ToLower(d.LocationID)
		if strings.HasPrefix(got, want) {
			if prefixOK {
				return usbi.USBDevice{}, false
			}
			prefix = d
			prefixOK = true
		}
	}
	if prefixOK {
		return prefix, true
	}
	return usbi.USBDevice{}, false
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "usbi — cross-platform USB & Type-C device information\n\n")
	fmt.Fprintf(w, "Usage: %s [flags]\n\nFlags:\n", os.Args[0])
	fmt.Fprintf(w, "  -p, --port <location>   show a single device as a neofetch-style card\n")
	fmt.Fprintf(w, "      --all               show the overview of all devices (default)\n")
	fmt.Fprintf(w, "      --no-color          disable color output\n")
	fmt.Fprintf(w, "      --json              emit JSON array of USBDevice\n")
	fmt.Fprintf(w, "      --indent            pretty-print JSON output\n")
	fmt.Fprintf(w, "      --include-hubs      include USB hubs in the output (default true)\n")
	fmt.Fprintf(w, "      --include-typec     include Type-C port records in the output (default true)\n")
	fmt.Fprintf(w, "      --version           print version and exit\n")
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


