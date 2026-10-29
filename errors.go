package httpc

import (
	"errors"

	"github.com/Albert-Zhan/httpc/body"
)

var (
	// ErrInvalidRequest 表示请求 URL、参数、方法或调用状态无效。
	ErrInvalidRequest = errors.New("httpc: invalid request")
	// ErrInvalidProxy 表示代理地址配置无效。
	ErrInvalidProxy = errors.New("httpc: invalid proxy")
	// ErrRequestBody 表示请求体构建或编码失败。
	ErrRequestBody = body.ErrRequestBody
	// ErrUploadFile 表示上传文件打开、读取或写入表单失败。
	ErrUploadFile = body.ErrUploadFile
	// ErrSendRequest 表示发送请求失败，包括连接、代理、TLS、取消和超时错误。
	ErrSendRequest = errors.New("httpc: send request failed")
	// ErrReadResponse 表示响应体读取失败。
	ErrReadResponse = errors.New("httpc: read response failed")
	// ErrDecodeResponse 表示 gzip 响应解压失败。
	ErrDecodeResponse = errors.New("httpc: decode response failed")
	// ErrSaveFile 表示下载文件创建、写入或关闭失败。
	ErrSaveFile = errors.New("httpc: save file failed")
	// ErrHTTPStatus 表示下载接口返回了非 200 状态码。
	// End 和 EndByte 不会因 HTTP 状态码而返回此错误。
	ErrHTTPStatus = errors.New("httpc: unexpected HTTP status")
)

// Error 描述 httpc 操作失败的类别、操作和底层原因。
// 使用 errors.Is(err, ErrXXX) 判断类别，errors.As 获取 *Error；
// Unwrap 保留底层错误，例如 *os.PathError、*url.Error 或 context.Canceled。
type Error struct {
	Op   string
	Kind error
	Err  error
}

func (e *Error) Error() string {
	message := "httpc"
	if e.Op != "" {
		message += ": " + e.Op
	}
	if e.Err != nil {
		return message + ": " + e.Err.Error()
	}
	if e.Kind != nil {
		return message + ": " + e.Kind.Error()
	}
	return message
}

// Is 允许 errors.Is 根据 Kind 判断 httpc 错误类别。
func (e *Error) Is(target error) bool { return errors.Is(e.Kind, target) }

// Unwrap 返回原始错误，保留标准库及调用者的错误链。
func (e *Error) Unwrap() error { return e.Err }

func wrapError(kind error, op string, err error) error {
	return &Error{Kind: kind, Op: op, Err: err}
}
