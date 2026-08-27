//go:build windows

// Windows backend for usbi — pure Go, zero CGo.
//
// Why pure Go (no CGo):
//
//  1. Cross-compilation. With CGo, building for Windows from Linux CI needs
//     a full MinGW/MSVC toolchain (SDK headers, import libraries, linker).
//     A pure-Go implementation only needs the Go toolchain, so
//     `GOOS=windows CGO_ENABLED=0 go build` works anywhere.
//  2. Deployment. CGo binaries drag a runtime dependency on MSVCRT and the
//     target machine's import libs. A pure-Go binary is a single static
//     artifact that runs on stock Windows.
//  3. Stability. SetupAPI and usbioctl are a stable C ABI; every call we
//     need is either already wrapped by golang.org/x/sys/windows or is
//     invoked with syscall.SyscallN using the exact documented argument
//     order (see SetupDiGetDevicePropertyW below).
//
// Enumeration strategy (two passes):
//
//	Pass 1 — SetupAPI, no handles. Enumerate GUID_DEVINTERFACE_USB_DEVICE
//	device-interface elements and read DEVPKEY_Device_HardwareIds /
//	FriendlyName / Manufacturer. This yields VID/PID/name for every present
//	USB device without opening a single handle (the fast first round).
//
//	Pass 2 — Hub IOCTLs. Enumerate every USB hub interface
//	(GUID_DEVINTERFACE_USB_HUB) with CfgMgr32, open each hub, then per port
//	issue IOCTL_USB_GET_NODE_CONNECTION_INFORMATION_EX for VID/PID/speed/
//	address/hub-ness, refined by the V2 and PORT_CONNECTOR_PROPERTIES
//	IOCTLs (SuperSpeed / Type-C detection) and descriptor reads
//	(iProduct / iManufacturer / iSerialNumber / bMaxPower).
//
// Every IOCTL failure degrades gracefully: the field is left empty and the
// next port/hub is tried. ListDevices always returns what it collected and
// only a total enumeration collapse would surface an error.
//
// Note on binary layout: the usbioctl.h structures are declared inside a
// pshpack1 region, i.e. packed with 1-byte alignment. Go's natural struct
// alignment therefore does NOT match the kernel ABI for the fields that land
// on odd offsets (DeviceAddress/NumberOfOpenPipes/ConnectionStatus in
// USB_NODE_CONNECTION_INFORMATION_EX). Those are read at their exact byte
// offsets with encoding/binary below.
package usbi

import (
	"encoding/binary"
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	// IOCTL codes are CTL_CODE(FILE_DEVICE_USB(0x22), fn, METHOD_BUFFERED(0),
	// FILE_ANY_ACCESS(0)) = (0x22<<16) | (fn<<2), with the "EX" function
	// numbers used by modern usbioctl.h:
	//   USB_GET_DESCRIPTOR_FROM_NODE_CONNECTION        fn 260
	//   USB_GET_NODE_CONNECTION_INFORMATION_EX         fn 274
	//   USB_GET_HUB_INFORMATION_EX                     fn 277
	//   USB_GET_PORT_CONNECTOR_PROPERTIES              fn 278
	//   USB_GET_NODE_CONNECTION_INFORMATION_EX_V2      fn 279
	ioctlUsbGetDescriptorFromNodeConnection  = 0x220410
	ioctlUsbGetNodeConnectionInformationEx   = 0x220448
	ioctlUsbGetHubInformationEx              = 0x220454
	ioctlUsbGetPortConnectorProperties       = 0x220458
	ioctlUsbGetNodeConnectionInformationExV2 = 0x22045C

	// usbPortPropIsUsbC is USB_PORT_PROP_IS_USB_C from usbioctl.h (bit 11).
	// (The "1<<3" in some notes is a typo for 1<<11.)
	usbPortPropIsUsbC = 0x00000800

	// genericWrite is GENERIC_WRITE, needed to open a hub handle for IOCTLs.
	genericWrite = 0x40000000

	// usbConnectionStatusDeviceConnected = USB_CONNECTION_STATUS.DeviceConnected.
	usbConnectionStatusDeviceConnected = 1

	// nodeConnInfoExFixedLen is the packed length of the fixed header of
	// USB_NODE_CONNECTION_INFORMATION_EX (35 bytes, before the pipe list).
	nodeConnInfoExFixedLen = 35

	// maxPortBufLen is a generous fixed buffer for per-port IOCTLs.
	maxPortBufLen = 1024
)

