package httpc_test

import (
	"errors"
	"fmt"

	"github.com/Albert-Zhan/httpc"
)

func ExampleError() {
	client := httpc.NewHttpClient().SetProxy("://invalid-proxy")
	_, _, err := httpc.NewRequest(client).SetUrl("http://example.test/").Send().End()
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
