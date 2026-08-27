//go:build darwin

package usbi

// macOS USB / Type-C backend.
//
// All data is gathered by shelling out to the built-in command line tools
// system_profiler(8) and ioreg(8) through os/exec only.  No cgo, no IOKit
// private framework calls, no entitlements.  Both tools live in /usr/sbin and
// run as the invoking (unprivileged) user.
//
// Primary source: `system_profiler -json <USBDataType>`.  macOS 26+ renamed
// the USB listing to SPUSBHostDataType; older releases use SPUSBDataType.
// The correct type is detected at runtime from `system_profiler -listDataTypes`
// and cached in a package-level variable.
//
// Secondary sources (never fatal if they fail):
//   - SPThunderboltDataType -> Thunderbolt/USB4 bus topology records
//   - ioreg IOPortTransportComponentCCUSBPDSOPp / AppleTCControllerType10 /
//     AppleHPMInterfaceType10 -> USB-C cable E-marker (SOP') metadata and
//     UCSI/controller presence on Apple Silicon.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DarwinBackend is the macOS implementation of USBBackend.
type DarwinBackend struct{}

// Compile-time assertion that the backend satisfies the interface.
var _ USBBackend = (*DarwinBackend)(nil)

// NewBackend wires the Darwin backend into the CLI (see cmd/usbi/main.go).
func NewBackend() (USBBackend, string) {
	return &DarwinBackend{}, "macos-system_profiler+ioreg"
}

// ---------- system_profiler plumbing ----------

// systemProfilerList returns the output of `system_profiler -listDataTypes`
// or the empty string when the tool cannot be run.
func systemProfilerList() string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "system_profiler", "-listDataTypes").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// cachedUSBDataType remembers the detected USB data type across calls.
var cachedUSBDataType string

// chooseUSBDataType picks the USB data type used by system_profiler.
//
// macOS 26+ lists the USB tree under SPUSBHostDataType; everything older uses
// SPUSBDataType.  Detection is driven by the `-listDataTypes` output rather
// than parsing sw_vers, so it keeps working across version boundaries.
func chooseUSBDataType() string {
	if cachedUSBDataType != "" {
		return cachedUSBDataType
	}
	chosen := "SPUSBDataType"
	for _, line := range strings.Split(systemProfilerList(), "\n") {
		if !strings.Contains(line, "USB") {
			continue
		}
		if strings.Contains(line, "SPUSBHostDataType") {
			chosen = "SPUSBHostDataType"
			break
		}
		if strings.Contains(line, "SPUSBDataType") {
			chosen = "SPUSBDataType"
		}
	}
	cachedUSBDataType = chosen
	return chosen
}

// runSystemProfilerJSON runs `system_profiler -json <dataType>` and returns
// the raw JSON document.  A command failure or malformed JSON is returned as
// an error.
func runSystemProfilerJSON(dataType string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "system_profiler", "-json", dataType).Output()
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ---------- JSON shapes of the USB data type ----------

// spUSBNode mirrors one device/hub node in the USB data type output.  The
// hex identifier fields are kept as RawMessage because some macOS releases
// emit them as strings ("0x05ac") and others as numbers.
type spUSBNode struct {
	Name              string          `json:"_name"`
	Manufacturer      string          `json:"manufacturer"`
	VendorID          json.RawMessage `json:"vendor_id"`
	ProductID         json.RawMessage `json:"product_id"`
	BcdDevice         json.RawMessage `json:"bcd_device"`
	BcdUSB            json.RawMessage `json:"bcd_usb"`
	SerialNum         string          `json:"serial_num"`
	Speed             string          `json:"speed"`
	CurrentAvailable  json.Number     `json:"current_available"`
	CurrentRequired   json.Number     `json:"current_required"`
	LocationID        string          `json:"location_id"`
	USBPowerDelivery  bool            `json:"usb_power_delivery"`
	DeviceType        string          `json:"device_type"`
	DataRole          string          `json:"data_role"`
	PowerRole         string          `json:"power_role"`
	PortConnectorType string          `json:"port_connector_type"`
	Depth             *int            `json:"_depth"`
	Items             []spUSBNode     `json:"_items"`
}

// spUSBBus is one top-level bus entry of the USB data type array.  Buses are
// not real devices; they are recorded as IsBus=true topology records.
type spUSBBus struct {
	Name  string      `json:"_name"`
	Speed string      `json:"speed"`
	Items []spUSBNode `json:"_items"`
}

