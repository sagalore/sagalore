package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// personalCoverPNG returns a simple cover for a starter title, or nil.
func personalCoverPNG(title string) []byte {
	switch title {
	case "Wave hello":
		return encodeCover(coverWave())
	case "A quick rhythm":
		return encodeCover(coverRhythm())
	case "Strike a pose":
		return encodeCover(coverDance())
	default:
		return nil
	}
}

func encodeCover(img *image.NRGBA) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func newCover(w, h int, top, bottom color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h-1)
		c := color.NRGBA{
			R: lerp(top.R, bottom.R, t),
			G: lerp(top.G, bottom.G, t),
			B: lerp(top.B, bottom.B, t),
			A: 255,
		}
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func lerp(a, b uint8, t float64) uint8 {
	return uint8(float64(a)*(1-t) + float64(b)*t + 0.5)
}

func fillRect(img *image.NRGBA, x0, y0, x1, y1 int, c color.NRGBA) {
	b := img.Bounds()
	if x0 < b.Min.X {
		x0 = b.Min.X
	}
	if y0 < b.Min.Y {
		y0 = b.Min.Y
	}
	if x1 > b.Max.X {
		x1 = b.Max.X
	}
	if y1 > b.Max.Y {
		y1 = b.Max.Y
	}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
}

func fillCircle(img *image.NRGBA, cx, cy, r int, c color.NRGBA) {
	r2 := r * r
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r2 {
				if image.Pt(x, y).In(img.Bounds()) {
					img.SetNRGBA(x, y, c)
				}
			}
		}
	}
}

// Sky and a raised hand — first action in the world.
func coverWave() *image.NRGBA {
	img := newCover(640, 360,
		color.NRGBA{R: 92, G: 148, B: 196, A: 255},
		color.NRGBA{R: 210, G: 186, B: 120, A: 255},
	)
	fillCircle(img, 520, 70, 48, color.NRGBA{R: 240, G: 210, B: 90, A: 255})
	ink := color.NRGBA{R: 36, G: 28, B: 18, A: 255}
	fillRect(img, 280, 150, 320, 300, ink)
	fillRect(img, 318, 160, 400, 185, ink)
	fillCircle(img, 300, 130, 28, ink)
	return img
}

// Two beats on a warm field — timed sequence.
func coverRhythm() *image.NRGBA {
	img := newCover(640, 360,
		color.NRGBA{R: 120, G: 72, B: 40, A: 255},
		color.NRGBA{R: 48, G: 28, B: 18, A: 255},
	)
	gold := color.NRGBA{R: 230, G: 190, B: 90, A: 255}
	fillCircle(img, 220, 180, 36, gold)
	fillCircle(img, 420, 180, 36, gold)
	fillRect(img, 250, 172, 390, 188, gold)
	return img
}

// A flourish under torchlight — dance.
func coverDance() *image.NRGBA {
	img := newCover(640, 360,
		color.NRGBA{R: 28, G: 72, B: 68, A: 255},
		color.NRGBA{R: 18, G: 28, B: 24, A: 255},
	)
	gold := color.NRGBA{R: 220, G: 175, B: 70, A: 255}
	fillCircle(img, 320, 200, 55, gold)
	fillRect(img, 300, 240, 340, 320, gold)
	fillCircle(img, 200, 100, 8, gold)
	fillCircle(img, 440, 90, 6, gold)
	fillCircle(img, 480, 160, 7, gold)
	fillCircle(img, 160, 180, 5, gold)
	return img
}