// ---------------------------------------------------------------------------
// GUIDs and device property keys
// ---------------------------------------------------------------------------

var (
	guidDevInterfaceUSBDevice = windows.GUID{
		Data1: 0xA5DCBF10, Data2: 0x6530, Data3: 0x11D2,
		Data4: [8]byte{0x90, 0x1F, 0x00, 0xC0, 0x4F, 0xB9, 0x51, 0xED},
	}
	guidDevInterfaceUSBHub = windows.GUID{
		Data1: 0xF18A0E88, Data2: 0xC30C, Data3: 0x11D0,
		Data4: [8]byte{0x88, 0x15, 0x00, 0xA0, 0xC9, 0x06, 0xBE, 0xD8},
	}

	// DEVPKEY_Device_* share the PKEY_Device fmtid {A45C254E-DF1C-4EFD-8020-67D146A850E0}.
	devpkeyDeviceFmtid = windows.DEVPROPGUID{
		Data1: 0xA45C254E, Data2: 0xDF1C, Data3: 0x4EFD,
		Data4: [8]byte{0x80, 0x20, 0x67, 0xD1, 0x46, 0xA8, 0x50, 0xE0},
	}
	devpkeyHardwareIds  = windows.DEVPROPKEY{FmtID: devpkeyDeviceFmtid, PID: 3}
	devpkeyManufacturer = windows.DEVPROPKEY{FmtID: devpkeyDeviceFmtid, PID: 13}
	devpkeyFriendlyName = windows.DEVPROPKEY{FmtID: devpkeyDeviceFmtid, PID: 14}

	// procSetupDiGetDevicePropertyW is loaded manually because the
	// x/sys/windows wrapper only decodes DEVPROP_TYPE_STRING, not the
	// DEVPROP_TYPE_STRING_LIST that DEVPKEY_Device_HardwareIds returns.
	// This is the "API not wrapped by x/sys/windows" we call via SyscallN.
	procSetupDiGetDevicePropertyW = windows.NewLazySystemDLL("setupapi.dll").NewProc("SetupDiGetDevicePropertyW")
)

// ---------------------------------------------------------------------------
// Binary ABI structures (usbioctl.h / usbspec.h)
// ---------------------------------------------------------------------------

// USB_DEVICE_DESCRIPTOR mirrors the packed USB device descriptor. Go's natural
// layout coincidentally matches the packed C layout (all uint16 on even offsets).
type USB_DEVICE_DESCRIPTOR struct {
	BLength            uint8
	BDescriptorType    uint8
	BcdUSB             uint16
	BDeviceClass       uint8
	BDeviceSubClass    uint8
	BDeviceProtocol    uint8
	BMaxPacketSize0    uint8
	IDVendor           uint16
	IDProduct          uint16
	BcdDevice          uint16
	IManufacturer      uint8
	IProduct           uint8
	ISerialNumber      uint8
	BNumConfigurations uint8
}

// USB_NODE_CONNECTION_INFORMATION_EX is a container for the per-port data
// returned by IOCTL_USB_GET_NODE_CONNECTION_INFORMATION_EX. The kernel ABI is
// packed (pack(1)); the trailing fields (DeviceAddress, NumberOfOpenPipes,
// ConnectionStatus) therefore land on odd offsets and are populated by
// ioctlNodeConnInfoEx at their exact byte offsets.
type USB_NODE_CONNECTION_INFORMATION_EX struct {
	ConnectionIndex           uint32
	DeviceDescriptor          USB_DEVICE_DESCRIPTOR
	CurrentConfigurationValue uint8
	Speed                     uint8
	DeviceIsHub               uint8
	DeviceAddress             uint16
	NumberOfOpenPipes         uint32
	ConnectionStatus          uint32
}

// USB_HUB_INFORMATION_EX is the (packed) fixed part of the hub-info response.
// HubType is at offset 0, HighestPortNumber at offset 4.
type USB_HUB_INFORMATION_EX struct {
	HubType           uint32
	HighestPortNumber uint16
}

// USB_SETUP_PACKET is the setup-packet portion of USB_DESCRIPTOR_REQUEST. Its
// fields happen to be naturally aligned (wValue/wIndex/wLength on even
// offsets), so it can be overlaid directly at buf[4:12].
type USB_SETUP_PACKET struct {
	BmRequestType uint8
	BRequest      uint8
	WValue        uint16
	WIndex        uint16
	WLength       uint16
}

