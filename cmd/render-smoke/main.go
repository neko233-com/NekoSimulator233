// render-smoke renders an already-running instance in a hidden MyGo window.
// It never saves the frame to disk.
package main

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/NekoSimulator233/internal/avd"
)

func run() error {
	manager, err := avd.DiscoverManager()
	if err != nil {
		return err
	}
	if home := os.Getenv("NEKO_SMOKE_AVD_HOME"); home != "" {
		manager.Home = home
	}
	if name := os.Getenv("NEKO_SMOKE_AVD_NAME"); name != "" {
		manager.Name = name
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	instance, err := manager.Ready(ctx)
	if err != nil {
		return err
	}
	source, err := instance.Open()
	if err != nil {
		return err
	}
	frame, err := source.Capture(nil)
	source.Close()
	if err != nil {
		return err
	}
	bitmap := ui.NewBitmap(frame)
	result := make(chan error, 1)
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{Title: "Neko render smoke", Width: 640, Height: 480, Hidden: true, Content: ui.View(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { p.Image(bitmap, r, ui.Contain) })
		})})
		go func() {
			time.Sleep(time.Second)
			var checkErr error
			data, e := win.CapturePage()
			if e != nil {
				checkErr = e
			} else if img, e := png.Decode(bytes.NewReader(data)); e != nil {
				checkErr = e
			} else if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
				checkErr = fmt.Errorf("empty window capture")
			} else {
				r1, g1, b1, _ := frame.At(frame.Bounds().Dx()/2, frame.Bounds().Dy()/2).RGBA()
				r2, g2, b2, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()
				diff := func(a, b uint32) uint32 {
					if a > b {
						return a - b
					}
					return b - a
				}
				if diff(r1, r2) > 12000 || diff(g1, g2) > 12000 || diff(b1, b2) > 12000 {
					checkErr = fmt.Errorf("rendered center differs from captured frame")
				} else {
					fmt.Printf("rendered live frame %dx%d into hidden window %dx%d\n", frame.Bounds().Dx(), frame.Bounds().Dy(), img.Bounds().Dx(), img.Bounds().Dy())
				}
			}
			result <- checkErr
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	default:
		return fmt.Errorf("application exited before render check completed")
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
