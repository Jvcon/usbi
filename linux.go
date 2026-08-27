//go:build linux

// Package usbi provides the Linux sysfs-backed USB / Type-C backend.
//
// Everything is read from the kernel sysfs tree with no cgo and no external
// dependencies:
//
//	/sys/bus/usb/devices/    one directory per USB device ("1-1", "1-1.2", ...)
//	/sys/class/typec/        Type-C ports, partners, cables and plugs
//	/sys/bus/typec/devices/  Type-C alternate modes (portN-partner.N, ...)
//
// Every attribute read is guarded by an os.Stat() first; a missing or
// unreadable attribute simply leaves the corresponding field empty and never
// produces an error, so a partially broken sysfs still yields useful output.
package usbi

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	sysUSBDevices = "/sys/bus/usb/devices"
	sysTypeC      = "/sys/class/typec"
	sysTypeCBus   = "/sys/bus/typec/devices"
)

// LinuxBackend collects USB and Type-C information from the Linux sysfs.
type LinuxBackend struct{}

// Compile-time assertion that the backend satisfies the interface.
var _ USBBackend = (*LinuxBackend)(nil)

// NewBackend wires the Linux backend into the CLI (see cmd/usbi/main.go).
func NewBackend() (USBBackend, string) {
	return &LinuxBackend{}, "linux-sysfs"
}

// ListDevices returns every USB device found in sysfs followed by every
// Type-C port with its partner / cable / alt-mode context.  An error is
// returned only when neither /sys/bus/usb/devices nor /sys/class/typec is
// readable; any single attribute failure degrades to "information not
// available" for that field.
func (b *LinuxBackend) ListDevices() ([]USBDevice, error) {
	devices, usbErr := listUSBDevices()
	ports, typecErr := listTypeCPorts()
	if usbErr != nil && typecErr != nil {
		return nil, fmt.Errorf("usbi: cannot read USB sysfs (%v) and Type-C sysfs (%v)", usbErr, typecErr)
	}
	return append(devices, ports...), nil
}

// ---------- helpers ----------