// ---------------------------------------------------------------------------
// Backend
// ---------------------------------------------------------------------------

// WinBackend implements USBBackend for Windows.
type WinBackend struct{}

// Compile-time assertion that the backend satisfies the interface.
var _ USBBackend = (*WinBackend)(nil)

// NewBackend wires the Windows backend into the CLI (see cmd/usbi/main.go).
func NewBackend() (USBBackend, string) {
	return &WinBackend{}, "windows-syscall"
}

// ListDevices returns the Windows USB / Type-C device inventory.
func (b *WinBackend) ListDevices() ([]USBDevice, error) {
	// Pass 1: SetupAPI — fast, handle-free VID/PID/name for every device.
	devices := enumSetupAPIDevices()

	// Lightweight UCSI presence probe (ACPI\UCSIA in the device tree).
	ucsi := enumUCSIDevices()
	for i := range devices {
		d := &devices[i]
		d.UCSIPresent = ucsi
		d.Source = "windows-syscall"
		if ucsi {
			d.Unavailable = append(d.Unavailable, "e_marker_not_exposed_by_windows_kernel")
		}
	}

	// Index pass-1 devices by vendor:product so the hub pass can enrich them.
	byID := map[string][]int{}
	for i := range devices {
		if devices[i].VendorID != "" && devices[i].ProductID != "" {
			key := usbKey(devices[i].VendorID, devices[i].ProductID)
			byID[key] = append(byID[key], i)
		}
	}
	used := map[int]bool{}

	// Pass 2: hub IOCTLs — add speed, port, address, hub-ness, Type-C.
	if paths, err := windows.CM_Get_Device_Interface_List("", &guidDevInterfaceUSBHub, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT); err == nil {
		for _, p := range paths {
			if extra := b.scanHub(p, devices, byID, used); len(extra) > 0 {
				for i := range extra {
					extra[i].UCSIPresent = ucsi
					extra[i].Source = "windows-syscall"
					if ucsi {
						extra[i].Unavailable = append(extra[i].Unavailable, "e_marker_not_exposed_by_windows_kernel")
					}
					devices = append(devices, extra[i])
				}
			}
		}
	}

	// Partial failures are normal; only a total collapse would error out.
	return devices, nil
}

// scanHub opens one hub interface and enriches the pass-1 device list with
// per-port data. Devices not seen by pass 1 are returned as extras.
func (b *WinBackend) scanHub(path string, devices []USBDevice, byID map[string][]int, used map[int]bool) []USBDevice {
	var extra []USBDevice

	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return extra
	}
	handle, err := windows.CreateFile(pathPtr, genericWrite,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		// e.g. ERROR_ACCESS_DENIED on a protected/root hub → skip this hub.
		return extra
	}
	defer windows.CloseHandle(handle)
	h := syscall.Handle(handle)

	highest, err := ioctlHubInfoEx(h)
	if err != nil {
		return extra
	}

	for port := uint32(1); port <= uint32(highest); port++ {
		info, err := ioctlNodeConnInfoEx(h, port)
		if err != nil {
			continue
		}
		if info.ConnectionStatus != usbConnectionStatusDeviceConnected {
			continue
		}

		d := USBDevice{
			VendorID:   fmt.Sprintf("0x%04x", info.DeviceDescriptor.IDVendor),
			ProductID:  fmt.Sprintf("0x%04x", info.DeviceDescriptor.IDProduct),
			BcdUSB:     fmt.Sprintf("0x%04X", info.DeviceDescriptor.BcdUSB),
			BcdDevice:  fmt.Sprintf("0x%04X", info.DeviceDescriptor.BcdDevice),
			Speed:      speedToString(info.Speed),
			DeviceAddr: int(info.DeviceAddress),
			IsHub:      info.DeviceIsHub != 0,
			Port:       int(port),
		}
		d.SpeedBps = speedToBps(d.Speed)

		// V2 refinement: the OS may report a High-speed device that actually
		// negotiates SuperSpeed / SuperSpeedPlus.
		if flags, err := ioctlNodeConnInfoExV2(h, port); err == nil {
			if info.Speed == 3 && flags&0x1 != 0 {
				d.Speed = "5 Gb/s"
			}
			if flags&0x4 != 0 {
				d.Speed = "10 Gb/s"
			}
			d.SpeedBps = speedToBps(d.Speed)
		}

		// Type-C connector detection.
		if props, err := ioctlPortConnectorProps(h, port); err == nil {
			if props&usbPortPropIsUsbC != 0 {
				d.PortConnectorType = "Type-C"
			}
		}

		// String descriptors (optional; empty on failure).
		d.Manufacturer = ioctlStringDescriptor(h, port, info.DeviceDescriptor.IManufacturer)
		d.Name = ioctlStringDescriptor(h, port, info.DeviceDescriptor.IProduct)
		d.SerialNumber = ioctlStringDescriptor(h, port, info.DeviceDescriptor.ISerialNumber)

		// bMaxPower (config descriptor) → negotiated current in mA.
		d.NegotiatedCurrent = ioctlConfigMaxPowerMA(h, port)

		// Merge into a pass-1 device (first unused match) or keep as extra.
		idx := -1
		for _, i := range byID[usbKey(d.VendorID, d.ProductID)] {
			if !used[i] {
				idx = i
				break
			}
		}
		if idx >= 0 {
			used[idx] = true
			mergeDevice(&devices[idx], &d)
		} else if d.VendorID != "" && d.VendorID != "0x0000" {
			extra = append(extra, d)
		}
	}
	return extra
}

// mergeDevice folds richer hub data into a pass-1 (PnP) device, filling gaps.
func mergeDevice(dst *USBDevice, src *USBDevice) {
	if src.Speed != "" && src.Speed != "Unknown" {
		dst.Speed = src.Speed
		dst.SpeedBps = src.SpeedBps
	}
	if src.Port != 0 {
		dst.Port = src.Port
	}
	if src.DeviceAddr != 0 {
		dst.DeviceAddr = src.DeviceAddr
	}
	if src.IsHub {
		dst.IsHub = true
	}
	if src.PortConnectorType != "" {
		dst.PortConnectorType = src.PortConnectorType
	}
	if src.BcdUSB != "" {
		dst.BcdUSB = src.BcdUSB
	}
	if src.BcdDevice != "" {
		dst.BcdDevice = src.BcdDevice
	}
	if src.NegotiatedCurrent != 0 {
		dst.NegotiatedCurrent = src.NegotiatedCurrent
	}
	if src.Name != "" {
		dst.Name = src.Name
	}
	if src.Manufacturer != "" {
		dst.Manufacturer = src.Manufacturer
	}
	if src.SerialNumber != "" {
		dst.SerialNumber = src.SerialNumber
	}
	if dst.VendorID == "" {
		dst.VendorID = src.VendorID
	}
	if dst.ProductID == "" {
		dst.ProductID = src.ProductID
	}
	dst.Source = "windows-syscall"
}

// ---------------------------------------------------------------------------
// Pass 1 — SetupAPI enumeration
// ---------------------------------------------------------------------------

// enumSetupAPIDevices enumerates USB device-interface elements and reads
// VID/PID/name via DEVPKEY_* (no handles opened).
func enumSetupAPIDevices() []USBDevice {
	var devices []USBDevice

	devInfo, err := windows.SetupDiGetClassDevsEx(&guidDevInterfaceUSBDevice, "", 0,
		windows.DIGCF_PRESENT|windows.DIGCF_DEVICEINTERFACE, 0, "")
	if err != nil {
		return devices
	}
	defer devInfo.Close()

	for i := 0; ; i++ {
		data, err := windows.SetupDiEnumDeviceInfo(devInfo, i)
		if err != nil {
			break // ERROR_NO_MORE_ITEMS terminates the loop
		}

		dev := USBDevice{Source: "windows-syscall"}

		if v, err := devicePropertyString(devInfo, data, &devpkeyHardwareIds); err == nil {
			dev.VendorID, dev.ProductID = parseVIDPIDFromHWID(v)
		}
		// Registry fallback in case the DEVPKEY path is unavailable.
		if dev.VendorID == "" || dev.ProductID == "" {
			if v, err := windows.SetupDiGetDeviceRegistryProperty(devInfo, data, windows.SPDRP_HARDWAREID); err == nil {
				if list, ok := v.([]string); ok && len(list) > 0 {
					vid, pid := parseVIDPIDFromHWID(list[0])
					if dev.VendorID == "" {
						dev.VendorID = vid
					}
					if dev.ProductID == "" {
						dev.ProductID = pid
					}
				}
			}
		}

		if v, err := devicePropertyString(devInfo, data, &devpkeyFriendlyName); err == nil && v != "" {
			dev.Name = v
		} else if v, err := windows.SetupDiGetDeviceRegistryProperty(devInfo, data, windows.SPDRP_FRIENDLYNAME); err == nil {
			if s, ok := v.(string); ok {
				dev.Name = s
			}
		}

		if v, err := devicePropertyString(devInfo, data, &devpkeyManufacturer); err == nil && v != "" {
			dev.Manufacturer = v
		} else if v, err := windows.SetupDiGetDeviceRegistryProperty(devInfo, data, windows.SPDRP_MFG); err == nil {
			if s, ok := v.(string); ok {
				dev.Manufacturer = s
			}
		}

		devices = append(devices, dev)
	}
	return devices
}

