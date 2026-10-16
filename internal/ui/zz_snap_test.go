package ui

import (
	"image"
	"image/png"
	"os"
	"testing"
	"time"
)

func crop(src image.Image, x0, y0, w, h, sc int) image.Image {
	out := image.NewRGBA(image.Rect(0, 0, w*sc, h*sc))
	for y := 0; y < h*sc; y++ {
		for x := 0; x < w*sc; x++ {
			out.Set(x, y, src.At(x0+x/sc, y0+y/sc))
		}
	}
	return out
}

func save(name string, img image.Image) {
	f, _ := os.Create(os.Getenv("SNAPDIR") + "/" + name)
	png.Encode(f, img)
	f.Close()
}

func TestZZSnap(t *testing.T) {
	for _, dark := range []bool{false, true} {
		e, _ := newTablesEnv(t)
		e.tt.SetDark(dark)
		doubleClickText(t, e.tt, "public")
		e.runPost()
		doubleClickText(t, e.tt, "users")
		e.runPost()
		e.runPost()
		doubleClickText(t, e.tt, "audit")
		e.runPost()
		e.tt.Advance(time.Second)
		e.tt.Frame()
		doubleClickText(t, e.tt, "log")
		e.runPost()
		e.runPost()
		e.tt.Advance(time.Second)
		e.tt.Frame()
		e.tt.Frame()
		name := "tb-light"
		if dark {
			name = "tb-dark"
		}
		save(name+"-1.png", crop(e.tt.Image(), 262, 48, 1018, 76, 2))
		// hover the close button of the first tab: find its label
		if r, ok := e.tt.Find("Close users"); ok {
			x, y := center(r)
			e.tt.Move(x, y)
			e.tt.Frame()
			e.tt.Frame()
			save(name+"-hover.png", crop(e.tt.Image(), 262, 48, 400, 40, 4))
		} else {
			t.Log("no Close users label")
		}
	}
}

