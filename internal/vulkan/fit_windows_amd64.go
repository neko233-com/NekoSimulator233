//go:build windows && amd64

package vulkan

import (
	"errors"
	"image"
)

// fitPlan caches only a row of black pixels and horizontal source offsets;
// no full-sized intermediate image or previous frame is retained.
type fitPlan struct {
	sw, sh, w, h      int
	left, top, fw, fh int
	xOffsets          []int
	black             []byte
}

func newFitPlan(sw, sh int, extent extent2D) (*fitPlan, error) {
	w, h := int(extent.Width), int(extent.Height)
	if sw <= 0 || sh <= 0 || sw > 8192 || sh > 8192 || sw*sh > 32*1024*1024 || w <= 0 || h <= 0 || w > 8192 || h > 8192 || w*h > 32*1024*1024 {
		return nil, errors.New("画面尺寸无效")
	}
	p := &fitPlan{sw: sw, sh: sh, w: w, h: h, fw: w, fh: h}
	if w == sw && h == sh {
		return p, nil
	}
	if w*sh <= h*sw {
		p.fh = max(1, w*sh/sw)
	} else {
		p.fw = max(1, h*sw/sh)
	}
	p.left, p.top = (w-p.fw)/2, (h-p.fh)/2
	p.xOffsets = make([]int, p.fw)
	for x := range p.xOffsets {
		p.xOffsets[x] = x * sw / p.fw * 4
	}
	p.black = make([]byte, w*4)
	for i := 3; i < len(p.black); i += 4 {
		p.black[i] = 255
	}
	return p, nil
}

func (p *fitPlan) matches(frame *image.RGBA, extent extent2D) bool {
	return p != nil && frame != nil && p.sw == frame.Rect.Dx() && p.sh == frame.Rect.Dy() && p.w == int(extent.Width) && p.h == int(extent.Height)
}

func (p *fitPlan) unscaledLetterbox() bool {
	return p != nil && p.fw == p.sw && p.fh == p.sh && (p.w != p.sw || p.h != p.sh)
}

// Called with the host lock held, as part of validating a submission.
func (s *Swapchain) ensureFitPlan(frame *image.RGBA) error {
	if s.fit.matches(frame, s.extent) {
		return nil
	}
	if frame == nil {
		return errors.New("画面尺寸无效")
	}
	p, err := newFitPlan(frame.Rect.Dx(), frame.Rect.Dy(), s.extent)
	if err != nil {
		return err
	}
	s.fit = p
	return nil
}

// pack writes directly to mapped Vulkan memory. It reads only the original
// frame and the small cached row, never the potentially write-combined mapping.
func (p *fitPlan) pack(dst []byte, frame *image.RGBA, format uint32) error {
	if _, err := validateFrame(frame); err != nil {
		return err
	}
	if !p.matches(frame, extent2D{uint32(p.w), uint32(p.h)}) || len(dst) != p.w*p.h*4 {
		return errors.New("画面尺寸无效")
	}
	bgra := format == vkFormatBGRA8 || format == vkFormatBGRA8SRGB
	if !bgra && format != vkFormatRGBA8 && format != vkFormatRGBA8SRGB {
		return errors.New("不支持的 Vulkan 像素格式")
	}
	if p.w == p.sw && p.h == p.sh {
		return frameBytes(dst, frame, format, false)
	}
	for y := 0; y < p.h; y++ {
		row := dst[y*p.w*4 : (y+1)*p.w*4]
		if y < p.top || y >= p.top+p.fh {
			copy(row, p.black)
			continue
		}
		left := p.left * 4
		right := (p.left + p.fw) * 4
		copy(row[:left], p.black[:left])
		copy(row[right:], p.black[:len(row)-right])
		src := frame.Pix[((y-p.top)*p.sh/p.fh)*frame.Stride:]
		out := row[left:right]
		for x, offset := range p.xOffsets {
			i := x * 4
			if bgra {
				out[i], out[i+1], out[i+2], out[i+3] = src[offset+2], src[offset+1], src[offset], src[offset+3]
			} else {
				copy(out[i:i+4], src[offset:offset+4])
			}
		}
	}
	return nil
}
