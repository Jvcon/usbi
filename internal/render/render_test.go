package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"usbi"
)

func TestUSBTier(t *testing.T) {
	cases := []struct {
		name string
		dev  usbi.USBDevice
		want Tier
	}{
		{"empty", usbi.USBDevice{}, TierEmpty},
		{"usb1 bcd", usbi.USBDevice{BcdUSB: "0x0110"}, TierUSB1},
		{"usb2 bcd", usbi.USBDevice{BcdUSB: "0x0200"}, TierUSB2},
		{"usb2.5 bcd", usbi.USBDevice{BcdUSB: "0x0250"}, TierUSB2},
		{"usb3.0 bcd", usbi.USBDevice{BcdUSB: "0x0300"}, TierUSB3_0},
		{"usb3.1 bcd", usbi.USBDevice{BcdUSB: "0x0310"}, TierUSB3_1},
		{"usb3.2 bcd", usbi.USBDevice{BcdUSB: "0x0320"}, TierUSB3_2},
		{"usb4 bcd", usbi.USBDevice{BcdUSB: "0x0400"}, TierUSB4G3},
		{"usb2 speed", usbi.USBDevice{Speed: "480 Mb/s"}, TierUSB2},
		{"usb3.0 speed", usbi.USBDevice{Speed: "5 Gb/s"}, TierUSB3_0},
		{"usb3.2 speed", usbi.USBDevice{Speed: "20 Gb/s"}, TierUSB3_2},
		{"usb4 gen4 speed", usbi.USBDevice{Speed: "80 Gb/s"}, TierUSB4G4},
		{"usb4 bps", usbi.USBDevice{SpeedBps: 40_000_000_000}, TierUSB4G3},
		{"tbt3 enrichment", usbi.USBDevice{BcdUSB: "0x0320", CableTBT3: true}, TierTBT3},
		{"usb4 enrichment", usbi.USBDevice{Speed: "5 Gb/s", CableUSB4: true}, TierUSB4G3},
		{"tbt3 enrichment overrides usb4", usbi.USBDevice{Speed: "40 Gb/s", CableUSB4: true, CableTBT3: true}, TierTBT3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := usbTier(c.dev)
			if got != c.want {
				t.Fatalf("usbTier() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRenderOverviewFallbackTTY(t *testing.T) {
	devs := sampleDevs()
	meta := Meta{HostOS: "linux", HostArch: "amd64", BackendName: "linux-sysfs", Now: time.Now()}
	var buf bytes.Buffer
	// Stdout nil means not a TTY: must fall back to the plain table.
	opts := Options{NoColor: true, Stdout: nil, ForceWidth: 80}
	if err := RenderOverview(&buf, devs, meta, opts); err != nil {
		t.Fatalf("RenderOverview: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Backend: linux-sysfs", "USB2 Hub", "USB3 Drive", "typec-port0"} {
		if !strings.Contains(out, want) {
			t.Errorf("fallback output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("--no-color output should not contain ANSI color escapes\n%s", out)
	}
}

func TestRenderOverviewFancy(t *testing.T) {
	devs := sampleDevs()
	meta := Meta{HostOS: "linux", HostArch: "amd64", BackendName: "linux-sysfs", Now: time.Now()}
	var buf bytes.Buffer
	theme := NewTheme(true, nil, 80)
	theme.IsTTY = true // force fancy path for testing
	if err := renderOverview(&buf, devs, meta, theme); err != nil {
		t.Fatalf("renderOverview: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"usbi @ linux/amd64", "Ports", "Type-C", "USB2 Hub", "USB3 Drive", "typec-port0"} {
		if !strings.Contains(out, want) {
			t.Errorf("fancy output missing %q\n%s", want, out)
		}
	}
}

func TestRenderCardFancy(t *testing.T) {
	dev := sampleCardDev()
	meta := Meta{HostOS: "linux", HostArch: "amd64", BackendName: "linux-sysfs", Now: time.Now()}
	var buf bytes.Buffer
	theme := NewTheme(true, nil, 80)
	theme.IsTTY = true
	if err := renderCard(&buf, dev, meta, theme); err != nil {
		t.Fatalf("renderCard: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"USB4 Dock", "2-1", "USB4 Gen3", "Device", "Connector", "Cable", "Alt Modes", "DisplayPort"} {
		if !strings.Contains(out, want) {
			t.Errorf("card output missing %q\n%s", want, out)
		}
	}
}

func TestRenderOverviewEmpty(t *testing.T) {
	meta := Meta{HostOS: "linux", HostArch: "amd64", BackendName: "linux-sysfs", Now: time.Now()}
	var buf bytes.Buffer
	theme := NewTheme(true, nil, 80)
	theme.IsTTY = true
	if err := renderOverview(&buf, nil, meta, theme); err != nil {
		t.Fatalf("renderOverview empty: %v", err)
	}
	if !strings.Contains(buf.String(), "(no devices)") {
		t.Errorf("empty overview should contain '(no devices)', got:\n%s", buf.String())
	}
}

func TestGlyphs(t *testing.T) {
	if InlineGlyph(TierUSB2) != "◁─▷" {
		t.Errorf("unexpected USB2 inline glyph: %q", InlineGlyph(TierUSB2))
	}
	if !strings.Contains(BigGlyph(TierUSB4G3), "40") {
		t.Errorf("USB4 Gen3 big glyph missing 40: %q", BigGlyph(TierUSB4G3))
	}
	if !strings.Contains(BigGlyph(TierTBT3), "TBT") {
		t.Errorf("TBT3 big glyph missing TBT: %q", BigGlyph(TierTBT3))
	}
}

func sampleDevs() []usbi.USBDevice {
	return []usbi.USBDevice{
		{
			Name:              "USB2 Hub",
			LocationID:        "1-1",
			VendorID:          "0x1234",
			ProductID:         "0xabcd",
			BcdUSB:            "0x0200",
			Speed:             "480 Mb/s",
			PortConnectorType: "Type-A",
		},
		{
			Name:       "USB3 Drive",
			LocationID: "1-2",
			BcdUSB:     "0x0300",
			Speed:      "5 Gb/s",
		},
		{
			Name:              "Type-C Port 0",
			LocationID:        "typec-port0",
			PortConnectorType: "Type-C",
			DataRole:          "host",
			PowerRole:         "source",
			PowerOpMode:       "usb_power_delivery",
		},
	}
}

func sampleCardDev() usbi.USBDevice {
	return usbi.USBDevice{
		Name:              "USB4 Dock",
		LocationID:        "2-1",
		VendorID:          "0x4242",
		ProductID:         "0x0001",
		BcdUSB:            "0x0400",
		Speed:             "40 Gb/s",
		NegotiatedCurrent: 3000,
		PortConnectorType: "Type-C",
		DataRole:          "host",
		PowerRole:         "sink",
		CableType:         "active",
		CableMaxSpeed:     "USB4 Gen3 (40 Gb/s)",
		CableCurrent:      "5A",
		Altmodes: []usbi.Altmode{
			{SVID: "0xff01", Description: "DisplayPort", Mode: 1, Active: true},
		},
	}
}
