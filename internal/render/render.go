// Package render rasterizes adplot content (text, images) into raw RGB
// framebuffers for pixel-streaming LED drivers such as DDP.
package render

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"  // register decoder for LoadImage
	_ "image/jpeg" // register decoder for LoadImage
	_ "image/png"  // register decoder for LoadImage
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// ParseColor parses "#rrggbb" (or "rrggbb"). An empty string falls back to
// red, matching the vendors' default text color.
func ParseColor(s string) (color.Color, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return color.RGBA{R: 0xff, A: 0xff}, nil
	}
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return nil, fmt.Errorf("invalid color %q: want #rrggbb", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid color %q: %w", s, err)
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}, nil
}

// Text renders text with the built-in 7x13 bitmap font at the given integer
// scale (values < 1 are clamped to 1) and returns a tight bitmap around the
// glyphs.
func Text(text string, col color.Color, scale int) *image.RGBA {
	if scale < 1 {
		scale = 1
	}
	width := 7 * utf8.RuneCountInString(text)
	if width < 1 {
		width = 1
	}
	base := image.NewRGBA(image.Rect(0, 0, width, 13))
	drawer := &font.Drawer{
		Dst:  base,
		Src:  image.NewUniform(col),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(0, 11), // baseline = ascent of Face7x13
	}
	drawer.DrawString(text)
	if scale == 1 {
		return base
	}
	big := image.NewRGBA(image.Rect(0, 0, width*scale, 13*scale))
	xdraw.NearestNeighbor.Scale(big, big.Bounds(), base, base.Bounds(), xdraw.Over, nil)
	return big
}

// Fit returns a w×h black canvas with src centered and enlarged by the largest
// integer factor that fits. Integer scaling keeps pixels crisp, which matters
// at LED resolutions.
func Fit(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)

	sb := src.Bounds()
	if sb.Dx() == 0 || sb.Dy() == 0 {
		return dst
	}
	scale := w / sb.Dx()
	if s := h / sb.Dy(); s < scale {
		scale = s
	}
	if scale < 1 {
		scale = 1
	}
	tw, th := sb.Dx()*scale, sb.Dy()*scale
	rect := image.Rect((w-tw)/2, (h-th)/2, (w+tw)/2, (h+th)/2).Intersect(dst.Bounds())
	xdraw.NearestNeighbor.Scale(dst, rect, src, sb, xdraw.Over, nil)
	return dst
}

// RGBBytes flattens img into a row-major RGB byte slice (3 bytes per pixel),
// the layout expected by DDP and similar pixel protocols.
func RGBBytes(img *image.RGBA) []byte {
	b := img.Bounds()
	out := make([]byte, 0, b.Dx()*b.Dy()*3)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[img.PixOffset(b.Min.X, y):img.PixOffset(b.Max.X, y)]
		for i := 0; i+3 <= len(row); i += 4 {
			out = append(out, row[i], row[i+1], row[i+2])
		}
	}
	return out
}

// LoadImage decodes a PNG, JPEG or GIF file for use with Fit and RGBBytes.
func LoadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return img, nil
}
