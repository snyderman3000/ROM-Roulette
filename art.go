package main

import (
	"image"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"os"
)

// LoadArt decodes a box-art file and shrinks it to fit w×h.
func LoadArt(path string, w, h int) *image.RGBA {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() > 8<<20 {
		return nil
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	return fit(img, w, h)
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	b := img.Bounds()
	r := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(r, r.Bounds(), img, b.Min, draw.Src)
	return r
}

// fit box-filters the image down so it fits inside w×h, keeping aspect ratio.
func fit(img image.Image, w, h int) *image.RGBA {
	src := toRGBA(img)
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if sw <= w && sh <= h {
		return src
	}
	scale := float64(w) / float64(sw)
	if s2 := float64(h) / float64(sh); s2 < scale {
		scale = s2
	}
	dw, dh := int(float64(sw)*scale), int(float64(sh)*scale)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := y*sh/dh, (y+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0, x1 := x*sw/dw, (x+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, b, a, n int
			for yy := y0; yy < y1; yy++ {
				o := yy*src.Stride + x0*4
				for xx := x0; xx < x1; xx++ {
					r += int(src.Pix[o])
					g += int(src.Pix[o+1])
					b += int(src.Pix[o+2])
					a += int(src.Pix[o+3])
					n++
					o += 4
				}
			}
			d := y*dst.Stride + x*4
			dst.Pix[d], dst.Pix[d+1], dst.Pix[d+2], dst.Pix[d+3] = uint8(r/n), uint8(g/n), uint8(b/n), uint8(a/n)
		}
	}
	return dst
}

// DrawArt draws a (premultiplied) RGBA image 1:1 at x,y, honouring transparency.
func (c *Canvas) DrawArt(src *image.RGBA, x, y int) {
	b := src.Bounds()
	for yy := 0; yy < b.Dy(); yy++ {
		py := y + yy
		if py < 0 || py >= c.H {
			continue
		}
		so := yy * src.Stride
		for xx := 0; xx < b.Dx(); xx++ {
			px := x + xx
			if px < 0 || px >= c.W {
				so += 4
				continue
			}
			a := int(src.Pix[so+3])
			d := py*c.Stride + px*4
			if a == 255 {
				c.Pix[d], c.Pix[d+1], c.Pix[d+2] = src.Pix[so], src.Pix[so+1], src.Pix[so+2]
			} else if a > 0 {
				ia := 255 - a
				c.Pix[d] = uint8(int(src.Pix[so]) + int(c.Pix[d])*ia/255)
				c.Pix[d+1] = uint8(int(src.Pix[so+1]) + int(c.Pix[d+1])*ia/255)
				c.Pix[d+2] = uint8(int(src.Pix[so+2]) + int(c.Pix[d+2])*ia/255)
			}
			so += 4
		}
	}
}
