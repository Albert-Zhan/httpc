package body

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func streamUploadFile(t *testing.T, name, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStreamFormMatchesBufferedEncodingAndReplays(t *testing.T) {
	file := streamUploadFile(t, "upload.txt", "file contents")
	empty := streamUploadFile(t, "empty.txt", "")
	stream := NewStreamFormData().SetBoundary("httpc stream boundary")
	buffered := NewFormData().SetBoundary("httpc stream boundary")
	stream.SetData("key", "one").SetFile("file", file).SetData("key", "two").SetFile("empty", empty).SetData("last", "value")
	buffered.SetData("key", "one").SetFile("file", file).SetData("key", "two").SetFile("empty", empty).SetData("last", "value")
	want, err := io.ReadAll(buffered.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if stream.ContentLength() != -1 {
		t.Fatal("unencoded form has a known length")
	}
	for range 2 {
		reader, ok := stream.Encode().(io.ReadCloser)
		if !ok {
			t.Fatalf("stream encoding failed: %v", stream.Err())
		}
		copy, err := stream.GetBody()
		if err != nil {
			t.Fatal(err)
		}
		first := make([]byte, 17)
		if _, err := io.ReadFull(reader, first); err != nil {
			t.Fatal(err)
		}
		other, err := io.ReadAll(copy)
		_ = copy.Close()
		if err != nil || !bytes.Equal(other, want) {
			t.Fatalf("independent replay differs: %q, %v", other, err)
		}
		rest, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil || !bytes.Equal(append(first, rest...), want) {
			t.Fatalf("stream differs from buffered form: %v", err)
		}
	}
	if stream.ContentLength() != int64(len(want)) || stream.GetContentType() != buffered.GetContentType() {
		t.Fatalf("incorrect metadata: length=%d, type=%s", stream.ContentLength(), stream.GetContentType())
	}
	for _, stream := range []*StreamForm{NewStreamFormData(), NewStreamFormData().SetData("field", "value")} {
		reader := stream.Encode().(io.ReadCloser)
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil || int64(len(data)) != stream.ContentLength() {
			t.Fatalf("file-free form has incorrect length: %v", err)
		}
	}
}

func TestStreamFormBuildErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	for _, tc := range []struct {
		name string
		form *StreamForm
		kind error
	}{
		{"boundary", NewStreamFormData().SetBoundary("bad\nboundary"), ErrRequestBody},
		{"late boundary", NewStreamFormData().SetData("key", "value").SetBoundary("late"), ErrRequestBody},
		{"missing", NewStreamFormData().SetFile("file", missing), ErrUploadFile},
		{"directory", NewStreamFormData().SetFile("file", t.TempDir()), ErrUploadFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := io.ReadAll(tc.form.Encode())
			if !errors.Is(err, tc.kind) || err != tc.form.Err() || tc.form.ContentLength() != -1 {
				t.Fatalf("wrong build error: %v", err)
			}
			if tc.name == "missing" && !errors.Is(err, os.ErrNotExist) {
				t.Fatal("lost original filesystem error")
			}
			first := tc.form.Err()
			tc.form.SetData("other", "value").SetFile("file", missing)
			if tc.form.Err() != first {
				t.Fatal("first error changed")
			}
		})
	}
	f := NewStreamFormData()
	reader := f.Encode().(io.ReadCloser)
	_ = reader.Close()
	f.SetData("late", "value")
	if !errors.Is(f.Err(), ErrRequestBody) {
		t.Fatal("encoded form allowed modification")
	}
}

func TestStreamFormLazilyOpensAndClosesFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(64 << 20); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	_ = file.Close()
	f := NewStreamFormData().SetFile("first", path).SetFile("second", path)
	r := f.Encode().(*streamFormReader)
	defer r.Close()
	if f.ContentLength() <= 128<<20 || len(f.plan.pieces[0].prefix) > 1024 {
		t.Fatal("file was buffered or length omitted multipart framing")
	}
	if r.files[0].file != nil || r.files[1].file != nil {
		t.Fatal("encoding opened files")
	}
	if _, err := io.CopyN(io.Discard, r, int64(len(f.plan.pieces[0].prefix))+1); err != nil {
		t.Fatal(err)
	}
	opened := r.files[0].file
	if opened == nil || r.files[1].file != nil {
		t.Fatal("files were not opened one at a time")
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = io.CopyBuffer(io.Discard, r, make([]byte, 512))
	}()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	<-finished
	if _, err := opened.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("active file remained open: %v", err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("closed stream can still be read: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStreamFormFileChangesAndTruncation(t *testing.T) {
	for _, remove := range []bool{false, true} {
		file := streamUploadFile(t, "source", "original contents")
		f := NewStreamFormData().SetFile("file", file)
		reader := f.Encode().(io.ReadCloser)
		if remove {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Truncate(file, 1); err != nil {
			t.Fatal(err)
		}
		_, err := io.ReadAll(reader)
		_ = reader.Close()
		if !errors.Is(err, ErrUploadFile) || (remove && !errors.Is(err, os.ErrNotExist)) {
			t.Fatalf("changed file did not report upload error: %v", err)
		}
	}
	file := streamUploadFile(t, "source", "original contents")
	f := NewStreamFormData().SetFile("file", file)
	reader := f.Encode().(*streamFormReader)
	defer reader.Close()
	if _, err := io.CopyN(io.Discard, reader, int64(len(f.plan.pieces[0].prefix))+1); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(file, 1); err != nil {
		t.Fatal(err)
	}
	_, err := io.ReadAll(reader)
	if !errors.Is(err, ErrUploadFile) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("mid-stream truncation lost its cause: %v", err)
	}
	if reader.files[0].file != nil {
		t.Fatal("failed read left a file open")
	}
}
