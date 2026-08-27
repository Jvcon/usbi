// Empty stub to satisfy `go build ./...` when building for unsupported
// platforms.  The real Windows/Darwin/Linux implementations live in
// *_windows.go / *_darwin.go / *_linux.go and are selected by build tags.
//go:build !linux && !darwin && !windows

package usbi

type unsupportedBackend struct{}

func (unsupportedBackend) ListDevices() ([]USBDevice, error) {
	return nil, errUnsupported
}

// NewBackend is the fallback used on platforms without a real backend.
func NewBackend() (USBBackend, string) {
	return &unsupportedBackend{}, "unsupported"
}

var errUnsupported = &unsupportedError{}

type unsupportedError struct{}

func (*unsupportedError) Error() string { return "usbi: unsupported platform" }
