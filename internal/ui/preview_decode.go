package ui

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// decodePreview returns the first frame of a preview image.
// Animated WebP has no top-level VP8 frame, so the stock decoder rejects it;
// the first animation frame is lifted out and decoded as a still.
func decodePreview(data []byte) (image.Image, error) {
	img, err := decodeImage(data)
	if err == nil {
		return img, nil
	}
	if still := stillWebP(data); still != nil {
		if img, err2 := decodeImage(still); err2 == nil {
			return img, nil
		}
	}
	return nil, err
}

func decodeImage(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxPreviewEdge || cfg.Height > maxPreviewEdge {
		return nil, fmt.Errorf("image is %dx%d", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// stillWebP rebuilds an animated WebP as a single-frame file the standard
// decoder accepts. It returns nil when data is not an animated WebP.
func stillWebP(data []byte) []byte {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil
	}
	for _, chunk := range splitChunks(data[12:]) {
		if len(chunk) < 8 || string(chunk[0:4]) != "ANMF" {
			continue
		}
		payload := chunk[8:]
		if len(payload) < 16 {
			continue
		}
		w := int(uint32(payload[6])|uint32(payload[7])<<8|uint32(payload[8])<<16) + 1
		h := int(uint32(payload[9])|uint32(payload[10])<<8|uint32(payload[11])<<16) + 1
		var alpha, frame []byte
		for _, inner := range splitChunks(payload[16:]) {
			if len(inner) < 4 {
				continue
			}
			switch string(inner[0:4]) {
			case "ALPH":
				alpha = inner
			case "VP8 ", "VP8L":
				frame = inner
			}
		}
		if frame == nil {
			continue
		}
		if alpha != nil && string(frame[0:4]) == "VP8 " && w > 0 && h > 0 && w <= maxPreviewEdge && h <= maxPreviewEdge {
			return wrapWebP(vp8xChunk(w, h), alpha, frame)
		}
		return wrapWebP(frame)
	}
	return nil
}

func splitChunks(b []byte) [][]byte {
	var out [][]byte
	for len(b) >= 8 {
		n := int(binary.LittleEndian.Uint32(b[4:8]))
		if n < 0 || 8+n > len(b) {
			break
		}
		out = append(out, b[:8+n])
		b = b[8+n:]
		if n%2 == 1 && len(b) > 0 {
			b = b[1:]
		}
	}
	return out
}

func wrapWebP(chunks ...[]byte) []byte {
	payload := []byte("WEBP")
	for _, chunk := range chunks {
		payload = append(payload, chunk...)
		if len(chunk) >= 8 && binary.LittleEndian.Uint32(chunk[4:8])%2 == 1 {
			payload = append(payload, 0)
		}
	}
	out := make([]byte, 8+len(payload))
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(payload)))
	copy(out[8:], payload)
	return out
}

func vp8xChunk(w, h int) []byte {
	payload := make([]byte, 10)
	payload[0] = 1 << 4 // alpha
	wm := uint32(w - 1)
	hm := uint32(h - 1)
	payload[4] = byte(wm)
	payload[5] = byte(wm >> 8)
	payload[6] = byte(wm >> 16)
	payload[7] = byte(hm)
	payload[8] = byte(hm >> 8)
	payload[9] = byte(hm >> 16)
	buf := make([]byte, 8+len(payload))
	copy(buf, "VP8X")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(payload)))
	copy(buf[8:], payload)
	return buf
}
