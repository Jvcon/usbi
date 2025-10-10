package usbi

import (
	"io/ioutil"
	"path/filepath"
	"strconv"
	"strings"
)

type LinuxBackend struct{}

func (b *LinuxBackend) ListDevices() ([]USBDevice, error) {
	basePath := "/sys/bus/usb/devices"
	entries, err := ioutil.ReadDir(basePath)
	if err != nil {
		return nil, err
	}

	var devices []USBDevice
	for _, e := range entries {
		devPath := filepath.Join(basePath, e.Name())
		if !strings.Contains(e.Name(), "-") {
			continue
		}

		vendorID := readFile(filepath.Join(devPath, "idVendor"))
		productID := readFile(filepath.Join(devPath, "idProduct"))
		speed := readFile(filepath.Join(devPath, "speed")) + " Mb/s"
		maxPower := readFile(filepath.Join(devPath, "bMaxPower"))
		product := readFile(filepath.Join(devPath, "product"))

		devices = append(devices, USBDevice{
			Name:          product,
			VendorID:      vendorID,
			ProductID:     productID,
			Speed:         speed,
			PowerRequired: parsePower(maxPower),
			LocationID:    e.Name(),
			IsHub:         strings.Contains(strings.ToLower(product), "hub"),
		})
	}
	return devices, nil
}

func readFile(path string) string {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func parsePower(p string) int {
	p = strings.TrimSuffix(p, "mA")
	val, _ := strconv.Atoi(strings.TrimSpace(p))
	return val
}
