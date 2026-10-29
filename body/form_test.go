package body

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"
)

type failingWriter struct {
	err         error
	onlyPayload bool
}

func (w failingWriter) Write(p []byte) (int, error) {
	if !w.onlyPayload || bytes.Equal(p, []byte("upload content")) {
		return 0, w.err
	}
	return len(p), nil
}

func TestFormRetainsFirstError(t *testing.T) {
	f := NewFormData().SetBoundary("invalid\nboundary")
	first := f.Err()
	f.SetFile("file", filepath.Join(t.TempDir(), "missing")).SetData("field", "value")
	if first == nil || f.Err() != first || !errors.Is(first, ErrRequestBody) {
		t.Fatalf("first error was not retained: %v", f.Err())
	}
	_, err := io.ReadAll(f.Encode())
	if !errors.Is(err, ErrRequestBody) {
		t.Fatalf("Encode reader did not return the stored error: %v", err)
	}
}

func TestFormPropagatesWriterErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(file, []byte("upload content"), 0600); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("multipart writer failed")
	for _, tc := range []struct {
		name        string
		onlyPayload bool
		operation   func(*Form)
		kind        error
	}{
		{"field", false, func(f *Form) { f.SetData("key", "value") }, ErrRequestBody},
		{"file header", false, func(f *Form) { f.SetFile("file", file) }, ErrUploadFile},
		{"file copy", true, func(f *Form) { f.SetFile("file", file) }, ErrUploadFile},
		{"close writer", false, func(f *Form) { f.Encode() }, ErrRequestBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFormData()
			f.data = multipart.NewWriter(failingWriter{err: cause, onlyPayload: tc.onlyPayload})
			tc.operation(f)
			if !errors.Is(f.Err(), tc.kind) || !errors.Is(f.Err(), cause) {
				t.Fatalf("Form.Err = %v, want category %v and original cause", f.Err(), tc.kind)
			}
		})
	}
}

func TestFormEncodesUpload(t *testing.T) {
	file := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(file, []byte("upload content"), 0600); err != nil {
		t.Fatal(err)
	}
	f := NewFormData().SetBoundary("httpc-test-boundary").SetData("key", "one").SetData("key", "two").SetFile("file", file)
	reader := multipart.NewReader(f.Encode(), "httpc-test-boundary")
	parsed, err := reader.ReadForm(1024)
	if err != nil {
		t.Fatal(err)
	}
	defer parsed.RemoveAll()
	if f.Err() != nil || len(parsed.Value["key"]) != 2 || len(parsed.File["file"]) != 1 {
		t.Fatalf("invalid form: values=%v, files=%v, error=%v", parsed.Value, parsed.File, f.Err())
	}
	upload, err := parsed.File["file"][0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer upload.Close()
	content, err := io.ReadAll(upload)
	if err != nil || string(content) != "upload content" {
		t.Fatalf("upload content = %q, error = %v", content, err)
	}
	f.SetData("late", "value")
	if !errors.Is(f.Err(), ErrRequestBody) {
		t.Fatal("modifying an encoded form did not report an error")
	}
}
