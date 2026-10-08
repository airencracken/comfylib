// SPDX-License-Identifier: AGPL-3.0-or-later
package brandimage_test

import (
	"bytes"
	"github.com/airencracken/comfylib/brandimage"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
	"testing/quick"
)

func TestNormalizedImageContract(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.SetNRGBA(1, 1, color.NRGBA{R: 200, A: 100})
	for _, format := range []string{"png", "jpeg", "gif"} {
		var input bytes.Buffer
		var err error
		switch format {
		case "png":
			err = png.Encode(&input, img)
		case "jpeg":
			err = jpeg.Encode(&input, img, nil)
		case "gif":
			err = gif.Encode(&input, img, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		raw := append(input.Bytes(), []byte("untrusted-metadata-after-image")...)
		clean, err := brandimage.Normalize(raw)
		if err != nil {
			t.Fatal(format, err)
		}
		decoded, err := png.Decode(bytes.NewReader(clean))
		if err != nil || decoded.Bounds() != img.Bounds() || bytes.Contains(clean, []byte("untrusted-metadata")) {
			t.Fatal(format, "normalization contract", err)
		}
		if format == "png" && decoded.At(1, 1) != img.At(1, 1) {
			t.Fatal("transparency lost")
		}
	}
}
func TestRejectsAdversarialImages(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte("<svg><script>bad</script></svg>"), []byte("not an image"), make([]byte, brandimage.MaxBytes+1)} {
		if clean, err := brandimage.Normalize(raw); err == nil || clean != nil {
			t.Fatal("invalid input accepted")
		}
	}
	for _, size := range []image.Point{{X: 2049, Y: 1}, {X: 1, Y: 2049}} {
		var out bytes.Buffer
		if err := png.Encode(&out, image.NewGray(image.Rect(0, 0, size.X, size.Y))); err != nil {
			t.Fatal(err)
		}
		if _, err := brandimage.Normalize(out.Bytes()); err == nil {
			t.Fatal("decoded dimensions unbounded")
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	raw := out.Bytes()
	if _, err := brandimage.Normalize(raw[:len(raw)-16]); err == nil {
		t.Fatal("truncated image accepted")
	}
	oversized := make([]byte, brandimage.MaxBytes+1)
	copy(oversized, raw)
	if _, err := brandimage.Normalize(oversized); err == nil {
		t.Fatal("valid image with excessive encoded data accepted")
	}
}
func TestNormalizationProperty(t *testing.T) {
	property := func(w, h uint8) bool {
		var input bytes.Buffer
		img := image.NewGray(image.Rect(0, 0, 1+int(w)%32, 1+int(h)%32))
		if png.Encode(&input, img) != nil {
			return false
		}
		clean, err := brandimage.Normalize(input.Bytes())
		if err != nil {
			return false
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(clean))
		return err == nil && format == "png" && cfg.Width == img.Bounds().Dx() && cfg.Height == img.Bounds().Dy()
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 30}); err != nil {
		t.Fatal(err)
	}
}
func FuzzNormalize(f *testing.F) {
	f.Add([]byte("invalid"))
	f.Fuzz(func(t *testing.T, data []byte) {
		clean, err := brandimage.Normalize(data)
		if err == nil {
			if _, err := png.Decode(bytes.NewReader(clean)); err != nil {
				t.Fatal(err)
			}
		}
	})
}
