package httpc_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/Albert-Zhan/httpc/v2"
	"github.com/Albert-Zhan/httpc/v2/body"
)

func ExampleError() {
	client := httpc.NewClient().SetProxy("://invalid-proxy")
	_, _, err := httpc.NewRequest(client).SetURL("http://example.test/").Send().End()
	fmt.Println(errors.Is(err, httpc.ErrInvalidProxy))
	fmt.Println(errors.Is(err, httpc.ErrSendRequest))
	var detail *httpc.Error
	if errors.As(err, &detail) {
		fmt.Println(detail.Op)
	}
	// Output:
	// true
	// true
	// send request
}

func ExampleRequest_EndTo() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello httpc")
	}))
	defer server.Close()
	client := httpc.NewClient()
	defer client.CloseIdleConnections()
	var output strings.Builder
	resp, err := httpc.NewRequest(client).SetURL(server.URL).
		SetResponseLimit(1024).Send().EndTo(&output)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(resp.StatusCode)
	fmt.Println(output.String())
	// Output:
	// 200
	// hello httpc
}

type appHeaderTransport struct{ next http.RoundTripper }

func (t *appHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	changed := req.Clone(req.Context())
	changed.Header.Set("X-Application", "demo")
	return t.next.RoundTrip(changed)
}

func ExampleClient_WrapTransport() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("X-Application"))
	}))
	defer server.Close()
	client := httpc.NewClient().WrapTransport(func(next http.RoundTripper) http.RoundTripper {
		return &appHeaderTransport{next: next}
	})
	defer client.CloseIdleConnections()
	_, text, err := httpc.NewRequest(client).SetURL(server.URL).Send().End()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(text)
	// Output:
	// demo
}

func ExampleRequest_streamUpload() {
	dir, err := os.MkdirTemp("", "httpc-upload-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello upload"), 0600); err != nil {
		fmt.Println(err)
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		defer file.Close()
		defer r.MultipartForm.RemoveAll()
		_, _ = io.Copy(w, file)
	}))
	defer server.Close()
	client := httpc.NewClient()
	defer client.CloseIdleConnections()
	form := body.NewStreamFormData().SetData("description", "demo").SetFile("file", path)
	resp, text, err := httpc.NewRequest(client).SetMethod("POST").SetURL(server.URL).SetBody(form).Send().End()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(resp.StatusCode, text)
	// Output:
	// 200 hello upload
}