// devicePropertyString reads a DEVPKEY via SetupDiGetDevicePropertyW using
// syscall.SyscallN (the x/sys wrapper cannot decode STRING_LIST properties).
// Returns the first non-empty string of the value.
func devicePropertyString(devInfoSet windows.DevInfo, devInfoData *windows.DevInfoData, key *windows.DEVPROPKEY) (string, error) {
	var propType uint32
	reqSize := uint32(512)
	for attempts := 0; attempts < 8; attempts++ {
		buf := make([]byte, reqSize)
		r1, _, e1 := syscall.SyscallN(procSetupDiGetDevicePropertyW.Addr(),
			uintptr(devInfoSet),
			uintptr(unsafe.Pointer(devInfoData)),
			uintptr(unsafe.Pointer(key)),
			uintptr(unsafe.Pointer(&propType)),
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(uint32(len(buf))),
			uintptr(unsafe.Pointer(&reqSize)),
			0)
		if r1 == 0 {
			if e1 == windows.ERROR_INSUFFICIENT_BUFFER && reqSize > uint32(len(buf)) {
				continue
			}
			return "", e1
		}
		if reqSize > uint32(len(buf)) {
			reqSize = uint32(len(buf))
		}
		switch windows.DEVPROPTYPE(propType) {
		case windows.DEVPROP_TYPE_STRING:
			return windows.UTF16ToString(bytesToUTF16(buf[:reqSize])), nil
		case windows.DEVPROP_TYPE_STRING_LIST:
			for _, s := range multiSzStrings(bytesToUTF16(buf[:reqSize])) {
				if s != "" {
					return s, nil
				}
			}
			return "", nil
		default:
			return "", nil
		}
	}
	return "", syscall.EINVAL
}

