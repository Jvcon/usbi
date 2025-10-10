package usbi

/*
#cgo LDFLAGS: -lsetupapi -lwinusb
#include <windows.h>
#include <setupapi.h>
#include <winusb.h>
#include <usbiodef.h>
*/
import "C"
import (
	"fmt"
	"strings"
	"unsafe"
)

type WinBackend struct{}

func (b *WinBackend) ListDevices() ([]USBDevice, error) {
	var devices []USBDevice

	guid := C.GUID{Data1: 0xA5DCBF10, Data2: 0x6530, Data3: 0x11D2, Data4: [8]C.UCHAR{0x90, 0x1F, 0x00, 0xC0, 0x4F, 0xB9, 0x51, 0xED}}
	hdev := C.SetupDiGetClassDevs(&guid, nil, nil, C.DIGCF_PRESENT|C.DIGCF_DEVICEINTERFACE)
	if hdev == C.INVALID_HANDLE_VALUE {
		return nil, fmt.Errorf("SetupDiGetClassDevs failed")
	}
	defer C.SetupDiDestroyDeviceInfoList(hdev)

	var devInfo C.SP_DEVINFO_DATA
	devInfo.cbSize = C.DWORD(unsafe.Sizeof(devInfo))

	index := C.DWORD(0)
	for C.SetupDiEnumDeviceInfo(hdev, index, &devInfo) != 0 {
		name := getDevProp(hdev, &devInfo, C.SPDRP_FRIENDLYNAME)
		if name == "" {
			name = getDevProp(hdev, &devInfo, C.SPDRP_DEVICEDESC)
		}
		hwid := getDevProp(hdev, &devInfo, C.SPDRP_HARDWAREID)

		vendorID, productID := parseVIDPID(hwid)
		devicePath := getDeviceInterfacePath(hdev, &devInfo, &guid)

		speed, protocol, power := queryWinUSBInfo(devicePath)

		devices = append(devices, USBDevice{
			Name:          name,
			VendorID:      vendorID,
			ProductID:     productID,
			Speed:         speed,
			Protocol:      protocol,
			PowerRequired: power,
			LocationID:    fmt.Sprintf("%d", index),
		})
		index++
	}
	return devices, nil
}

func getDevProp(hdev C.HDEVINFO, devInfo *C.SP_DEVINFO_DATA, prop C.DWORD) string {
	buf := make([]C.CHAR, 256)
	if C.SetupDiGetDeviceRegistryPropertyA(hdev, devInfo, prop, nil, (*C.BYTE)(unsafe.Pointer(&buf[0])), C.DWORD(len(buf)), nil) != 0 {
		return C.GoString(&buf[0])
	}
	return ""
}

func parseVIDPID(hwid string) (string, string) {
	hwid = strings.ToUpper(hwid)
	var vid, pid string
	if idx := strings.Index(hwid, "VID_"); idx >= 0 {
		vid = hwid[idx+4 : idx+8]
	}
	if idx := strings.Index(hwid, "PID_"); idx >= 0 {
		pid = hwid[idx+4 : idx+8]
	}
	return vid, pid
}

func getDeviceInterfacePath(hdev C.HDEVINFO, devInfo *C.SP_DEVINFO_DATA, guid *C.GUID) string {
	var ifaceData C.SP_DEVICE_INTERFACE_DATA
	ifaceData.cbSize = C.DWORD(unsafe.Sizeof(ifaceData))
	if C.SetupDiEnumDeviceInterfaces(hdev, devInfo, guid, 0, &ifaceData) == 0 {
		return ""
	}
	var reqSize C.DWORD
	C.SetupDiGetDeviceInterfaceDetailA(hdev, &ifaceData, nil, 0, &reqSize, nil)
	buf := make([]byte, reqSize)
	detail := (*C.SP_DEVICE_INTERFACE_DETAIL_DATA_A)(unsafe.Pointer(&buf[0]))
	detail.cbSize = 5 // SIZEOF(SP_DEVICE_INTERFACE_DETAIL_DATA_A) = 5 on Win32
	if C.SetupDiGetDeviceInterfaceDetailA(hdev, &ifaceData, detail, reqSize, nil, nil) == 0 {
		return ""
	}
	return C.GoString(&detail.DevicePath[0])
}

// ---- 来自 Microsoft WinUSB API 和 [libusb/os/windows_winusb.c](https://android.googlesource.com/platform/external/libusb/+/master/libusb/os/windows_winusb.c) 的缓存读取逻辑 ----
func queryWinUSBInfo(devicePath string) (string, string, int) {
	if devicePath == "" {
		return "", "", 0
	}
	path := C.CString(devicePath)
	defer C.free(unsafe.Pointer(path))

	handle := C.CreateFileA(path, C.GENERIC_WRITE|C.GENERIC_READ,
		C.FILE_SHARE_WRITE|C.FILE_SHARE_READ, nil,
		C.OPEN_EXISTING, C.FILE_ATTRIBUTE_NORMAL|C.FILE_FLAG_OVERLAPPED, nil)
	if handle == C.INVALID_HANDLE_VALUE {
		return "", "", 0
	}
	defer C.CloseHandle(handle)

	var winusbHandle C.WINUSB_INTERFACE_HANDLE
	if C.WinUsb_Initialize(handle, &winusbHandle) == 0 {
		return "", "", 0
	}
	defer C.WinUsb_Free(winusbHandle)

	var devDesc C.USB_DEVICE_DESCRIPTOR
	if C.WinUsb_GetDescriptor(winusbHandle, C.USB_DEVICE_DESCRIPTOR_TYPE, 0, 0,
		unsafe.Pointer(&devDesc), C.ULONG(unsafe.Sizeof(devDesc)), nil) == 0 {
		return "", "", 0
	}
	protocol := mapUSBProtocol(uint16(devDesc.bcdUSB))

	var confDesc C.USB_CONFIGURATION_DESCRIPTOR
	if C.WinUsb_GetDescriptor(winusbHandle, C.USB_CONFIGURATION_DESCRIPTOR_TYPE, 0, 0,
		unsafe.Pointer(&confDesc), C.ULONG(unsafe.Sizeof(confDesc)), nil) == 0 {
		return usbSpeedFromProtocol(protocol), protocol, 0
	}
	power := int(confDesc.bMaxPower) * 2
	return usbSpeedFromProtocol(protocol), protocol, power
}

func mapUSBProtocol(bcdUSB uint16) string {
	maj := bcdUSB >> 8
	switch maj {
	case 0x01:
		return "USB 1.x"
	case 0x02:
		return "USB 2.0"
	case 0x03:
		return "USB 3.x"
	default:
		return "Unknown"
	}
}

func usbSpeedFromProtocol(proto string) string {
	switch proto {
	case "USB 1.x":
		return "12 Mb/s"
	case "USB 2.0":
		return "480 Mb/s"
	case "USB 3.x":
		return "5~20 Gb/s"
	default:
		return "Unknown"
	}
}
