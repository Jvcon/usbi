// Package render provides the neofetch-style human-readable renderer for usbi.
//
// It exposes two high-level entry points:
//
//   - RenderOverview: the default / --all view with a banner, port grid, and
//     Type-C summary.
//   - RenderCard: the single-port neofetch-style card triggered by -p/--port.
//
// The package automatically degrades to a plain width-aware table when stdout
// is not a TTY or the terminal is too narrow.
package render

import (
	"io"
	"os"
	"time"

	"golang.org/x/term"
	"usbi"
)

// Meta carries the host/context information shown in headers.
type Meta struct {
	HostOS, HostArch string
	BackendName      string
	Now              time.Time
}

// Options controls one render pass.
type Options struct {
	NoColor    bool
	Stdout     *os.File // for background detection and terminal size
	ForceWidth int      // 0 = auto-detect
}

// RenderOverview prints the default / --all overview to w.
func RenderOverview(w io.Writer, devs []usbi.USBDevice, meta Meta, opts Options) error {
	theme := NewTheme(opts.NoColor, opts.Stdout, opts.ForceWidth)
	if !theme.ShouldRenderFancy() {
		return renderTable(w, devs, meta)
	}
	return renderOverview(w, devs, meta, theme)
}

// RenderCard prints a single device in neofetch card form to w.
func RenderCard(w io.Writer, dev usbi.USBDevice, meta Meta, opts Options) error {
	theme := NewTheme(opts.NoColor, opts.Stdout, opts.ForceWidth)
	if !theme.ShouldRenderFancy() {
		return renderCardTable(w, dev, meta)
	}
	return renderCard(w, dev, meta, theme)
}

// termSize wraps golang.org/x/term.GetSize.
func termSize(out *os.File) (int, int, error) {
	if out == nil {
		return 0, 0, os.ErrInvalid
	}
	return term.GetSize(int(out.Fd()))
}

// isTerminal reports whether f is a terminal.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
