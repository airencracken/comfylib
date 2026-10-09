// SPDX-License-Identifier: AGPL-3.0-or-later
package profileimage

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
)

func fixture(t *testing.T, count, size int) []byte {
	t.Helper()
	v := &gif.GIF{}
	for i := 0; i < count; i++ {
		img := image.NewPaletted(image.Rect(0, 0, size, size), color.Palette{color.Transparent, color.White})
		img.SetColorIndex(i%size, i%size, 1)
		v.Image = append(v.Image, img)
		v.Delay = append(v.Delay, 1)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, v); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestProfileImageContracts(t *testing.T) {
	for count := 1; count <= MaxFrames; count++ {
		v, err := Normalize(fixture(t, count, 8))
		if err != nil {
			t.Fatal(count, err)
		}
		if _, err := png.Decode(bytes.NewReader(v.Still)); err != nil {
			t.Fatal(err)
		}
		if (count > 1) != (len(v.Animation) > 0) {
			t.Fatal("animation contract", count)
		}
		if count > 1 {
			decoded, err := gif.DecodeAll(bytes.NewReader(v.Animation))
			if err != nil || len(decoded.Image) != count {
				t.Fatal("lost frames", err)
			}
			for _, delay := range decoded.Delay {
				if delay < 10 {
					t.Fatal("unsafe frame rate")
				}
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 512, 512))); err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(b.Bytes()); err != nil {
		t.Fatal("boundary PNG", err)
	}
}

func TestRejectsAdversarialProfiles(t *testing.T) {
	var wide bytes.Buffer
	if err := png.Encode(&wide, image.NewNRGBA(image.Rect(0, 0, 513, 1))); err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(wide.Bytes()); err == nil {
		t.Fatal("wide PNG accepted")
	}
	valid := fixture(t, 2, 8)
	inputs := [][]byte{nil, []byte("<svg/>"), bytes.Repeat([]byte("x"), (2<<20)+1), fixture(t, MaxFrames+1, 8), fixture(t, 1, 513), append(append([]byte{}, valid...), 1)}
	for n := 0; n < len(valid); n++ {
		inputs = append(inputs, valid[:n])
	}
	for _, data := range inputs {
		v, err := Normalize(data)
		if err == nil || len(v.Still) != 0 || len(v.Animation) != 0 {
			t.Fatal("accepted malformed or oversized image", len(data))
		}
	}
	// A frame can claim dimensions larger than the logical screen.
	data := append([]byte{}, valid...)
	for i := 13; i < len(data)-9; i++ {
		if data[i] == 0x2c {
			data[i+5], data[i+6] = 0xff, 0xff
			break
		}
	}
	if _, err := Normalize(data); err == nil {
		t.Fatal("oversized descriptor accepted")
	}
}

func FuzzNormalize(f *testing.F) {
	f.Add([]byte("GIF89a"))
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := Normalize(data)
		if err != nil {
			return
		}
		config, err := png.DecodeConfig(bytes.NewReader(v.Still))
		if err != nil || config.Width > MaxDimension || config.Height > MaxDimension {
			t.Fatal("invalid normalized output")
		}
		if len(v.Animation) > 0 {
			frames, err := gif.DecodeAll(bytes.NewReader(v.Animation))
			if err != nil || len(frames.Image) > MaxFrames {
				t.Fatal("invalid animation")
			}
		}
	})
}
