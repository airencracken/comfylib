// SPDX-License-Identifier: AGPL-3.0-or-later

// Package brandimage validates small uploaded PNG, JPEG and GIF images and
// converts them to a single PNG without metadata or animation.
package brandimage

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
)

// Limits bound encoded input size and decoded memory before image decoding.
const (
	MaxBytes     = 2 << 20
	MaxDimension = 2048
	MaxPixels    = MaxDimension * MaxDimension
)

// Normalize checks the input and dimensions before decoding, then emits PNG.
// For GIFs only the first frame is used. PNG encoding preserves transparency.
func Normalize(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > MaxBytes {
		return nil, errors.New("choose an image no larger than 2 MiB")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > MaxDimension || config.Height > MaxDimension || int64(config.Width)*int64(config.Height) > MaxPixels {
		return nil, errors.New("choose a valid image up to 2048 by 2048 pixels")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("choose a PNG, JPEG, or GIF image")
	}
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
