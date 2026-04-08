// gentiles generates minimal placeholder PNG map tiles for offline use.
//
// Usage:
//
//	go run ./cmd/gentiles [--out web/static/tiles] [--max-zoom 2]
//
// It produces valid 256×256 PNG files at web/static/tiles/{z}/{x}/{y}.png
// for every (z, x, y) combination from zoom 0 through max-zoom (default 2).
// Each tile is a light-gray background with a 1-pixel border, making it a
// recognisable "placeholder" that keeps Leaflet's tile layer functional
// without requiring an internet connection.
//
// Tile count by zoom level:
//
//	z=0 →   1 tile   (1×1 grid)
//	z=1 →   4 tiles  (2×2 grid)
//	z=2 →  16 tiles  (4×4 grid)
//	z=3 →  64 tiles  (8×8 grid)
//	…
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
	"path/filepath"
)

func main() {
	outDir := flag.String("out", "web/static/tiles", "output directory for tile files")
	maxZoom := flag.Int("max-zoom", 2, "maximum zoom level to generate (inclusive)")
	flag.Parse()

	total := 0
	for z := 0; z <= *maxZoom; z++ {
		n := 1 << z // tiles per axis = 2^z
		for x := 0; x < n; x++ {
			for y := 0; y < n; y++ {
				path := filepath.Join(*outDir, fmt.Sprintf("%d/%d/%d.png", z, x, y))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					log.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
				}
				if err := writeTile(path, z, x, y); err != nil {
					log.Fatalf("write tile z=%d x=%d y=%d: %v", z, x, y, err)
				}
				total++
			}
		}
		fmt.Printf("zoom %d: %d tile(s) written\n", z, n*n)
	}
	fmt.Printf("Done — %d total tiles in %s\n", total, *outDir)
}

// writeTile creates a 256×256 PNG placeholder tile at the given path.
// The tile colour shifts subtly with zoom level so it is visually
// distinguishable at different zoom steps without requiring any external
// font or drawing library.
func writeTile(path string, z, x, y int) error {
	const size = 256

	// Background colour: light grey, slightly warmer at higher zoom.
	grey := uint8(210 + z*5)
	if grey > 240 {
		grey = 240
	}
	bg := color.RGBA{R: grey, G: grey, B: grey, A: 255}
	border := color.RGBA{R: 160, G: 160, B: 160, A: 255}

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

	// Draw a 1-pixel border so tile boundaries are visible.
	for i := 0; i < size; i++ {
		img.SetRGBA(i, 0, border)
		img.SetRGBA(i, size-1, border)
		img.SetRGBA(0, i, border)
		img.SetRGBA(size-1, i, border)
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
