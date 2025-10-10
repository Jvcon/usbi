package main

import (
	"fmt"
	"runtime"
	"usbi"
)

func main() {
	var backend usbi.USBBackend
	switch runtime.GOOS {
	case "darwin":
		backend = &usbi.DarwinBackend{}
	case "linux":
		backend = &usbi.LinuxBackend{}
	case "windows":
		backend = &usbi.WinBackend{}
	default:
		fmt.Println("Unsupported OS")
		return
	}

	devs, err := backend.ListDevices()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	for _, d := range devs {
		fmt.Printf("%+v\n", d)
	}
}