// parseUSBSystemProfiler turns the raw system_profiler JSON document into
// USBDevice records by recursively walking the bus/devices tree.
func parseUSBSystemProfiler(raw json.RawMessage, dataType string) ([]USBDevice, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		// Some builds emit a bare array instead of a keyed document.
		var buses []spUSBBus
		if err := json.Unmarshal(raw, &buses); err != nil {
			return nil, err
		}
		return flattenBuses(buses), nil
	}

	item, ok := top[dataType]
	if !ok {
		// Fall back to the sibling USB type key present on transitional builds.
		if dataType == "SPUSBHostDataType" {
			item, ok = top["SPUSBDataType"]
		} else {
			item, ok = top["SPUSBHostDataType"]
		}
	}
	if !ok {
		return nil, fmt.Errorf("usbi: no %q key in system_profiler output", dataType)
	}

	var buses []spUSBBus
	if err := json.Unmarshal(item, &buses); err != nil {
		return nil, err
	}
	return flattenBuses(buses), nil
}

// flattenBuses converts top-level bus entries and all nested devices into a
// flat slice.  Each node becomes its own USBDevice record.
func flattenBuses(buses []spUSBBus) []USBDevice {
	var out []USBDevice
	for i, bus := range buses {
		d := USBDevice{
			Name:     bus.Name,
			Speed:    normalizeSpeed(bus.Speed),
			SpeedBps: speedBps(bus.Speed),
			IsBus:    true,
			Source:   "macos-system_profiler",
		}
		if d.Name == "" {
			d.Name = fmt.Sprintf("USB Bus %d", i+1)
		}
		out = append(out, d)
		out = walkUSBNodes(bus.Items, 1, out)
	}
	return out
}

// walkUSBNodes recurses through a hub/device tree, appending one record per
// node and tracking the tree level for the Level field.
func walkUSBNodes(nodes []spUSBNode, level int, out []USBDevice) []USBDevice {
	for _, n := range nodes {
		d := USBDevice{
			Name:              n.Name,
			Manufacturer:      n.Manufacturer,
			VendorID:          stringifyRaw(n.VendorID),
			ProductID:         stringifyRaw(n.ProductID),
			BcdDevice:         stringifyRaw(n.BcdDevice),
			BcdUSB:            stringifyRaw(n.BcdUSB),
			SerialNumber:      n.SerialNum,
			Speed:             normalizeSpeed(n.Speed),
			SpeedBps:          speedBps(n.Speed),
			LocationID:        n.LocationID,
			DataRole:          n.DataRole,
			PowerRole:         n.PowerRole,
			PortConnectorType: n.PortConnectorType,
			PartnerPD:         n.USBPowerDelivery,
			Level:             level,
			Source:            "macos-system_profiler",
		}
		if cr, err := n.CurrentRequired.Int64(); err == nil && cr > 0 {
			d.NegotiatedCurrent = int(cr)
		} else if ca, err := n.CurrentAvailable.Int64(); err == nil && ca > 0 {
			d.NegotiatedCurrent = int(ca)
		}
		if n.Depth != nil {
			d.Level = *n.Depth
		}
		kind := strings.ToLower(n.DeviceType)
		if len(n.Items) > 0 || strings.Contains(kind, "hub") || strings.Contains(strings.ToLower(n.Name), "hub") {
			d.IsHub = true
		}
		if d.Name == "" {
			d.Name = "USB Device"
		}
		out = append(out, d)
		if len(n.Items) > 0 {
			out = walkUSBNodes(n.Items, level+1, out)
		}
	}
	return out
}

// stringifyRaw converts a RawMessage that may be a JSON string, a JSON number,
// or absent into a lowercase string.
func stringifyRaw(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.ToLower(s)
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		return strings.ToLower(num.String())
	}
	return ""
}

// normalizeSpeed trims system_profiler's "Up to " phrasing so the Speed field
// reads like "5 Gb/s" / "480 Mb/s" consistently with other platforms.
func normalizeSpeed(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "Up to ")
	s = strings.TrimSuffix(s, ".")
	return s
}

// speedRe matches "<number> <unit>" where unit is one of gbit/gb/mbit/mb/kbit/kb,
// with or without a trailing "/s" (Gb/s, Gbps, Gbit/s, ...).
var speedRe = regexp.MustCompile(`(?i)([\d.]+)\s*(gbps|gbit|gbs|gb|mbps|mbit|mbs|mb|kbps|kbit|kbs|kb)`)

