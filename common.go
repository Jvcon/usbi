package usbi

type USBDevice struct {
	Name           string
	VendorID       string
	ProductID      string
	Speed          string
	Protocol       string
	PowerRequired  int
	PowerAvailable int
	LocationID     string
	IsHub          bool
}

type USBBackend interface {
	ListDevices() ([]USBDevice, error)
}