// enumUCSIDevices returns true if any present device node has a hardware ID
// containing "UCSIA" (ACPI\UCSIA USB Connector Manager).
func enumUCSIDevices() bool {
	devInfo, err := windows.SetupDiGetClassDevsEx(nil, "", 0,
		windows.DIGCF_PRESENT|windows.DIGCF_ALLCLASSES, 0, "")
	if err != nil {
		return false
	}
	defer devInfo.Close()

	for i := 0; ; i++ {
		data, err := windows.SetupDiEnumDeviceInfo(devInfo, i)
		if err != nil {
			break
		}
		v, err := devicePropertyString(devInfo, data, &devpkeyHardwareIds)
		if err == nil && strings.Contains(strings.ToUpper(v), "UCSIA") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Pass 2 — hub IOCTLs
// ---------------------------------------------------------------------------

// ioctlHubInfoEx returns the hub's highest port number.
func ioctlHubInfoEx(handle syscall.Handle) (uint16, error) {
	buf := make([]byte, 64)
	var bytesReturned uint32
	err := syscall.DeviceIoControl(handle, ioctlUsbGetHubInformationEx,
		nil, 0, &buf[0], uint32(len(buf)), &bytesReturned, nil)
	if err != nil {
		return 0, err
	}
	// USB_HUB_INFORMATION_EX: HubType @0, HighestPortNumber @4.
	return binary.LittleEndian.Uint16(buf[4:6]), nil
}

// ioctlNodeConnInfoEx returns per-port connection information.
func ioctlNodeConnInfoEx(handle syscall.Handle, port uint32) (*USB_NODE_CONNECTION_INFORMATION_EX, error) {
	buf := make([]byte, maxPortBufLen)
	binary.LittleEndian.PutUint32(buf[0:4], port)
	var bytesReturned uint32
	err := syscall.DeviceIoControl(handle, ioctlUsbGetNodeConnectionInformationEx,
		&buf[0], uint32(len(buf)), &buf[0], uint32(len(buf)), &bytesReturned, nil)
	if err != nil {
		return nil, err
	}
	if bytesReturned < nodeConnInfoExFixedLen {
		return nil, syscall.EINVAL
	}
	// The kernel ABI is packed: the fixed header is 35 bytes with
	// DeviceAddress@25, NumberOfOpenPipes@27, ConnectionStatus@31.
	info := &USB_NODE_CONNECTION_INFORMATION_EX{
		ConnectionIndex:           binary.LittleEndian.Uint32(buf[0:4]),
		DeviceDescriptor:          *(*USB_DEVICE_DESCRIPTOR)(unsafe.Pointer(&buf[4])),
		CurrentConfigurationValue: buf[22],
		Speed:                     buf[23],
		DeviceIsHub:               buf[24],
		DeviceAddress:             binary.LittleEndian.Uint16(buf[25:27]),
		NumberOfOpenPipes:         binary.LittleEndian.Uint32(buf[27:31]),
		ConnectionStatus:          binary.LittleEndian.Uint32(buf[31:35]),
	}
	return info, nil
}

// ioctlNodeConnInfoExV2 returns the V2 Flags (SuperSpeed capability bits).
func ioctlNodeConnInfoExV2(handle syscall.Handle, port uint32) (uint32, error) {
	req := make([]byte, 16)
	binary.LittleEndian.PutUint32(req[0:4], port) // ConnectionIndex
	binary.LittleEndian.PutUint32(req[4:8], 16)   // Length = sizeof(struct)
	var bytesReturned uint32
	err := syscall.DeviceIoControl(handle, ioctlUsbGetNodeConnectionInformationExV2,
		&req[0], uint32(len(req)), &req[0], uint32(len(req)), &bytesReturned, nil)
	if err != nil {
		return 0, err
	}
	// Flags is the last ULONG of the 16-byte struct.
	return binary.LittleEndian.Uint32(req[12:16]), nil
}

// ioctlPortConnectorProps returns the UsbPortProperties bitmask.
func ioctlPortConnectorProps(handle syscall.Handle, port uint32) (uint32, error) {
	buf := make([]byte, maxPortBufLen)
	binary.LittleEndian.PutUint32(buf[0:4], port)
	var bytesReturned uint32
	err := syscall.DeviceIoControl(handle, ioctlUsbGetPortConnectorProperties,
		&buf[0], uint32(len(buf)), &buf[0], uint32(len(buf)), &bytesReturned, nil)
	if err != nil {
		return 0, err
	}
	if bytesReturned < 12 {
		return 0, syscall.EINVAL
	}
	// USB_PORT_CONNECTOR_PROPERTIES: UsbPortProperties is the ULONG at offset 8.
	return binary.LittleEndian.Uint32(buf[8:12]), nil
}

// ioctlStringDescriptor reads a string descriptor (e.g. iProduct) over the hub
// handle. Returns "" on any failure or when the index is 0.
func ioctlStringDescriptor(handle syscall.Handle, port uint32, index uint8) string {
	if index == 0 {
		return ""
	}
	buf := make([]byte, 512)
	binary.LittleEndian.PutUint32(buf[0:4], port) // ConnectionIndex
	sp := (*USB_SETUP_PACKET)(unsafe.Pointer(&buf[4]))
	sp.BmRequestType = 0x80                  // device → host, standard, device
	sp.BRequest = 0x06                       // GET_DESCRIPTOR
	sp.WValue = uint16(3)<<8 | uint16(index) // 3 = string descriptor type
	sp.WIndex = 0
	sp.WLength = 256
	var bytesReturned uint32
	err := syscall.DeviceIoControl(handle, ioctlUsbGetDescriptorFromNodeConnection,
		&buf[0], uint32(len(buf)), &buf[0], uint32(len(buf)), &bytesReturned, nil)
	if err != nil {
		return ""
	}
	// Data (descriptor) starts at offset 12: bLength@12, bDescriptorType@13,
	// UTF-16LE string bytes @14.
	if bytesReturned < 14 {
		return ""
	}
	n := int(bytesReturned)
	bLen := int(buf[12])
	if bLen < 2 {
		return ""
	}
	if bLen > n-12 {
		bLen = n - 12
	}
	u := bytesToUTF16(buf[14 : 14+bLen-2])
	return strings.TrimRight(windows.UTF16ToString(u), "\x00")
}

// ioctlConfigMaxPowerMA reads bMaxPower of the configuration descriptor and
// returns it in mA (bMaxPower is in 2 mA units).
func ioctlConfigMaxPowerMA(handle syscall.Handle, port uint32) int {
	buf := make([]byte, 12+0x1000) // wLength = 0x1000
	binary.LittleEndian.PutUint32(buf[0:4], port)
	sp := (*USB_SETUP_PACKET)(unsafe.Pointer(&buf[4]))
	sp.BmRequestType = 0x80
	sp.BRequest = 0x06
	sp.WValue = 0x0200 // configuration descriptor, index 0
	sp.WIndex = 0
	sp.WLength = 0x1000
	var bytesReturned uint32
	err := syscall.DeviceIoControl(handle, ioctlUsbGetDescriptorFromNodeConnection,
		&buf[0], uint32(len(buf)), &buf[0], uint32(len(buf)), &bytesReturned, nil)
	if err != nil {
		return 0
	}
	// Config descriptor layout: bLength@0, bDescriptorType@1, wTotalLength@2,
	// bNumInterfaces@4, bConfigurationValue@5, iConfiguration@6,
	// bmAttributes@7, bMaxPower@8. Data starts at buffer offset 12, so
	// bMaxPower is at absolute offset 12+8 = 20.
	if bytesReturned < 21 {
		return 0
	}
	return int(buf[20]) * 2
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseVIDPIDFromHWID extracts VID/PID from a hardware ID such as
// "USB\VID_1234&PID_ABCD&REV_0100", returning lowercase "0x1234" / "0xabcd".
func parseVIDPIDFromHWID(hwid string) (vid, pid string) {
	hwid = strings.ToUpper(hwid)
	if idx := strings.Index(hwid, "VID_"); idx >= 0 && idx+8 <= len(hwid) {
		vid = "0x" + strings.ToLower(hwid[idx+4:idx+8])
	}
	if idx := strings.Index(hwid, "PID_"); idx >= 0 && idx+8 <= len(hwid) {
		pid = "0x" + strings.ToLower(hwid[idx+4:idx+8])
	}
	return vid, pid
}

// speedToString maps the USB_DEVICE_SPEED field to a human string.
func speedToString(speed uint8) string {
	switch speed {
	case 1:
		return "1.5 Mb/s"
	case 2:
		return "12 Mb/s"
	case 3:
		return "480 Mb/s"
	case 4:
		return "5 Gb/s"
	case 5:
		return "10 Gb/s"
	default:
		return "Unknown"
	}
}

// speedToBps converts a speed string to raw bits per second (0 if unknown).
func speedToBps(speed string) uint64 {
	switch speed {
	case "1.5 Mb/s":
		return 1_500_000
	case "12 Mb/s":
		return 12_000_000
	case "480 Mb/s":
		return 480_000_000
	case "5 Gb/s":
		return 5_000_000_000
	case "10 Gb/s":
		return 10_000_000_000
	case "20 Gb/s":
		return 20_000_000_000
	case "40 Gb/s":
		return 40_000_000_000
	}
	return 0
}

// usbKey normalizes a vendor:product pair for matching.
func usbKey(vid, pid string) string {
	return strings.ToLower(vid) + ":" + strings.ToLower(pid)
}

// bytesToUTF16 decodes a little-endian UTF-16 byte buffer into []uint16.
func bytesToUTF16(b []byte) []uint16 {
	n := len(b) / 2
	if n == 0 {
		return nil
	}
	u := make([]uint16, n)
	for i := 0; i < n; i++ {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return u
}

// multiSzStrings splits a double-null-terminated UTF-16 string list.
func multiSzStrings(u []uint16) []string {
	var out []string
	for i := 0; i < len(u); {
		j := i
		for j < len(u) && u[j] != 0 {
			j++
		}
		if j > i {
			out = append(out, windows.UTF16ToString(u[i:j]))
		}
		if j >= len(u) {
			break
		}
		i = j + 1
	}
	return out
}
