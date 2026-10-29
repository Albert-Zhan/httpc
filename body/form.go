package body

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
)

// Form 用于构建 multipart/form-data 类型的表单请求体
// 支持添加普通字段与文件字段，并最终编码为可用于 HTTP 请求的 io.Reader
// Form 包含可变构建状态，不支持并发修改或在多个请求之间并发共享。
type Form struct {
	dataBuf *bytes.Buffer
	data    *multipart.Writer
	err     error
	closed  bool
}

// NewFormData 创建一个新的 Form 实例
// 返回的 Form 可用于设置字段、文件内容并生成 multipart/form-data 请求体
func NewFormData() *Form {
	bodyBuf := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(bodyBuf)
	return &Form{
		dataBuf: bodyBuf,
		data:    bodyWriter,
	}
}

// SetBoundary 手动设置 multipart/form-data 边界值
// 一般情况下不需要手动设置边界，除非对外兼容性有特殊要求
func (f *Form) SetBoundary(boundary string) *Form {
	if !f.canWrite() {
		return f
	}
	if err := f.data.SetBoundary(boundary); err != nil {
		f.setError(ErrRequestBody, "set multipart boundary", err)
	}
	return f
}

// SetData 向表单中添加一个普通字段（name=value）
// 参数 name 表示字段名，value 表示字段内容
// 支持多次链式调用
func (f *Form) SetData(name, value string) *Form {
	if !f.canWrite() {
		return f
	}
	if err := f.data.WriteField(name, value); err != nil {
		f.setError(ErrRequestBody, "write multipart field", err)
	}
	return f
}

// SetFile 向表单中添加一个文件字段
// 参数 name 为表单字段名，file 为本地文件路径
// 支持多次链式调用
func (f *Form) SetFile(name, file string) *Form {
	if !f.canWrite() {
		return f
	}
	fd, err := os.Open(file)
	if err != nil {
		f.setError(ErrUploadFile, "open upload file", err)
		return f
	}
	defer func() {
		if err := fd.Close(); err != nil {
			f.setError(ErrUploadFile, "close upload file", err)
		}
	}()
	fileWriter, err := f.data.CreateFormFile(name, filepath.Base(file))
	if err != nil {
		f.setError(ErrUploadFile, "create multipart file field", err)
		return f
	}
	if _, err := io.Copy(fileWriter, fd); err != nil {
		f.setError(ErrUploadFile, "copy upload file", err)
	}
	return f
}

// Err 返回构建或编码时遇到的第一个错误；发生错误后不再继续修改表单。
func (f *Form) Err() error { return f.err }

func (f *Form) setError(kind error, op string, err error) {
	if f.err == nil {
		f.err = fmt.Errorf("%w: %s: %w", kind, op, err)
	}
}

func (f *Form) canWrite() bool {
	if f.err != nil {
		return false
	}
	if f.closed {
		f.setError(ErrRequestBody, "modify multipart form", fmt.Errorf("form is already encoded"))
		return false
	}
	return true
}

// GetContentType 返回该表单对应的 Content-Type，包含 boundary
func (f *Form) GetContentType() string {
	return f.data.FormDataContentType()
}

// Encode 关闭 multipart.Writer 并返回完整的 multipart/form-data 编码内容
// 返回的 io.Reader 可直接作为 HTTP 请求 body
// 注意：调用 Encode() 后将无法再次向 Form 中添加字段
func (f *Form) Encode() io.Reader {
	if f.err == nil && !f.closed {
		if err := f.data.Close(); err != nil {
			f.setError(ErrRequestBody, "encode multipart form", err)
		}
		f.closed = true
	}
	if f.err != nil {
		return errorReader{err: f.err}
	}
	return bytes.NewReader(f.dataBuf.Bytes())
}
