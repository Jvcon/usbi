package main

import (
	"testing"

	"usbi"
)

// findDevice fixtures model a realistic macOS output where top-level buses
// and the E-marker cable do not have a LocationID but still need to be
// addressable through `--port`.
func macOSSampleDevs() []usbi.USBDevice {
	return []usbi.USBDevice{
		{Name: "AX88179A", LocationID: "0x14100000", VendorID: "0x0b95", ProductID: "0x1790"},
		{Name: "USB 3.1 Bus", IsBus: true},
		{Name: "USB 3.1 Bus", IsBus: true}, // duplicate name, no LocationID
		{Name: "USB-C Cable E-marker", CablePlugType: "type-c"},
		{Name: "USB2.1 Hub", LocationID: "0x14200000", IsHub: true},
		{Name: "thunderboltusb4_bus_0", IsBus: true, Speed: "40 Gb/s"},
	}
}

func TestFindDeviceByIndex(t *testing.T) {
	devs := macOSSampleDevs()
	for _, c := range []struct {
		sel  string
		want string
	}{
		{"1", "AX88179A"},
		{"2", "USB 3.1 Bus"},
		{"4", "USB-C Cable E-marker"},
		{"6", "thunderboltusb4_bus_0"},
	} {
		t.Run(c.sel, func(t *testing.T) {
			d, ok := findDevice(devs, c.sel)
			if !ok {
				t.Fatalf("findDevice(%q) returned !ok", c.sel)
			}
			if d.Name != c.want {
				t.Fatalf("findDevice(%q).Name = %q, want %q", c.sel, d.Name, c.want)
			}
		})
	}
}

func TestFindDeviceOutOfRangeIndex(t *testing.T) {
	devs := macOSSampleDevs()
	for _, sel := range []string{"0", "7", "99", "-1"} {
		if _, ok := findDevice(devs, sel); ok {
			t.Errorf("findDevice(%q) should fail (out of range)", sel)
		}
	}
}

func TestFindDeviceByLocationIDExact(t *testing.T) {
	devs := macOSSampleDevs()
	d, ok := findDevice(devs, "0x14100000")
	if !ok || d.Name != "AX88179A" {
		t.Fatalf("findDevice by exact LocationID failed: ok=%v name=%q", ok, d.Name)
	}
	// Case-insensitive.
	d, ok = findDevice(devs, "0X14100000")
	if !ok || d.Name != "AX88179A" {
		t.Fatalf("findDevice by case-insensitive LocationID failed: ok=%v name=%q", ok, d.Name)
	}
}

func TestFindDeviceByLocationIDPrefix(t *testing.T) {
	devs := macOSSampleDevs()
	d, ok := findDevice(devs, "0x1410")
	if !ok || d.Name != "AX88179A" {
		t.Fatalf("findDevice by LocationID prefix failed: ok=%v name=%q", ok, d.Name)
	}
}

func TestFindDeviceByNamePrefix(t *testing.T) {
	devs := macOSSampleDevs()
	// "USB 3.1" matches both USB 3.1 Bus entries → ambiguous.
	if _, ok := findDevice(devs, "USB 3.1"); ok {
		t.Fatalf("findDevice(%q) should fail (ambiguous name prefix)", "USB 3.1")
	}
	// "USB-C Cable" matches one.
	d, ok := findDevice(devs, "USB-C Cable")
	if !ok || d.Name != "USB-C Cable E-marker" {
		t.Fatalf("findDevice by name prefix failed: ok=%v name=%q", ok, d.Name)
	}
	// "thunderboltusb4" matches one.
	d, ok = findDevice(devs, "thunderboltusb4")
	if !ok || d.Name != "thunderboltusb4_bus_0" {
		t.Fatalf("findDevice by name prefix failed: ok=%v name=%q", ok, d.Name)
	}
	// Case-insensitive.
	d, ok = findDevice(devs, "usb-c cable")
	if !ok || d.Name != "USB-C Cable E-marker" {
		t.Fatalf("findDevice by case-insensitive name prefix failed: ok=%v name=%q", ok, d.Name)
	}
}

func TestFindDeviceNoMatch(t *testing.T) {
	devs := macOSSampleDevs()
	for _, sel := range []string{"", "does-not-exist", "nope"} {
		if _, ok := findDevice(devs, sel); ok {
			t.Errorf("findDevice(%q) should fail (no match)", sel)
		}
	}
}

// Index wins over prefix — make sure a numeric selector always wins.
func TestFindDeviceNumericAlwaysIndex(t *testing.T) {
	devs := []usbi.USBDevice{
		{Name: "AAA", LocationID: ""},
		{Name: "BBB", LocationID: ""},
	}
	// "1" matches index 1 → AAA.  No name prefix should ever win.
	d, ok := findDevice(devs, "1")
	if !ok || d.Name != "AAA" {
		t.Fatalf("numeric selector must match by index, got ok=%v name=%q", ok, d.Name)
	}
}
