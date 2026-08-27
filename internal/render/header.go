package render

import (
	"fmt"
	"strings"
	"time"
)

// usbibanner is the fixed ASCII logotype for usbi. Each line is trimmed so
// lipgloss doesn't paint trailing whitespace when a foreground color is set.
const usbiBanner = `   __ _____  ________  ___    ___
  / // / _ \/  _/ __ \/ _ \  / _ \
 / _  / ___// / /_/ / , _/ / ___/
/_//_/_/  /___/\____/_/|_| /_/`

// renderUSBIHeader returns the banner plus the one-line context string.
func renderUSBIHeader(theme *Theme, meta Meta) string {
	banner := theme.Banner.Render(usbiBanner)
	// lipgloss normalizes rendered blocks to the longest line width, painting
	// trailing whitespace with the style's foreground. Strip that artifact
	// per line so the banner is clean.
	banner = trimLineRight(banner)
	ctx := theme.Muted.Render(fmt.Sprintf(
		"usbi @ %s/%s · backend: %s · %s",
		meta.HostOS,
		meta.HostArch,
		meta.BackendName,
		meta.Now.Format(time.RFC3339),
	))
	return banner + "\n" + ctx
}

// trimLineRight right-trims spaces and tabs from every line of s.
func trimLineRight(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Join(lines, "\n")
}
