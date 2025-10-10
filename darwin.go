//go:build darwin

package usbi

import (
	"bufio"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type DarwinBackend struct{}

// getSystemProfilerCommand 根据 macOS 版本选择 DataType
func getSystemProfilerCommand() *exec.Cmd {
	versionCmd := exec.Command("sw_vers", "-productVersion")
	output, err := versionCmd.Output()
	if err != nil {
		return exec.Command("system_profiler", "SPUSBDataType")
	}

	version := strings.TrimSpace(string(output))
	parts := strings.Split(version, ".")
	majorVer, _ := strconv.Atoi(parts[0])

	// 新版本更偏向 SPUSBHostDataType（例如 macOS 15+）
	if majorVer >= 15 {
		return exec.Command("system_profiler", "SPUSBHostDataType")
	}
	return exec.Command("system_profiler", "SPUSBDataType")
}

func (b *DarwinBackend) ListDevices() ([]USBDevice, error) {
	cmd := getSystemProfilerCommand()
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var devices []USBDevice
	var current USBDevice
	scanner := bufio.NewScanner(strings.NewReader(string(out)))

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if line == "" || line == "USB:" {
			continue
		}

		level := 0
		for i, char := range line {
			if char != ' ' {
				level = i / 2
				line = strings.TrimSpace(line)
				break
			}
		}

		// 总线信息
		if regexp.MustCompile(`^USB .* Bus:?$`).MatchString(line) {
			if current.Name != "" {
				devices = append(devices, current)
			}
			current = USBDevice{
				Name:  strings.TrimSuffix(line, ":"),
				Level: level,
				IsBus: true,
			}
			continue
		}

		// 设备名
		if strings.HasSuffix(line, ":") {
			if current.Name != "" {
				devices = append(devices, current)
			}
			current = USBDevice{
				Name:  strings.TrimSuffix(line, ":"),
				Level: level,
			}
			continue
		}

		// 属性解析
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			switch key {
			case "Speed", "Link Speed":
				current.Speed = value
			case "Manufacturer":
				current.Manufacturer = value
			case "Vendor ID", "USB Vendor ID":
				if m := regexp.MustCompile(`(0x[0-9a-fA-F]+)`).FindStringSubmatch(value); len(m) > 0 {
					current.VendorID = strings.ToLower(m[1])
				}
			case "Product ID", "USB Product ID":
				if m := regexp.MustCompile(`(0x[0-9a-fA-F]+)`).FindStringSubmatch(value); len(m) > 0 {
					current.ProductID = strings.ToLower(m[1])
				}
			case "Current Required (mA)":
				if val, err := strconv.Atoi(regexp.MustCompile(`\d+`).FindString(value)); err == nil {
					current.PowerRequired = val
				}
			case "Current Available (mA)":
				if val, err := strconv.Atoi(regexp.MustCompile(`\d+`).FindString(value)); err == nil {
					current.PowerAvailable = val
				}
			case "Power Allocated":
				if m := regexp.MustCompile(`\((\d+)\s*mA\)`).FindStringSubmatch(value); len(m) > 1 {
					if val, err := strconv.Atoi(m[1]); err == nil {
						current.PowerRequired = val
					}
				}
			case "Power Sink Capability":
				if m := regexp.MustCompile(`\((\d+)\s*mA\)`).FindStringSubmatch(value); len(m) > 1 {
					if val, err := strconv.Atoi(m[1]); err == nil {
						current.PowerAvailable = val
					}
				}
			case "Location ID":
				current.LocationID = value
			}
		}
	}

	if current.Name != "" {
		devices = append(devices, current)
	}

	return devices, nil
}
