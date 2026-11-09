package epub

import (
	"fbc/common"
	"fbc/config"
	"fbc/content"
	"fbc/fb2"
	"github.com/beevik/etree"
	"go.uber.org/zap"
	"testing"
	"time"
)

func TestCrossInkAnnotationYear(t *testing.T) {
	for _, tt := range []struct {
		date *fb2.Date
		want string
	}{
		{nil, ""}, {&fb2.Date{}, ""}, {&fb2.Date{Display: "1962"}, "1962"},
		{&fb2.Date{Display: "1962 г."}, "1962"}, {&fb2.Date{Display: "1962-1963"}, ""},
		{&fb2.Date{Display: "unknown"}, ""},
		{&fb2.Date{Display: "wrong 1999", Value: time.Date(1962, 10, 1, 0, 0, 0, 0, time.UTC)}, "1962"},
	} {
		if got := annotationYear(tt.date); got != tt.want {
			t.Fatalf("%+v: %q != %q", tt.date, got, tt.want)
		}
	}
	for _, enabled := range []bool{false, true} {
		c := &content.Content{OutputFormat: common.OutputFmtEpub2, IDsIndex: make(fb2.IDIndex), Book: &fb2.FictionBook{}}
		c.Book.Description.TitleInfo.Date = &fb2.Date{Display: "1962"}
		c.Book.Description.TitleInfo.Annotation = &fb2.Flow{}
		result := generateAnnotation(c, &config.AnnotationConfig{Title: "Annotation", IncludeYear: enabled}, zap.NewNop())
		elements := result.Doc.FindElements("//p[@class='annotation-year']")
		if enabled {
			if len(elements) != 1 || elements[0].Text() != "1962" {
				t.Fatal("year not emitted")
			}
		} else if len(elements) != 0 {
			t.Fatal("year emitted when disabled")
		}
	}
}

func TestCrossInkConsecutiveEmptyLines(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		c := &content.Content{EmptyLineAsBR: enabled}
		parent := etree.NewElement("div")
		items := []fb2.FlowItem{{Kind: fb2.FlowEmptyLine}, {Kind: fb2.FlowEmptyLine}, {Kind: fb2.FlowEmptyLine}}
		_, err := appendFlowItems(parent, c, items, 1, 1, "section", zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		children := parent.ChildElements()
		if len(children) != 3 {
			t.Fatalf("lost empty lines: %d", len(children))
		}
		tag := "div"
		if enabled {
			tag = "br"
		}
		for _, el := range children {
			if el.Tag != tag || el.SelectAttrValue("class", "") != "emptyline" {
				t.Fatal("wrong empty line representation")
			}
		}
	}
}
