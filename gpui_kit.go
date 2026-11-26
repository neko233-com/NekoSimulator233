package main

import (
	"context"
	"encoding/binary"
	"image"
	"sync/atomic"

	"github.com/moriyoshi/gpui-go/client"
	"github.com/moriyoshi/gpui-go/scene"
)

const uiFontFamily = "Microsoft YaHei UI"

// Shared color palette matching Figure 1 (light modern Entry Manager) and
// Figure 2 (Chrome-style Emulator Window).
const (
	colBgPage       = 0xF4F6FA
	colBgSidebar    = 0xF8F9FC
	colBgCard       = 0xFFFFFF
	colBgSubtle     = 0xEDF0F5
	colBgHover      = 0xE6EBF3
	colBgChip       = 0xF0F2F6
	colBorderLight  = 0xE2E6EE
	colTextPrimary  = 0x181B22
	colTextMuted    = 0x6B7280
	colTextSubtle   = 0x9CA3AF
	colPrimaryBlue  = 0x2577F5
	colPrimaryHover = 0x1A66DB
	colDarkBtn      = 0x181A1F
	colDarkBtnHover = 0x2D313A
	colGreenBadgeBg = 0xE6F7EE
	colGreenText    = 0x16A34A
	colRedBg        = 0xFEE2E2
	colRedText      = 0xDC2626
	colAndroidBadge = 0x0F2942
	colTabStripBg   = 0xEFF2F7
	colTabActiveBg  = 0xFFFFFF
	colBlack        = 0x000000
	colWhite        = 0xFFFFFF
)

var nextElemID atomic.Uint64

func init() {
	nextElemID.Store(1000)
}

// eid allocates a nonzero element ID. Dynamic UI trees use sequential IDs per
// snapshot; text inputs and scroll/focus targets use fixed stable IDs (<1000).
func eid() uint64 {
	return nextElemID.Add(1)
}