// readFile returns the trimmed contents of path, or "" when the path does not
// exist or cannot be read (os.Stat is always consulted first).
func readFile(path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readBinary returns the raw bytes of path (no trimming), or nil on any error.
// Used for the binary `descriptors` file.
func readBinary(path string) []byte {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// parseHex parses a hexadecimal string with an optional "0x" prefix.
func parseHex(s string) (uint32, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

// extractU32 parses a hex string and returns 0 on any failure.
func extractU32(s string) uint32 {
	v, _ := parseHex(s)
	return v
}

// atoi parses a decimal integer, returning 0 on failure.
func atoi(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}

// parseIndex parses a decimal index, falling back to hexadecimal.
func parseIndex(s string) int {
	s = strings.TrimSpace(s)
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	if v, ok := parseHex(s); ok {
		return int(v)
	}
	return 0
}

// parsePDRev parses a PD revision label ("2.0", "3.1", ...) into a number;
// 0 is returned for anything unparsable.  A value of "0.0" means the port is
// not running USB Power Delivery.
func parsePDRev(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

// usbClassName maps a USB device class code to its well-known name.
func usbClassName(v uint32) string {
	switch v {
	case 0x00:
		return "Device"
	case 0x01:
		return "Audio"
	case 0x02:
		return "Communications"
	case 0x03:
		return "HID"
	case 0x05:
		return "Physical"
	case 0x06:
		return "Image"
	case 0x07:
		return "Printer"
	case 0x08:
		return "Mass Storage"
	case 0x09:
		return "Hub"
	case 0x0a:
		return "CDC Data"
	case 0x0b:
		return "Smart Card"
	case 0x0d:
		return "Content Security"
	case 0x0e:
		return "Video"
	case 0x0f:
		return "Personal Healthcare"
	case 0x10:
		return "Audio/Video"
	case 0x11:
		return "Billboard"
	case 0xdc:
		return "Diagnostic"
	case 0xe0:
		return "Wireless Controller"
	case 0xef:
		return "Miscellaneous"
	case 0xfe:
		return "Application Specific"
	case 0xff:
		return "Vendor Specific"
	}
	return ""
}

// ---------- USB devices (/sys/bus/usb/devices) ----------

// listUSBDevices enumerates every non-root-hub USB device directory.
func listUSBDevices() ([]USBDevice, error) {
	entries, err := os.ReadDir(sysUSBDevices)
	if err != nil {
		return nil, err
	}
	devices := make([]USBDevice, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		// Root hubs are named "usb1".."usbN" and carry no port path; skip them.
		if !strings.Contains(name, "-") {
			continue
		}
		if dev := readUSBDevice(name); dev != nil {
			devices = append(devices, *dev)
		}
	}
	return devices, nil
}

// readUSBDevice fills one USBDevice from a device directory like "1-1.2".
func readUSBDevice(devpath string) *USBDevice {
	dir := filepath.Join(sysUSBDevices, devpath)

	vid, vidOK := parseHex(readFile(filepath.Join(dir, "idVendor")))
	pid, pidOK := parseHex(readFile(filepath.Join(dir, "idProduct")))

	dev := &USBDevice{
		Name:         readFile(filepath.Join(dir, "product")),
		Manufacturer: readFile(filepath.Join(dir, "manufacturer")),
		SerialNumber: readFile(filepath.Join(dir, "serial")),
		LocationID:   devpath,
		Bus:          atoi(readFile(filepath.Join(dir, "busnum"))),
		DeviceAddr:   atoi(readFile(filepath.Join(dir, "devnum"))),
		Port:         atoi(readFile(filepath.Join(dir, "port"))),
		Level:        strings.Count(devpath, "-") + strings.Count(devpath, "."),
		Source:       "linux-sysfs",
	}
	if vidOK {
		dev.VendorID = fmt.Sprintf("0x%04x", vid)
	}
	if pidOK {
		dev.ProductID = fmt.Sprintf("0x%04x", pid)
	}

	// sysfs "speed" is in Mb/s ("480", "5000", ...); some kernels expose the
	// symbolic usb_speed_string() names instead ("high", "super", ...).
	if s := readFile(filepath.Join(dir, "speed")); s != "" {
		dev.Speed, dev.SpeedBps = speedFromSysfs(s)
	}

	// bMaxPower: kernel exports the descriptor value in mA (already ×2
	// internally); older kernels export the raw 2-mA-units number — see
	// maxPowerToMA for the dual-path handling.
	if s := readFile(filepath.Join(dir, "bMaxPower")); s != "" {
		dev.NegotiatedCurrent = maxPowerToMA(s)
	}

	if s := readFile(filepath.Join(dir, "bcdDevice")); s != "" {
		if v, ok := parseHex(s); ok {
			dev.BcdDevice = fmt.Sprintf("0x%04x", v)
		}
	}

	// bcdUSB is not exposed as a sysfs text attribute, so recover it from the
	// raw device descriptor (first 18 bytes) when present.  Multi-byte fields
	// are little-endian per the USB 2.0 specification; sysfs stores the raw
	// descriptor bytes verbatim.  The text files above remain the primary
	// (more stable) source for idVendor/idProduct/bcdDevice.
	if raw := readBinary(filepath.Join(dir, "descriptors")); len(raw) >= 18 {
		dBcdUSB, dVID, dPID, dBcdDev := parseDeviceDescriptor(raw)
		if dev.BcdUSB == "" && dBcdUSB != 0 {
			dev.BcdUSB = fmt.Sprintf("0x%04x", dBcdUSB)
		}
		if !vidOK && dVID != 0 {
			dev.VendorID = fmt.Sprintf("0x%04x", dVID)
		}
		if !pidOK && dPID != 0 {
			dev.ProductID = fmt.Sprintf("0x%04x", dPID)
		}
		if dev.BcdDevice == "" && dBcdDev != 0 {
			dev.BcdDevice = fmt.Sprintf("0x%04x", dBcdDev)
		}
	}

	// Hub detection: any device on a port path whose device class is the hub
	// class (0x09) or whose product name mentions "hub".
	if class, ok := parseHex(readFile(filepath.Join(dir, "bDeviceClass"))); ok {
		if usbClassName(class) == "Hub" || strings.Contains(strings.ToLower(dev.Name), "hub") {
			dev.IsHub = true
		}
	}
	return dev
}

// speedFromSysfs converts the sysfs speed attribute into a human label and
// bits-per-second figure.  The attribute is numeric Mb/s on most kernels and
// a symbolic usb_speed_string() name on others; both are handled.
func speedFromSysfs(s string) (string, uint64) {
	s = strings.TrimSpace(s)
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
		bps := uint64(math.Round(f * 1_000_000))
		if f >= 1000 {
			gb := f / 1000
			if gb == math.Floor(gb) {
				return fmt.Sprintf("%.0f Gb/s", gb), bps
			}
			return fmt.Sprintf("%.1f Gb/s", gb), bps
		}
		if f == math.Floor(f) {
			return fmt.Sprintf("%.0f Mb/s", f), bps
		}
		return fmt.Sprintf("%.1f Mb/s", f), bps
	}
	switch s {
	case "low":
		return "1.5 Mb/s", 1_500_000
	case "full":
		return "12 Mb/s", 12_000_000
	case "high":
		return "480 Mb/s", 480_000_000
	case "wireless":
		return "Wireless", 480_000_000
	case "super":
		return "5 Gb/s", 5_000_000_000
	case "super-speed", "super-speed-plus":
		return "10 Gb/s", 10_000_000_000
	case "super-speed-gen2x2", "super-speed-plus-gen2x2":
		return "20 Gb/s", 20_000_000_000
	}
	return "", 0
}

// maxPowerToMA converts the sysfs bMaxPower attribute to mA.
//
// The kernel's `usb_get_max_power()` already multiplies the raw descriptor
// value (which is in 2 mA units) before exporting it through sysfs, so on
// modern kernels the attribute is rendered as e.g. "500mA" with the value
// already in mA.  We detect the "mA" suffix and skip the ×2 in that case;
// if the kernel only emits the bare number (older kernels, or some embedded
// builds) we apply the ×2 ourselves to honour the USB 2.0 descriptor
// semantics.
//
// See: drivers/usb/core/sysfs.c::show_bMaxPower()
//
//	drivers/usb/core/usb.c    ::usb_get_max_power()
func maxPowerToMA(s string) int {
	s = strings.TrimSpace(s)
	hasMA := strings.HasSuffix(s, "mA")
	if hasMA {
		s = strings.TrimSuffix(s, "mA")
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	if hasMA {
		return v
	}
	return v * 2
}

// parseDeviceDescriptor decodes the first 18 bytes of a USB device descriptor.
// Layout (USB 2.0 spec, table 9-8), all multi-byte fields little-endian:
//
//	0  bLength, 1 bDescriptorType(0x01), 2-3 bcdUSB,
//	4  bDeviceClass, 5 bDeviceSubClass, 6 bDeviceProtocol, 7 bMaxPacketSize0,
//	8-9 idVendor, 10-11 idProduct, 12-13 bcdDevice,
//	14 iManufacturer, 15 iProduct, 16 iSerialNumber, 17 bNumConfigurations
func parseDeviceDescriptor(b []byte) (bcdUSB, idVendor, idProduct, bcdDevice uint32) {
	if len(b) < 18 || b[0] < 18 || b[1] != 0x01 {
		return 0, 0, 0, 0
	}
	bcdUSB = uint32(b[2]) | uint32(b[3])<<8
	idVendor = uint32(b[8]) | uint32(b[9])<<8
	idProduct = uint32(b[10]) | uint32(b[11])<<8
	bcdDevice = uint32(b[12]) | uint32(b[13])<<8
	return
}

// ---------- Type-C ports (/sys/class/typec) ----------

// listTypeCPorts enumerates every Type-C port directory ("port0", "port1", ...).
func listTypeCPorts() ([]USBDevice, error) {
	entries, err := os.ReadDir(sysTypeC)
	if err != nil {
		return nil, err
	}
	ports := make([]USBDevice, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		idx, ok := parsePortIndex(name)
		if !ok {
			continue
		}
		ports = append(ports, readTypeCPort(name, idx))
	}
	return ports, nil
}

// parsePortIndex extracts the numeric index from a typec port name ("port3" -> 3).
func parsePortIndex(name string) (int, bool) {
	if !strings.HasPrefix(name, "port") {
		return 0, false
	}
	s := strings.TrimPrefix(name, "port")
	if s == "" {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return v, true
}

// readTypeCPort builds one USBDevice record for a Type-C port and enriches it
// with partner, cable and alt-mode context.  Level 0 is used for port records
// to keep them distinguishable from USB-device records (which are >= 1).
func readTypeCPort(name string, idx int) USBDevice {
	rec := USBDevice{
		Name:              fmt.Sprintf("Type-C Port %d", idx),
		LocationID:        "typec-" + name,
		Port:              idx,
		PortConnectorType: "Type-C",
		Level:             0,
		Source:            "linux-sysfs",
	}
	base := filepath.Join(sysTypeC, name)

	rec.DataRole = readFile(filepath.Join(base, "data_role"))
	rec.PowerRole = readFile(filepath.Join(base, "power_role"))
	rec.PowerOpMode = readFile(filepath.Join(base, "power_operation_mode"))
	rec.Orientation = readFile(filepath.Join(base, "orientation"))
	rec.PDRevision = readFile(filepath.Join(base, "usb_power_delivery_revision"))
	rec.USBCapability = readFile(filepath.Join(base, "usb_capability"))

	// A port running USB Power Delivery (revision > "0.0") indicates a UCSI
	// style PD controller is present.
	if parsePDRev(rec.PDRevision) > 0 {
		rec.UCSIPresent = true
	}

	readTypeCPartner(base, &rec)
	readTypeCCable(base, &rec)
	readTypeCAltmodes(name, &rec)
	return rec
}

// readTypeCPartner reads the device on the other end of the Type-C link from
// the <port>-partner directory.
func readTypeCPartner(base string, rec *USBDevice) {
	dir := base + "-partner"
	if !dirExists(dir) {
		return
	}
	rec.PartnerType = readFile(filepath.Join(dir, "type"))
	if readFile(filepath.Join(dir, "supports_usb_power_delivery")) == "yes" {
		rec.PartnerPD = true
	}
	if rec.PDRevision == "" {
		rec.PDRevision = readFile(filepath.Join(dir, "usb_power_delivery_revision"))
	}

	ident := filepath.Join(dir, "identity")
	// PD identity id_header: bits 15:0 carry the vendor ID.
	if hdr, ok := parseHex(readFile(filepath.Join(ident, "id_header"))); ok && hdr != 0 {
		rec.PartnerVID = fmt.Sprintf("0x%04x", hdr&0xffff)
	}
	// PD identity "product" VDO: bits 31:16 carry the product ID.
	if prod, ok := parseHex(readFile(filepath.Join(ident, "product"))); ok && prod != 0 {
		rec.PartnerPID = fmt.Sprintf("0x%04x", (prod>>16)&0xffff)
	}
}

// readTypeCCable reads the cable E-marker / VDO data from the <port>-cable
// directory.  The Cable VDO bit layout is defined by the USB PD specification
// ("Cable VDO", PD 2.0) and the active-cable VDO (PD 3.0).
func readTypeCCable(base string, rec *USBDevice) {
	dir := base + "-cable"
	if !dirExists(dir) {
		return
	}
	rec.CablePresent = true
	rec.CableType = readFile(filepath.Join(dir, "type"))
	rec.CablePlugType = readFile(filepath.Join(dir, "plug_type"))
	if rec.PDRevision == "" {
		rec.PDRevision = readFile(filepath.Join(dir, "usb_power_delivery_revision"))
	}

	// A second plug device (SOP'' plug) means the cable carries an extra
	// SOP'' controller, i.e. it is a PD 3.0 active / retimer cable.
	if dirExists(base + "-plug1") {
		rec.CableSOPPPrime = true
	}

	ident := filepath.Join(dir, "identity")
	if hdr, ok := parseHex(readFile(filepath.Join(ident, "id_header"))); ok && hdr != 0 {
		rec.CableVendorID = fmt.Sprintf("0x%04x", hdr&0xffff)
	}
	if prod, ok := parseHex(readFile(filepath.Join(ident, "product"))); ok && prod != 0 {
		rec.CableProductID = fmt.Sprintf("0x%04x", (prod>>16)&0xffff)
	}

	// product_type_vdo1 = Cable VDO (USB PD 2.0).  Bit fields:
	//   bits 2:0   cable speed capability
	//   bit  3     SOP'' controller present (active cables)
	//   bits 6:5   current capability (1A / 3A / 5A / reserved)
	//   bits 10:9  VBUS through capability (0V / 5V / 12V / 20V)
	// An all-zero identity (0x00000000) means PD communication has not
	// completed; it is treated as "no data" rather than as a real cable.
	if vdo1, ok := parseHex(readFile(filepath.Join(ident, "product_type_vdo1"))); ok && vdo1 != 0 {
		rec.CableMaxSpeed = speedFromBits(vdo1 & 0x7)
		rec.CableCurrent = currentFromBits((vdo1 >> 5) & 0x3)
		rec.CableVBUSThrough = vbusFromBits((vdo1 >> 9) & 0x3)
		if vdo1&(1<<3) != 0 {
			rec.CableSOPPPrime = true
		}
	}

	// product_type_vdo2 = Active Cable VDO (USB PD 3.0).  Bit fields:
	//   bit 8  USB4 support
	//   bit 4  USB 3.2 support
	// Only present / meaningful for active cables.
	if vdo2, ok := parseHex(readFile(filepath.Join(ident, "product_type_vdo2"))); ok && vdo2 != 0 {
		if vdo2&(1<<8) != 0 {
			rec.CableUSB4 = true
		}
		if vdo2&(1<<4) != 0 {
			rec.CableUSB32 = true
		}
	}
}

// speedFromBits maps the Cable VDO bits 2:0 to a speed label (USB PD spec).
func speedFromBits(v uint32) string {
	switch v & 0x7 {
	case 0b000:
		return "USB 2.0 (480 Mb/s)"
	case 0b001:
		return "USB 3.2 Gen1 (5 Gb/s)"
	case 0b010:
		return "USB 3.2 Gen2 (10 Gb/s)"
	case 0b011:
		return "USB4 Gen3 (40 Gb/s)"
	case 0b100:
		return "USB4 Gen4 (80 Gb/s)"
	default:
		return fmt.Sprintf("0b%03b", v&0x7)
	}
}

// currentFromBits maps the Cable VDO bits 6:5 to a current rating.
func currentFromBits(v uint32) string {
	switch v & 0x3 {
	case 0b00:
		return "1A"
	case 0b01:
		return "3A"
	case 0b10:
		return "5A"
	default:
		return "reserved"
	}
}

// vbusFromBits maps the Cable VDO bits 10:9 to the VBUS through rating.
func vbusFromBits(v uint32) string {
	switch v & 0x3 {
	case 0b00:
		return "0V"
	case 0b01:
		return "5V"
	case 0b10:
		return "12V"
	default:
		return "20V"
	}
}

// readTypeCAltmodes enumerates the alternate modes belonging to this port from
// /sys/bus/typec/devices.  Alt-mode devices are named "<port>-partner.N",
// "<port>-cable.N" and "<port>-plugM.N" where N is the mode index.
func readTypeCAltmodes(portName string, rec *USBDevice) {
	entries, err := os.ReadDir(sysTypeCBus)
	if err != nil {
		return
	}
	seen := make(map[string]bool)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, portName+"-") || !strings.Contains(name, ".") {
			continue
		}
		dir := filepath.Join(sysTypeCBus, name)
		svidText := readFile(filepath.Join(dir, "svid"))
		svid, ok := parseHex(svidText)
		if !ok {
			continue
		}
		alt := Altmode{
			SVID:   fmt.Sprintf("0x%04x", svid),
			Mode:   parseIndex(readFile(filepath.Join(dir, "mode"))),
			Active: readFile(filepath.Join(dir, "active")) == "yes",
		}
		if v := readFile(filepath.Join(dir, "vdo")); v != "" {
			if vv, ok := parseHex(v); ok {
				alt.VDO = fmt.Sprintf("0x%08x", vv)
			} else {
				alt.VDO = v
			}
		}
		switch svid {
		case 0x8087:
			alt.Description = "Thunderbolt 3"
			rec.CableTBT3 = true
		case 0xff01:
			alt.Description = "DisplayPort"
		case 0x00b0:
			alt.Description = "USB Billboard"
		default:
			alt.Description = fmt.Sprintf("SVID 0x%s", strings.ToLower(svidText))
		}
		key := fmt.Sprintf("%04x:%d", svid, alt.Mode)
		if !seen[key] {
			seen[key] = true
			rec.Altmodes = append(rec.Altmodes, alt)
		}
	}
}

// TestLinuxBackendSmoke is a smoke test for the sysfs reader.  It is only
// compiled on Linux (//go:build linux); the Skip additionally protects hosts
// without a readable sysfs (containers, sandboxes, USB-less machines).
func TestLinuxBackendSmoke(t *testing.T) {
	b := &LinuxBackend{}
	devs, err := b.ListDevices()
	if err != nil {
		t.Skipf("linux sysfs unavailable: %v", err)
	}
	t.Logf("enumerated %d device records", len(devs))
	for i, d := range devs {
		t.Logf("  [%02d] loc=%s level=%d name=%q vid=%s pid=%s hub=%v cable=%v",
			i, d.LocationID, d.Level, d.Name, d.VendorID, d.ProductID, d.IsHub, d.CablePresent)
	}
}
