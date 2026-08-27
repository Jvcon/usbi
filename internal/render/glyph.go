package render

import (
	"strconv"
	"strings"

	"usbi"
)

// Tier is the USB version tier inferred from a device.  It drives both the
// inline and big glyphs as well as the tier color.
type Tier string

const (
	TierEmpty   Tier = "empty"
	TierUSB1    Tier = "usb1"
	TierUSB2    Tier = "usb2"
	TierUSB3_0  Tier = "usb3.0"
	TierUSB3_1  Tier = "usb3.1"
	TierUSB3_2  Tier = "usb3.2"
	TierUSB4G2  Tier = "usb4g2"
	TierUSB4G3  Tier = "usb4g3"
	TierUSB4G4  Tier = "usb4g4"
	TierTBT3    Tier = "tbt3"
	TierUnknown Tier = "unknown"
)

// usbTier infers the USB version tier from a device using BcdUSB, SpeedBps,
// and the Speed string, then applies Type-C cable enrichment.
func usbTier(d usbi.USBDevice) Tier {
	tier := tierBase(d)

	// Enrichment: cable metadata can raise or override the tier.
	if d.CableTBT3 {
		return TierTBT3
	}
	if d.CableUSB4 && tierLess(tier, TierUSB4G3) {
		return TierUSB4G3
	}
	return tier
}

// tierBase infers the tier from device descriptor / speed fields only.
func tierBase(d usbi.USBDevice) Tier {
	// Primary: parse BcdUSB as hex (normalize case first).
	if t := tierFromBcdUSB(d.BcdUSB); t != TierUnknown {
		return t
	}

	// Secondary: parse SpeedBps.
	if d.SpeedBps > 0 {
		return tierFromBps(d.SpeedBps)
	}

	// Tertiary: parse Speed string.
	if t := tierFromSpeed(d.Speed); t != TierUnknown {
		return t
	}

	return TierEmpty
}

// tierLess reports whether a is a lower tier than b.
func tierLess(a, b Tier) bool {
	order := map[Tier]int{
		TierEmpty:  0,
		TierUSB1:   1,
		TierUSB2:   2,
		TierUSB3_0: 3,
		TierUSB3_1: 4,
		TierUSB3_2: 5,
		TierUSB4G2: 6,
		TierUSB4G3: 7,
		TierUSB4G4: 8,
		TierTBT3:   9,
		TierUnknown: 0,
	}
	return order[a] < order[b]
}

func tierFromBcdUSB(s string) Tier {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "0x")
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return TierUnknown
	}
	switch v {
	case 0x0100, 0x0110:
		return TierUSB1
	case 0x0200:
		return TierUSB2
	case 0x0250:
		return TierUSB2
	case 0x0300:
		return TierUSB3_0
	case 0x0310:
		return TierUSB3_1
	case 0x0320:
		return TierUSB3_2
	case 0x0400:
		return TierUSB4G3
	}
	return TierUnknown
}

func tierFromBps(bps uint64) Tier {
	switch {
	case bps <= 1_500_000:
		return TierUSB1
	case bps <= 12_000_000:
		return TierUSB1
	case bps <= 480_000_000:
		return TierUSB2
	case bps <= 5_000_000_000:
		return TierUSB3_0
	case bps <= 10_000_000_000:
		return TierUSB3_1
	case bps <= 20_000_000_000:
		return TierUSB3_2
	case bps <= 40_000_000_000:
		return TierUSB4G3
	case bps <= 80_000_000_000:
		return TierUSB4G4
	default:
		return TierUSB4G4
	}
}

func tierFromSpeed(s string) Tier {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(s, "wireless"):
		return TierUSB2
	case strings.HasPrefix(s, "1.5 ") || s == "1.5 mb/s":
		return TierUSB1
	case strings.HasPrefix(s, "12 ") && strings.Contains(s, "mb"):
		return TierUSB1
	case strings.HasPrefix(s, "480 "):
		return TierUSB2
	case strings.HasPrefix(s, "5 ") && strings.Contains(s, "gb"):
		return TierUSB3_0
	case strings.HasPrefix(s, "10 ") && strings.Contains(s, "gb"):
		return TierUSB3_1
	case strings.HasPrefix(s, "20 ") && strings.Contains(s, "gb"):
		return TierUSB3_2
	case strings.HasPrefix(s, "40 ") && strings.Contains(s, "gb"):
		return TierUSB4G3
	case strings.HasPrefix(s, "80 ") && strings.Contains(s, "gb"):
		return TierUSB4G4
	case strings.Contains(s, "usb4"):
		if strings.Contains(s, "gen2") {
			return TierUSB4G2
		}
		if strings.Contains(s, "gen4") {
			return TierUSB4G4
		}
		return TierUSB4G3
	case strings.Contains(s, "tbt3"), strings.Contains(s, "thunderbolt"):
		return TierTBT3
	}
	return TierUnknown
}

// InlineGlyph returns the small icon shown in the overview grid.
func InlineGlyph(t Tier) string {
	switch t {
	case TierUSB1:
		return "·|·"
	case TierUSB2:
		return "◁─▷"
	case TierUSB3_0:
		return "◁─▷\\|⚡"
	case TierUSB3_1:
		return "◁─▷\\|⚡⚡"
	case TierUSB3_2:
		return "◁─▷\\|⚡⚡"
	case TierUSB4G2:
		return "◁─▷\\|▰▰"
	case TierUSB4G3:
		return "◁─▷\\|▰▰▰"
	case TierUSB4G4:
		return "◁─▷\\|▰▰▰▰"
	case TierTBT3:
		return "◁─▷\\|⚡TBT"
	case TierEmpty:
		return "▱▱"
	default:
		return "▱▱"
	}
}

// BigGlyph returns the large neofetch-style ASCII art shown in card mode.
func BigGlyph(t Tier) string {
	switch t {
	case TierUSB1:
		return "·\\|·"
	case TierUSB2:
		return "◁─▷\\|"
	case TierUSB3_0:
		return "◁─▷\\|⚡\n SS"
	case TierUSB3_1, TierUSB3_2:
		return "◁─▷\\|⚡⚡\nSS+"
	case TierUSB4G2:
		return "◁─▷\\|▰ ▰\n 20"
	case TierUSB4G3:
		return "◁─▷\\|▰ ▰▰\n 40"
	case TierUSB4G4:
		return "◁─▷\\|▰▰▰▰\n 80"
	case TierTBT3:
		return "◁─▷\\|⚡\nTBT"
	case TierEmpty:
		return "· · ·"
	default:
		return "· · ·"
	}
}

// TierLabel returns a short human label for the tier.
func TierLabel(t Tier) string {
	switch t {
	case TierUSB1:
		return "USB 1.x"
	case TierUSB2:
		return "USB 2.0"
	case TierUSB3_0:
		return "USB 3.0"
	case TierUSB3_1:
		return "USB 3.1"
	case TierUSB3_2:
		return "USB 3.2"
	case TierUSB4G2:
		return "USB4 Gen2"
	case TierUSB4G3:
		return "USB4 Gen3"
	case TierUSB4G4:
		return "USB4 Gen4"
	case TierTBT3:
		return "TBT3"
	case TierEmpty:
		return "empty"
	default:
		return "unknown"
	}
}
