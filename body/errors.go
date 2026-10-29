package body

import "errors"

var (
	// ErrRequestBody 表示请求体构建或编码失败，也可通过 httpc.ErrRequestBody 判断。
	ErrRequestBody = errors.New("httpc: request body failed")
	// ErrUploadFile 表示上传文件处理失败，也可通过 httpc.ErrUploadFile 判断。
	ErrUploadFile = errors.New("httpc: upload file failed")
)

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }
