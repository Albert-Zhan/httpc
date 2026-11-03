package httpc_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/Albert-Zhan/httpc"
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
