package main

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
)

// ensureThumb captures one preview frame for a running instance. MuMu shows a
// live device thumbnail on each card; Neko fills it lazily and keeps the last
// frame so stopped cards keep their preview.
func (a *app) ensureThumb(name string) {
	if a.thumbs == nil {
		a.thumbs = map[string]*ui.Bitmap{}
	}
	if a.thumbPending == nil {
		a.thumbPending = map[string]bool{}
	}
	if a.thumbs[name] != nil || a.thumbPending[name] {
		return
	}
	a.thumbPending[name] = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		manager, err := selectedManager(name)
		var frame *ui.Bitmap
		if err == nil {
			instance, readyErr := manager.Ready(ctx)
			err = readyErr
			if err == nil {
				controller, openErr := instance.Open()
				err = openErr
				if err == nil {
					img, captureErr := controller.Capture(nil)
					err = captureErr
					if err == nil {
						frame = ui.NewBitmap(img)
					}
					controller.Close()
				}
			}
		}
		a.window.Update(func() {
			delete(a.thumbPending, name)
			if err == nil && frame != nil {
				a.thumbs[name] = frame
			}
		})
	}()
}

func (a *app) thumbFor(name string) *ui.Bitmap {
	if a.thumbs == nil {
		return nil
	}
	return a.thumbs[name]
}
