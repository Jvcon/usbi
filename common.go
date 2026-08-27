// Package usbi provides a cross-platform USB device information backend.
//
// Each platform supplies its own implementation of USBBackend that reads the
// appropriate kernel/driver surface (sysfs on Linux, IOKit/system_profiler on
// macOS, hub IOCTLs on Windows) and returns a slice of USBDevice with as much
// aligned information as the platform can expose.
//
// All fields use `omitempty` so that the JSON output is sparse and platforms
// only emit what they were actually able to read.  Fields the platform knows
// about but could not read (because of missing kernel support, firmware bugs,
// or missing drivers) are recorded in the per-device `Unavailable` slice
// rather than silently elided.
package usbi

// USBDevice is the unified, cross-platform USB / Type-C device description.
//
// Field semantics are chosen so the same JSON key means the same thing
// regardless of which platform produced it.  Where a platform cannot fill a
// field, the key is omitted entirely unless the platform actively knows the
// answer is "not available", in which case the field name is added to
// Unavailable.
type USBDevice struct {
	// ---------- Basic USB (universal) ----------
	Name         string `json:"name,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	VendorID     string `json:"vendor_id,omitempty"`  // "0x1234"
	ProductID    string `json:"product_id,omitempty"` // "0xabcd"
	BcdUSB       string `json:"bcd_usb,omitempty"`    // "0x0320" etc.
	BcdDevice    string `json:"bcd_device,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`

	// ---------- Speed / Power ----------
	Speed             string `json:"speed,omitempty"`                 // "5 Gb/s", "480 Mb/s"
	SpeedBps          uint64 `json:"speed_bps,omitempty"`             // raw bits per second
	NegotiatedCurrent int    `json:"negotiated_current_ma,omitempty"` // mA

	// ---------- Topology / Location ----------
	LocationID string `json:"location_id,omitempty"`
	Port       int    `json:"port,omitempty"`
	Bus        int    `json:"bus,omitempty"`
	DeviceAddr int    `json:"device_address,omitempty"`
	IsHub      bool   `json:"is_hub,omitempty"`
	IsBus      bool   `json:"is_bus,omitempty"`
	Level      int    `json:"level,omitempty"`

	// ---------- Type-C port context ----------
	PortConnectorType string `json:"port_connector_type,omitempty"` // "Type-A" / "Type-B" / "Type-C"
	DataRole          string `json:"data_role,omitempty"`           // "host" / "device"
	PowerRole         string `json:"power_role,omitempty"`          // "source" / "sink"
	PowerOpMode       string `json:"power_op_mode,omitempty"`       // "default" / "1.5A" / "3.0A" / "usb_power_delivery"
	Orientation       string `json:"orientation,omitempty"`         // "normal" / "reverse" / "unknown"
	PDRevision        string `json:"pd_revision,omitempty"`         // "2.0" / "3.0" / "3.1"
	USBCapability     string `json:"usb_capability,omitempty"`      // "usb2" / "usb3" / "usb4"

	// ---------- Partner (device on the other end) ----------
	PartnerType string `json:"partner_type,omitempty"` // "hub" / "peripheral" / "host" / ...
	PartnerVID  string `json:"partner_vendor_id,omitempty"`
	PartnerPID  string `json:"partner_product_id,omitempty"`
	PartnerPD   bool   `json:"partner_supports_pd,omitempty"`

	// ---------- Cable / E-marker ----------
	CablePresent     bool   `json:"cable_present,omitempty"`
	CableType        string `json:"cable_type,omitempty"`      // "active" / "passive"
	CablePlugType    string `json:"cable_plug_type,omitempty"` // "type-c" / "captive" / ...
	CableVendorID    string `json:"cable_vendor_id,omitempty"`
	CableProductID   string `json:"cable_product_id,omitempty"`
	CableMaxSpeed    string `json:"cable_max_speed,omitempty"`    // "USB4 Gen4 (80 Gb/s)" / "USB 3.2 Gen2 (10 Gb/s)" / ...
	CableCurrent     string `json:"cable_current,omitempty"`      // "5A" / "3A" / "1A"
	CableVBUSThrough string `json:"cable_vbus_through,omitempty"` // "20V" / "12V" / "5V" / "0V"
	CableUSB4        bool   `json:"cable_usb4,omitempty"`
	CableUSB32       bool   `json:"cable_usb32,omitempty"`
	CableTBT3        bool   `json:"cable_tbt3,omitempty"`
	CableSOPPPrime   bool   `json:"cable_sop_pp_controller,omitempty"`

	// ---------- Alternate Modes (DP, TBT3, ...) ----------
	Altmodes []Altmode `json:"altmodes,omitempty"`

	// ---------- UCSI presence / source identification ----------
	UCSIPresent bool   `json:"ucsi_present,omitempty"`
	Source      string `json:"source,omitempty"` // "linux-sysfs" / "windows-syscall" / "macos-ioreg"

	// ---------- Field-level unavailable annotations ----------
	Unavailable []string `json:"unavailable,omitempty"`
}

// Altmode describes one USB Type-C Alternate Mode entry (DP, TBT3, ...).
type Altmode struct {
	SVID        string `json:"svid,omitempty"` // "0xff01" = DP, "0x8087" = TBT3
	Mode        int    `json:"mode,omitempty"`
	Active      bool   `json:"active,omitempty"`
	VDO         string `json:"vdo,omitempty"`         // raw hex "0x..."
	Description string `json:"description,omitempty"` // "DisplayPort" / "TBT3" / ...
}

// USBBackend is implemented by every platform-specific collector.
type USBBackend interface {
	ListDevices() ([]USBDevice, error)
}
