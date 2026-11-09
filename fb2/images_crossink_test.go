package fb2

import (
	"bytes"
	"fbc/common"
	"fbc/config"
	"fmt"
	"go.uber.org/zap"
	"image"
	"os"
	"testing"
)

func TestCrossInkImageSizing(t *testing.T) {
	for _, tt := range []struct {
		name                       string
		cover                      bool
		mode                       common.ImageResizeMode
		resize                     string
		w, h, sw, sh, wantW, wantH int
	}{
		{"portrait-cover", true, common.ImageResizeModeCrop, "", 1200, 1500, 528, 792, 634, 792},
		{"narrow-cover", true, common.ImageResizeModeCrop, "", 600, 1000, 528, 792, 528, 880},
		{"landscape-cover", true, common.ImageResizeModeCrop, "", 800, 600, 528, 792, 1056, 792},
		{"exact-cover", true, common.ImageResizeModeCrop, "", 1000, 1500, 528, 792, 528, 792},
		{"small-cover", true, common.ImageResizeModeCrop, "", 200, 300, 528, 792, 528, 792},
		{"different-screen", true, common.ImageResizeModeCrop, "", 1200, 1500, 600, 800, 640, 800},
		{"body-fit", false, common.ImageResizeModeNone, "fit", 1200, 1500, 528, 792, 528, 660},
		{"body-tall", false, common.ImageResizeModeNone, "fit", 600, 1800, 528, 792, 264, 792},
		{"body-wide", false, common.ImageResizeModeNone, "fit", 1200, 600, 528, 792, 528, 264},
		{"body-small", false, common.ImageResizeModeNone, "fit", 100, 100, 528, 792, 100, 100},
		{"body-exact", false, common.ImageResizeModeNone, "fit", 528, 792, 528, 792, 528, 792},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := createTestJPEG(t, tt.w, tt.h, 90)
			cfg := config.ImagesConfig{Resize: tt.resize, ScaleFactor: 2, JPEGQuality: 95, Screen: config.ScreenConfig{Width: tt.sw, Height: tt.sh, DPI: 200}, Cover: config.CoverConfig{Resize: tt.mode}}
			bo := BinaryObject{ID: "test", ContentType: "image/jpeg", Data: data}
			got := bo.PrepareImage(common.OutputFmtEpub2, tt.cover, &cfg, zap.NewNop())
			d, _, err := image.DecodeConfig(bytes.NewReader(got.Data))
			if err != nil {
				t.Fatal(err)
			}
			if d.Width != tt.wantW || d.Height != tt.wantH {
				t.Fatalf("got %dx%d want %dx%d", d.Width, d.Height, tt.wantW, tt.wantH)
			}
			if !tt.cover && tt.w <= tt.sw && tt.h <= tt.sh && !bytes.Equal(data, got.Data) {
				t.Fatal("small image was re-encoded")
			}
		})
	}
}

func TestCrossInkBaselineWithoutResize(t *testing.T) {
	progressive, err := os.ReadFile("testdata/crossink-progressive.jpg")
	if err != nil {
		t.Fatal(err)
	}
	baseline := createTestJPEG(t, 96, 64, 80)
	for _, enabled := range []bool{false, true} {
		for _, cover := range []bool{false, true} {
			for _, data := range [][]byte{progressive, baseline} {
				cfg := config.ImagesConfig{BaselineJPEG: enabled, JPEGQuality: 95, ScaleFactor: 1, Screen: config.ScreenConfig{Width: 528, Height: 792, DPI: 200}}
				bo := BinaryObject{ID: "jpeg", ContentType: "image/jpeg", Data: data}
				got := bo.PrepareImage(common.OutputFmtEpub2, cover, &cfg, zap.NewNop())
				if enabled && !isBaselineJPEG(got.Data) {
					t.Fatal("non-baseline JPEG survived")
				}
				if (!enabled || bytes.Equal(data, baseline)) && !bytes.Equal(data, got.Data) {
					t.Fatal("unnecessary re-encoding")
				}
				d, _, err := image.DecodeConfig(bytes.NewReader(got.Data))
				if err != nil || d.Width != 96 || d.Height != 64 {
					t.Fatalf("dimensions changed or invalid JPEG: %+v %v", d, err)
				}
			}
		}
	}
}

func TestCrossInkJPEGMarkerParser(t *testing.T) {
	// APP payload contains a fake SOF0: it must not hide the real progressive SOF2.
	if isBaselineJPEG([]byte{0xff, 0xd8, 0xff, 0xe0, 0, 4, 0xff, 0xc0, 0xff, 0xc2}) {
		t.Fatal("parsed APP payload as a marker")
	}
	for _, b := range [][]byte{nil, {0xff}, {0xff, 0xd8, 0xff}, {0xff, 0xd8, 0xff, 0xe0, 0, 1}} {
		if isBaselineJPEG(b) {
			t.Fatal("accepted invalid JPEG")
		}
	}
}

func TestCrossInkSVGFitAndCover(t *testing.T) {
	for _, raster := range []bool{false, true} {
		for _, tt := range []struct {
			w, h         int
			cover        bool
			wantW, wantH int
		}{
			{100, 100, false, 100, 100}, {1200, 1500, false, 528, 660}, {600, 1000, true, 528, 880},
		} {
			data := []byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d"><rect width="100%%" height="100%%" fill="red"/></svg>`, tt.w, tt.h, tt.w, tt.h))
			cfg := config.ImagesConfig{Resize: "fit", RasterizeSVG: raster, ScaleFactor: 2, JPEGQuality: 95, Screen: config.ScreenConfig{Width: 528, Height: 792}, Cover: config.CoverConfig{Resize: common.ImageResizeModeCrop}}
			bo := BinaryObject{ID: "svg", ContentType: "image/svg+xml", Data: data}
			got := bo.PrepareImage(common.OutputFmtEpub2, tt.cover, &cfg, zap.NewNop())
			if got.Dim.Width != tt.wantW || got.Dim.Height != tt.wantH {
				t.Fatalf("raster=%v cover=%v: %+v", raster, tt.cover, got.Dim)
			}
			if !raster && !tt.cover && tt.w == 100 && !bytes.Equal(data, got.Data) {
				t.Fatal("small native SVG changed")
			}
		}
	}
}
