package body

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRawEncodesTextAndBinary(t *testing.T) {
	for _, tc := range []struct {
		name string
		data any
		want []byte
	}{
		{"string", "hello 中文", []byte("hello 中文")},
		{"bytes", []byte{0x00, 0xff, 0xfe, 0x80, 'a'}, []byte{0x00, 0xff, 0xfe, 0x80, 'a'}},
		{"binary string", string([]byte{0x00, 0xff}), []byte{0x00, 0xff}},
		{"nil", nil, nil},
		{"nil bytes", []byte(nil), nil},
		{"empty bytes", []byte{}, nil},
		{"empty string", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := NewRawData()
			raw.SetData(tc.data, Protobuf)
			if raw.Err() != nil {
				t.Fatal(raw.Err())
			}
			for range 2 {
				got, err := io.ReadAll(raw.Encode())
				if err != nil || !bytes.Equal(got, tc.want) {
					t.Fatalf("encoded=%x want=%x error=%v", got, tc.want, err)
				}
			}
		})
	}
}

func TestRawCopiesBytesAndPreservesValueSemantics(t *testing.T) {
	data := []byte{0, 0xff, 'a'}
	want := bytes.Clone(data)
	raw := NewRawData()
	raw.SetData(data, Protobuf)
	copied := *raw
	data[0] = 42
	data[2] = 'z'
	got, err := io.ReadAll(raw.Encode())
	if err != nil || !bytes.Equal(got, want) || *raw != copied {
		t.Fatalf("input changed saved data: %x, %v", got, err)
	}
	raw.SetData("replacement", Text)
	got, err = io.ReadAll(copied.Encode())
	if err != nil || !bytes.Equal(got, want) || *raw == copied {
		t.Fatalf("replacement changed a value copy: %x, %v", got, err)
	}
}

func TestRawRejectsUnsupportedTypesAndRecovers(t *testing.T) {
	for _, data := range []any{42, true, map[string]string{"key": "value"}, struct{ Name string }{"value"}, strings.NewReader("reader")} {
		raw := NewRawData()
		raw.SetData("previous content", Text)
		raw.SetData(data, Protobuf)
		if !errors.Is(raw.Err(), ErrRequestBody) {
			t.Fatalf("unsupported type %T was accepted: %v", data, raw.Err())
		}
		got, err := io.ReadAll(raw.Encode())
		if !errors.Is(err, ErrRequestBody) || len(got) != 0 {
			t.Fatalf("invalid body returned stale content: %q, %v", got, err)
		}
		raw.SetData([]byte{0, 0xff}, Protobuf)
		got, err = io.ReadAll(raw.Encode())
		if raw.Err() != nil || err != nil || !bytes.Equal(got, []byte{0, 0xff}) {
			t.Fatalf("valid replacement did not recover: %x, %v", got, err)
		}
	}
}

func TestRawEmptyStringAndFormatRemainValid(t *testing.T) {
	raw := NewRawData()
	raw.SetData("", "")
	got, err := io.ReadAll(raw.Encode())
	if raw.Err() != nil || err != nil || len(got) != 0 || raw.GetContentType() != "" {
		t.Fatalf("empty data or format rejected: %q, %v", got, err)
	}
}
