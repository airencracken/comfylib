// SPDX-License-Identifier: AGPL-3.0-or-later

// Package profileimage normalizes account pictures and keeps a still rendition
// alongside bounded GIF animation. Encoded metadata is never retained.
package profileimage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/gif"
	"io"

	"github.com/airencracken/comfylib/brandimage"
)

const MaxDimension = 512
const MaxFrames = 64
const MaxOutputBytes = 4 << 20

type Picture struct {
	Still     []byte
	Animation []byte
}

// Normalize validates dimensions and GIF frame bounds before allocating pixels.
// PNG and JPEG inputs have only a still rendition; animated GIFs are re-encoded.
func Normalize(data []byte) (Picture, error) {
	var result Picture
	if len(data) == 0 || len(data) > brandimage.MaxBytes {
		return result, errors.New("choose a picture no larger than 2 MiB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > MaxDimension || config.Height > MaxDimension {
		return result, errors.New("choose a picture up to 512 by 512 pixels")
	}
	if format == "gif" {
		if err := checkFrames(data); err != nil {
			return result, err
		}
	}
	result.Still, err = brandimage.Normalize(data)
	if err != nil {
		return Picture{}, err
	}
	if format == "gif" {
		result.Animation, err = animation(data)
	}
	if err != nil || len(result.Still) > MaxOutputBytes || len(result.Animation) > MaxOutputBytes {
		return Picture{}, errors.New("picture could not be normalized within size limits")
	}
	return result, nil
}

func animation(data []byte) ([]byte, error) {
	frames, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if len(frames.Image) < 2 {
		return nil, nil
	}
	for i, delay := range frames.Delay {
		if delay < 10 {
			frames.Delay[i] = 10
		}
	}
	var output bytes.Buffer
	if err := gif.EncodeAll(&output, frames); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// Scan the complete block stream first: DecodeAll must never allocate an
// unbounded number of compressed frames. Reject truncation and trailing data.
func checkFrames(data []byte) error {
	r := bytes.NewReader(data[13:])
	if err := skipPalette(r, data[10]); err != nil {
		return err
	}
	count := 0
	for {
		marker, err := r.ReadByte()
		if err != nil {
			return err
		}
		switch marker {
		case 0x3b:
			if count == 0 || r.Len() != 0 {
				return errors.New("invalid GIF ending")
			}
			return nil
		case 0x21:
			if _, err = r.ReadByte(); err != nil {
				return err
			}
		case 0x2c:
			count++
			if count > MaxFrames {
				return errors.New("GIF has more than 64 frames")
			}
			if err = skipFrame(r); err != nil {
				return err
			}
		default:
			return errors.New("invalid GIF block")
		}
		if err = skipBlocks(r); err != nil {
			return err
		}
	}
}

func skipFrame(r *bytes.Reader) error {
	var desc [9]byte
	if _, err := io.ReadFull(r, desc[:]); err != nil {
		return err
	}
	w, h := binary.LittleEndian.Uint16(desc[4:6]), binary.LittleEndian.Uint16(desc[6:8])
	if w == 0 || h == 0 || w > MaxDimension || h > MaxDimension {
		return errors.New("GIF frame dimensions exceed limits")
	}
	if err := skipPalette(r, desc[8]); err != nil {
		return err
	}
	_, err := r.ReadByte() // LZW minimum code size; decoder validates it.
	return err
}

func skipPalette(r *bytes.Reader, packed byte) error {
	if packed&0x80 == 0 {
		return nil
	}
	_, err := io.CopyN(io.Discard, r, int64(3<<((packed&7)+1)))
	return err
}

func skipBlocks(r *bytes.Reader) error {
	for {
		n, err := r.ReadByte()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
			return err
		}
	}
}
