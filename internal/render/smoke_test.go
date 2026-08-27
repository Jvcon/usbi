package render

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"

	"usbi"
)

// macOS-shaped device list: many entries have no LocationID, several names
// are duplicated.  This exercises both the index column and the findDevice
// code path (covered in cmd/usbi/main_test.go).
func macOSSampleDevs() []usbi.USBDevice {
	return []usbi.USBDevice{
		{Name: "AX88179A", Level: 2, Source: "macos-system_profiler", LocationID: "0x14100000"},
		{Name: "USB 3.1 Bus", IsBus: true, Source: "macos-system_profiler"},
		{Name: "USB 3.1 Bus", IsBus: true, Source: "macos-system_profiler"},
		{Name: "USB-C Cable E-marker", CablePlugType: "type-c", UCSIPresent: true, Source: "macos-ioreg"},
		{Name: "USB2.1 Hub", IsHub: true, Level: 1, Source: "macos-system_profiler", LocationID: "0x14200000"},
		{Name: "USB3.1 Hub", IsHub: true, Level: 1, Source: "macos-system_profiler"},
		{Name: "thunderboltusb4_bus_0", Speed: "40 Gb/s", SpeedBps: 40_000_000_000, IsBus: true, Source: "macos-system_profiler+thunderbolt"},
		{Name: "thunderboltusb4_bus_1", Speed: "40 Gb/s", SpeedBps: 40_000_000_000, IsBus: true, Source: "macos-system_profiler+thunderbolt"},
	}
}

func TestRenderOverviewFancyShowsIndex(t *testing.T) {
	devs := macOSSampleDevs()
	meta := Meta{HostOS: "darwin", HostArch: "arm64", BackendName: "macos-system_profiler+ioreg", Now: time.Now()}
	var buf bytes.Buffer
	theme := NewTheme(true, nil, 120)
	theme.IsTTY = true
	if err := renderOverview(&buf, devs, meta, theme); err != nil {
		t.Fatalf("renderOverview: %v", err)
	}
	out := buf.String()
	// Every 1-based index must appear.
	for _, idx := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		if !strings.Contains(out, idx) {
			t.Errorf("fancy overview missing index %q\n%s", idx, out)
		}
	}
	// And every device name.
	for _, name := range []string{"AX88179A", "USB 3.1 Bus", "USB-C Cable E-marker", "thunderboltusb4_bus_0"} {
		if !strings.Contains(out, name) {
			t.Errorf("fancy overview missing %q\n%s", name, out)
		}
	}
}

func TestRenderTableShowsIndex(t *testing.T) {
	devs := macOSSampleDevs()
	meta := Meta{HostOS: "darwin", HostArch: "arm64", BackendName: "macos-system_profiler+ioreg", Now: time.Now()}
	var buf bytes.Buffer
	if err := renderTable(&buf, devs, meta); err != nil {
		t.Fatalf("renderTable: %v", err)
	}
	out := buf.String()
	// Header must contain the # column.
	if !strings.Contains(out, "#") {
		t.Errorf("table header missing '#' column\n%s", out)
	}
	// Every device must appear on a row whose leading 1-based index matches
	// its position in the input slice (the renderer does not re-sort).
	for i, d := range devs {
		wantIdx := strconv.Itoa(i + 1)
		wantName := d.Name
		if wantName == "" {
			wantName = "USB Device"
		}
		ok := false
		for _, line := range strings.Split(out, "\n") {
			trimmed := strings.TrimLeft(line, " ")
			if !strings.HasPrefix(trimmed, wantIdx+"  ") {
				continue
			}
			if strings.Contains(trimmed, wantName) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("table row %d (%q) not found or wrong index\n%s", i+1, wantName, out)
		}
	}
}
