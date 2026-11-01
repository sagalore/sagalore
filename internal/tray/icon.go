package tray

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"image/png"
	"runtime"
)

//go:embed icon.png
var iconPNGBytes []byte

// Icon is the menu-bar and notification-area mark (the round map emblem).
func Icon() []byte {
	if runtime.GOOS == "windows" {
		return icoFromPNG(iconPNGBytes)
	}
	return iconPNGBytes
}

func icoFromPNG(pngBytes []byte) []byte {
	width, height := 128, 128
	if cfg, err := png.DecodeConfig(bytes.NewReader(pngBytes)); err == nil {
		width, height = cfg.Width, cfg.Height
	}
	dim := func(n int) byte {
		if n >= 256 {
			return 0
		}
		return byte(n)
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	buf.WriteByte(dim(width))
	buf.WriteByte(dim(height))
	buf.WriteByte(0)
	buf.WriteByte(0)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(32))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pngBytes)))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(22))
	buf.Write(pngBytes)
	return buf.Bytes()
}
