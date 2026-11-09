package config

import (
	"fbc/common"
	"os"
	"path/filepath"
	"testing"
)

func TestCrossInkConfiguration(t *testing.T) {
	for _, mode := range []string{"fit", "none", "stretch"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		data := "document:\n  empty_line_as_br: true\n  annotation:\n    include_year: true\n  images:\n    resize: " + mode + "\n    baseline_jpeg: true\n    cover:\n      resize: crop\n"
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfiguration(path)
		if mode == "stretch" {
			if err == nil {
				t.Fatal("invalid body resize accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.Document.EmptyLineAsBR || !cfg.Document.Annotation.IncludeYear || !cfg.Document.Images.BaselineJPEG || cfg.Document.Images.Cover.Resize != common.ImageResizeModeCrop || cfg.Document.Images.Resize != mode {
			t.Fatal("new options did not load")
		}
	}
	cfg, err := LoadConfiguration("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Document.EmptyLineAsBR || cfg.Document.Annotation.IncludeYear || cfg.Document.Images.BaselineJPEG || cfg.Document.Images.Resize != "none" {
		t.Fatal("new behavior enabled by default")
	}
}