// speedBps computes a decimal bits-per-second figure from a human speed label.
func speedBps(s string) uint64 {
	m := speedRe.FindStringSubmatch(s)
	if len(m) < 3 {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(m[2]) {
	case "gbps", "gbit", "gbs", "gb":
		return uint64(v * 1e9)
	case "mbps", "mbit", "mbs", "mb":
		return uint64(v * 1e6)
	case "kbps", "kbit", "kbs", "kb":
		return uint64(v * 1e3)
	}
	return 0
}

// ---------- Thunderbolt / USB4 topology (secondary source) ----------

// spTBPort mirrors one port entry of a Thunderbolt bus.
type spTBPort struct {
	Name       string `json:"_name"`
	LinkStatus string `json:"link_status"`
	LinkSpeed  string `json:"link_speed"`
}

// spTBBus mirrors one top-level Thunderbolt bus.  macOS exposes the ports both
// under "port" and, on some versions, "devices".
type spTBBus struct {
	Name    string     `json:"_name"`
	Speed   string     `json:"speed"`
	Ports   []spTBPort `json:"port"`
	Devices []spTBPort `json:"devices"`
}

// parseThunderboltBus parses SPThunderboltDataType into reference bus records.
// Only the top-level bus nodes are collected, per the simplified scope; the
// real USB endpoints on the other side of a Thunderbolt port are hidden behind
// an internal hub and are not addressable through system_profiler.  Failures
// are non-fatal and yield no records.
func parseThunderboltBus() []USBDevice {
	raw, err := runSystemProfilerJSON("SPThunderboltDataType")
	if err != nil {
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil
	}
	item, ok := top["SPThunderboltDataType"]
	if !ok {
		return nil
	}
	var buses []spTBBus
	if err := json.Unmarshal(item, &buses); err != nil {
		return nil
	}

	var out []USBDevice
	for i, b := range buses {
		d := USBDevice{
			Name:   b.Name,
			IsBus:  true,
			Source: "macos-system_profiler+thunderbolt",
		}
		if d.Name == "" {
			d.Name = fmt.Sprintf("Thunderbolt Bus %d", i+1)
		}
		// Take the fastest speed reported by a connected port.
		speed := b.Speed
		for _, ports := range [][]spTBPort{b.Ports, b.Devices} {
			for _, p := range ports {
				if strings.TrimSpace(p.LinkStatus) != "0x2" {
					continue
				}
				if sp := linkSpeedLabel(p.LinkSpeed); speedBps(sp) > speedBps(speed) {
					speed = sp
				}
			}
		}
		if speed == "" {
			speed = "Up to 40 Gb/s"
		}
		d.Speed = normalizeSpeed(speed)
		d.SpeedBps = speedBps(speed)
		out = append(out, d)
	}
	return out
}

// linkSpeedLabel maps the Apple Thunderbolt link_speed hex code to a human
// label.  0x1 = 20 Gb/s, 0x2 = 40 Gb/s, 0x3 = 80 Gb/s (USB4).
func linkSpeedLabel(h string) string {
	switch strings.ToLower(strings.TrimSpace(h)) {
	case "0x1":
		return "Up to 20 Gb/s"
	case "0x2":
		return "Up to 40 Gb/s"
	case "0x3":
		return "Up to 80 Gb/s"
	}
	return ""
}

// ---------- ioreg plumbing ----------

// runIOreg runs `ioreg -l -r -c <class>` and returns stdout, or the empty
// string on any failure.  The Type-C controller classes only exist on Apple
// Silicon, so an empty result on older Intel Macs is expected and never fatal.
func runIOreg(class string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-l", "-r", "-c", class).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

var (
	reVendorID    = regexp.MustCompile(`"Vendor ID"\s*=\s*(0x[0-9a-fA-F]+|\d+)`)
	reProductID   = regexp.MustCompile(`"Product ID"\s*=\s*(0x[0-9a-fA-F]+|\d+)`)
	reSpecRev     = regexp.MustCompile(`"Specification Revision"\s*=\s*(0x[0-9a-fA-F]+|\d+)`)
	reProductType = regexp.MustCompile(`"Product Type"\s*=\s*(0x[0-9a-fA-F]+|\d+)`)
	reVDOs        = regexp.MustCompile(`"VDOs"\s*=\s*\(([^)]*)\)`)
)

// parseTypeCEMarker probes the IORegistry for the Apple Silicon Type-C
// controller classes and the SOP' E-marker service, merging everything found
// into one synthetic cable device.  It returns ok=false when no ioreg output
// was produced at all (e.g. an Intel Mac).
func parseTypeCEMarker() (USBDevice, bool) {
	tcController := runIOreg("AppleTCControllerType10")  // M1 / M2 ports
	hpmController := runIOreg("AppleHPMInterfaceType10") // M3+ ports
	emarker := runIOreg("IOPortTransportComponentCCUSBPDSOPp")

	if tcController == "" && hpmController == "" && emarker == "" {
		return USBDevice{}, false
	}

	d := USBDevice{
		Name:           "USB-C Cable E-marker",
		Source:         "macos-ioreg",
		CablePlugType:  "type-c",
		UCSIPresent:    tcController != "" || hpmController != "",
		CableSOPPPrime: emarker != "",
	}
	if parseEmarker(emarker, &d) {
		d.CablePresent = true
	}
	return d, true
}

// parseEmarker extracts E-marker (cable) fields from the SOPP ioreg dump and
// stores them on d.  It reports whether any E-marker field was found.
func parseEmarker(out string, d *USBDevice) bool {
	found := false
	if v, ok := firstNum(out, reVendorID); ok {
		d.CableVendorID = fmt.Sprintf("0x%04x", v)
		found = true
	}
	if v, ok := firstNum(out, reProductID); ok {
		d.CableProductID = fmt.Sprintf("0x%04x", v)
		found = true
	}
	// PD revision carried by the E-marker: bits 7..4 major, bits 3..0 minor.
	if v, ok := firstNum(out, reSpecRev); ok {
		d.PDRevision = fmt.Sprintf("%d.%d", (v>>4)&0xf, v&0xf)
		found = true
	}
	if v, ok := firstNum(out, reProductType); ok {
		switch v {
		case 3:
			d.CableType = "passive"
		case 4:
			d.CableType = "active"
		case 6:
			d.CableType = "vpd"
		}
		found = true
	}
	vdos := parseHexVDOs(out)
	if len(vdos) > 0 {
		v := vdos[0] // the first VDO is the Cable VDO
		bits := v & 0x7
		d.CableMaxSpeed = cableSpeedFromBits(bits)
		d.CableCurrent = cableCurrentFromBits(v)
		d.CableVBUSThrough = cableVbusFromBits(v)
		d.CableUSB4 = bits == 3 || bits == 4 || bits == 5
		d.CableUSB32 = bits == 1 || bits == 2
		d.CableTBT3 = bits == 3
		found = true
	}
	return found
}

// firstNum extracts the first numeric token matched by re (hex or decimal).
func firstNum(s string, re *regexp.Regexp) (uint32, bool) {
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0, false
	}
	return parseNum(m[1])
}

// parseNum parses a hex ("0x...") or decimal integer token.
func parseNum(s string) (uint32, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		base = 16
		s = s[2:]
	}
	v, err := strconv.ParseUint(s, base, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

// parseHexVDOs finds the first `"VDOs" = (0x..., 0x..., ...)` group in the
// ioreg dump and returns every token as a uint32.
func parseHexVDOs(s string) []uint32 {
	m := reVDOs.FindStringSubmatch(s)
	if len(m) < 2 {
		return nil
	}
	var out []uint32
	for _, tok := range strings.Split(m[1], ",") {
		if v, ok := parseNum(tok); ok {
			out = append(out, v)
		}
	}
	return out
}

// cableSpeedFromBits maps the Cable VDO bits 2:0 to a speed label.
func cableSpeedFromBits(b uint32) string {
	switch b & 0x7 {
	case 0:
		return "USB 2.0 (480 Mb/s)"
	case 1:
		return "USB 3.2 Gen1 (5 Gb/s)"
	case 2:
		return "USB 3.2 Gen2 (10 Gb/s)"
	case 3:
		return "USB4 Gen3 (40 Gb/s)"
	case 4:
		return "USB4 Gen4 (80 Gb/s)"
	case 5:
		return "USB4 120 Gb/s (asymmetric)"
	default:
		return "unknown"
	}
}

// cableCurrentFromBits maps the Cable VDO bits 6:5 to a current rating.
func cableCurrentFromBits(b uint32) string {
	if (b>>5)&0x3 == 2 {
		return "3A"
	}
	return "5A"
}

// cableVbusFromBits maps the Cable VDO bits 10:9 to the VBUS through rating.
func cableVbusFromBits(b uint32) string {
	switch (b >> 9) & 0x3 {
	case 1:
		return "5V"
	case 2:
		return "12V"
	case 3:
		return "20V"
	default:
		return "0V"
	}
}

// ---------- Backend entry point ----------

// ListDevices collects USB topology from system_profiler and enriches it with
// Thunderbolt bus records and USB-C E-marker metadata from ioreg.
//
// Only a failure of the primary system_profiler source is surfaced as an
// error; the Thunderbolt and ioreg sources degrade silently.
func (b *DarwinBackend) ListDevices() ([]USBDevice, error) {
	dataType := chooseUSBDataType()
	raw, err := runSystemProfilerJSON(dataType)
	if err != nil {
		return nil, fmt.Errorf("usbi: system_profiler -json %s failed: %w", dataType, err)
	}
	devices, err := parseUSBSystemProfiler(raw, dataType)
	if err != nil {
		return nil, fmt.Errorf("usbi: parse system_profiler %s output: %w", dataType, err)
	}

	// Secondary source 1: Thunderbolt / USB4 bus topology (non-fatal).
	devices = append(devices, parseThunderboltBus()...)

	// Secondary source 2: USB-C cable E-marker via ioreg (non-fatal).
	if tc, ok := parseTypeCEMarker(); ok {
		devices = append(devices, tc)
	}

	return devices, nil
}
