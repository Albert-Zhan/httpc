package downloader_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/Albert-Zhan/httpc/v2"
	"github.com/Albert-Zhan/httpc/v2/downloader"
)

func ExampleDownloader_Download() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "file contents") }))
	defer server.Close()
	dir, err := os.MkdirTemp("", "httpc-example-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(dir)
	client := httpc.NewClient().SetTimeout(0)
	defer client.CloseIdleConnections()
	d, err := downloader.New(client, downloader.Options{Workers: 4, MaxBytes: 1 << 20})
	if err != nil {
		fmt.Println(err)
		return
	}
	result, err := d.Download(context.Background(), server.URL, filepath.Join(dir, "file.txt"))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(result.Mode, result.Size)
	// Output: single 13
}
