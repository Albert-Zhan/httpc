package body

import (
	"io"
	"net/url"
	"reflect"
	"testing"
)

var _ Body = (*URL)(nil)

func TestURLFormEncoding(t *testing.T) {
	for _, tc := range []struct {
		name   string
		create func() *URL
	}{
		{"constructor", NewURLEncode},
		{"zero value", func() *URL { return &URL{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.create()
			empty, err := io.ReadAll(data.Encode())
			if err != nil || len(empty) != 0 {
				t.Fatalf("empty form=%q error=%v", empty, err)
			}
			if data.SetData("q", "go & http").SetData("q", "中文") != data {
				t.Fatal("setter changed form instance")
			}
			for range 2 {
				encoded, err := io.ReadAll(data.Encode())
				if err != nil {
					t.Fatal(err)
				}
				parsed, err := url.ParseQuery(string(encoded))
				if err != nil || !reflect.DeepEqual(parsed["q"], []string{"go & http", "中文"}) || data.GetContentType() != "application/x-www-form-urlencoded" {
					t.Fatalf("form=%q error=%v", encoded, err)
				}
			}
		})
	}
}