// encodeRGBAAsBMP encodes an *image.RGBA into an uncompressed top-down 32-bit
// BGRA BMP byte slice. This avoids PNG compression overhead on live emulator
// frame updates while decoding cleanly in Rust's image crate.
func encodeRGBAAsBMP(img *image.RGBA) []byte {
	if img == nil {
		return nil
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	const hdrSize = 14 + 40
	pixelBytes := w * h * 4
	buf := make([]byte, hdrSize+pixelBytes)

	// BITMAPFILEHEADER (14 bytes)
	buf[0] = 'B'
	buf[1] = 'M'
	binary.LittleEndian.PutUint32(buf[2:6], uint32(len(buf)))
	binary.LittleEndian.PutUint32(buf[10:14], hdrSize)

	// BITMAPINFOHEADER (40 bytes) — negative height indicates top-down scanlines.
	binary.LittleEndian.PutUint32(buf[14:18], 40)
	binary.LittleEndian.PutUint32(buf[18:22], uint32(w))
	binary.LittleEndian.PutUint32(buf[22:26], uint32(int32(-h)))
	binary.LittleEndian.PutUint16(buf[26:28], 1)  // planes
	binary.LittleEndian.PutUint16(buf[28:30], 32) // bpp (BGRA)
	binary.LittleEndian.PutUint32(buf[34:38], uint32(pixelBytes))

	dst := buf[hdrSize:]
	for y := 0; y < h; y++ {
		srcRow := img.Pix[(y+b.Min.Y-img.Rect.Min.Y)*img.Stride+(b.Min.X-img.Rect.Min.X)*4:]
		dstRow := dst[y*w*4:]
		for x := 0; x < w; x++ {
			si := x * 4
			dstRow[si+0] = srcRow[si+2] // B
			dstRow[si+1] = srcRow[si+1] // G
			dstRow[si+2] = srcRow[si+0] // R
			dstRow[si+3] = srcRow[si+3] // A
		}
	}
	return buf
}

// iconBank stores uploaded crisp SVG handles for the GPUI UI.
type iconBank struct {
	Logo      *client.Resource
	Device    *client.Resource
	Download  *client.Resource
	Remote    *client.Resource
	Tools     *client.Resource
	Settings  *client.Resource
	Plus      *client.Resource
	Batch     *client.Resource
	Arrange   *client.Resource
	Search    *client.Resource
	Sort      *client.Resource
	List      *client.Resource
	Grid      *client.Resource
	Play      *client.Resource
	Power     *client.Resource
	More      *client.Resource
	Home      *client.Resource
	Back      *client.Resource
	Keymap    *client.Resource
	Volume    *client.Resource
	Mute      *client.Resource
	Close     *client.Resource
	Refresh   *client.Resource
	Sync      *client.Resource
}

func uploadSVG(ctx context.Context, cli *client.Client, svg string) *client.Resource {
	res, err := cli.UploadResource(ctx, client.ResourceSVG, "image/svg+xml", []byte(svg))
	if err != nil {
		return nil
	}
	return res
}

func uploadPNG(ctx context.Context, cli *client.Client, pngBytes []byte) *client.Resource {
	if len(pngBytes) == 0 {
		return nil
	}
	res, err := cli.UploadResource(ctx, client.ResourceImage, "image/png", pngBytes)
	if err != nil {
		return nil
	}
	return res
}

func loadIconBank(ctx context.Context, cli *client.Client) *iconBank {
	return &iconBank{
		Logo:     uploadPNG(ctx, cli, generateNekoLogoPNG(128)),
		Device:   uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="6" y="2" width="12" height="20" rx="2"/><path d="M12 18h.01"/></svg>`),
		Download: uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>`),
		Remote:   uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="4" width="20" height="14" rx="2"/><path d="M8 21h8M12 18v3"/></svg>`),
		Tools:    uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg>`),
		Settings: uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>`),
		Plus:     uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M12 5v14M5 12h14"/></svg>`),
		Batch:    uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2L2 7l10 5 10-5-10-5z"/><path d="M2 12l10 5 10-5"/><path d="M2 17l10 5 10-5"/></svg>`),
		Arrange:  uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="8" height="8" rx="1"/><rect x="13" y="3" width="8" height="8" rx="1"/><rect x="3" y="13" width="8" height="8" rx="1"/><rect x="13" y="13" width="8" height="8" rx="1"/></svg>`),
		Search:   uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="7"/><path d="M21 21l-4.35-4.35"/></svg>`),
		Sort:     uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M3 6h13M3 12h9M3 18h5M17 6v12M17 18l-3-3M17 18l3-3"/></svg>`),
		List:     uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/></svg>`),
		Grid:     uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>`),
		Play:     uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><polygon points="7,5 19,12 7,19"/></svg>`),
		Power:    uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><path d="M18.36 6.64a9 9 0 1 1-12.73 0"/><line x1="12" y1="2" x2="12" y2="12"/></svg>`),
		More:     uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="5" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="12" cy="19" r="1.8"/></svg>`),
		Home:     uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 10l9-7 9 7v10a2 2 0 0 1-2 2h-4v-6H9v6H5a2 2 0 0 1-2-2z"/></svg>`),
		Back:     uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="17,5 6,12 17,19"/></svg>`),
		Keymap:   uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="5" width="20" height="14" rx="2"/><path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01M7 15h10"/></svg>`),
		Volume:   uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="11,5 6,9 2,9 2,15 6,15 11,19"/><path d="M15.54 8.46a5 5 0 0 1 0 7.07"/><path d="M19.07 4.93a10 10 0 0 1 0 14.14"/></svg>`),
		Mute:     uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="11,5 6,9 2,9 2,15 6,15 11,19"/><line x1="22" y1="9" x2="16" y2="15"/><line x1="16" y1="9" x2="22" y2="15"/></svg>`),
		Close:    uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>`),
		Refresh:  uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>`),
		Sync:     uploadSVG(ctx, cli, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="12" height="10" rx="2"/><path d="M9 18h10a2 2 0 0 0 2-2V8"/></svg>`),
	}
}

func svgIcon(res *client.Resource, size float32, colorHex uint32) *scene.Node {
	if res == nil {
		return scene.Div(eid()).Style(&scene.Style{Width: scene.Pixels(size), Height: scene.Pixels(size)})
	}
	return scene.Image(eid(), res.Handle()).Style(&scene.Style{
		Width:      scene.Pixels(size),
		Height:     scene.Pixels(size),
		TextColor:  scene.RGB(colorHex),
		FlexShrink: scene.Ptr[float32](0),
	})
}

func rasterIcon(res *client.Resource, size float32) *scene.Node {
	if res == nil {
		return scene.Div(eid()).Style(&scene.Style{Width: scene.Pixels(size), Height: scene.Pixels(size)})
	}
	return scene.Image(eid(), res.Handle()).Style(&scene.Style{
		Width:      scene.Pixels(size),
		Height:     scene.Pixels(size),
		FlexShrink: scene.Ptr[float32](0),
	})
}

func labelText(text string, size float32, colorHex uint32, bold bool) *scene.Node {
	weight := uint32(400)
	if bold {
		weight = 600
	}
	return scene.Text(eid(), text).Style(&scene.Style{
		FontSize:   scene.Ptr[float32](size),
		FontWeight: scene.Ptr[uint32](weight),
		TextColor:  scene.RGB(colorHex),
		FontFamily: scene.Ptr[string](uiFontFamily),
	})
}

func pillButton(label string, handler uint64, bgHex, hoverHex, textHex, borderHex uint32) *scene.Node {
	st := &scene.Style{
		Display:        scene.DisplayFlex.Enum(),
		FlexDirection:  scene.FlexRow.Enum(),
		AlignItems:     scene.AlignCenter.Enum(),
		JustifyContent: scene.JustifyCenter.Enum(),
		Padding:        &scene.Edges{Top: scene.Pixels(7), Bottom: scene.Pixels(7), Left: scene.Pixels(13), Right: scene.Pixels(13)},
		Gap:            scene.Pixels(6),
		Background:     scene.RGB(bgHex),
		CornerRadius:   scene.Ptr[float32](8),
		Cursor:         scene.CursorPointer.Enum(),
	}
	if borderHex != 0 {
		st.BorderWidth = scene.Ptr[float32](1)
		st.BorderColor = scene.RGB(borderHex)
	}
	n := scene.Div(eid(), labelText(label, 13, textHex, true)).
		Focusable(0).
		Accessible(label, &scene.Accessibility{Role: scene.RoleButton}).
		Style(st).
		Hover(&scene.Style{Background: scene.RGB(hoverHex)})
	if handler != 0 {
		n.OnClick(handler)
	}
	return n
}

func iconPillButton(icon *client.Resource, label string, handler uint64, bgHex, hoverHex, textHex, borderHex uint32) *scene.Node {
	st := &scene.Style{
		Display:        scene.DisplayFlex.Enum(),
		FlexDirection:  scene.FlexRow.Enum(),
		AlignItems:     scene.AlignCenter.Enum(),
		JustifyContent: scene.JustifyCenter.Enum(),
		Padding:        &scene.Edges{Top: scene.Pixels(7), Bottom: scene.Pixels(7), Left: scene.Pixels(12), Right: scene.Pixels(12)},
		Gap:            scene.Pixels(6),
		Background:     scene.RGB(bgHex),
		CornerRadius:   scene.Ptr[float32](8),
		Cursor:         scene.CursorPointer.Enum(),
	}
	if borderHex != 0 {
		st.BorderWidth = scene.Ptr[float32](1)
		st.BorderColor = scene.RGB(borderHex)
	}
	children := []*scene.Node{}
	if icon != nil {
		children = append(children, svgIcon(icon, 14, textHex))
	}
	if label != "" {
		children = append(children, labelText(label, 13, textHex, true))
	}
	n := scene.Div(eid(), children...).
		Focusable(0).
		Accessible(label, &scene.Accessibility{Role: scene.RoleButton}).
		Style(st).
		Hover(&scene.Style{Background: scene.RGB(hoverHex)})
	if handler != 0 {
		n.OnClick(handler)
	}
	return n
}
