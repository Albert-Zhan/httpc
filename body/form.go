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
func (this *Form) SetBoundary(boundary string) *Form {
	if !this.canWrite() {
		return this
	}
	if err := this.data.SetBoundary(boundary); err != nil {
		this.setError(ErrRequestBody, "set multipart boundary", err)
	}
	return this
}

// SetData 向表单中添加一个普通字段（name=value）
// 参数 name 表示字段名，value 表示字段内容
// 支持多次链式调用
func (this *Form) SetData(name, value string) *Form {
	if !this.canWrite() {
		return this
	}
	if err := this.data.WriteField(name, value); err != nil {
		this.setError(ErrRequestBody, "write multipart field", err)
	}
	return this
}

// SetFile 向表单中添加一个文件字段
// 参数 name 为表单字段名，file 为本地文件路径
// 支持多次链式调用
func (this *Form) SetFile(name, file string) *Form {
	if !this.canWrite() {
		return this
	}
	fd, err := os.Open(file)
	if err != nil {
		this.setError(ErrUploadFile, "open upload file", err)
		return this
	}
	defer func() {
		if err := fd.Close(); err != nil {
			this.setError(ErrUploadFile, "close upload file", err)
		}
	}()
	fileWriter, err := this.data.CreateFormFile(name, filepath.Base(file))
	if err != nil {
		this.setError(ErrUploadFile, "create multipart file field", err)
		return this
	}
	if _, err := io.Copy(fileWriter, fd); err != nil {
		this.setError(ErrUploadFile, "copy upload file", err)
	}
	return this
}

// Err 返回构建或编码时遇到的第一个错误；发生错误后不再继续修改表单。
func (this *Form) Err() error { return this.err }

func (this *Form) setError(kind error, op string, err error) {
	if this.err == nil {
		this.err = fmt.Errorf("%w: %s: %w", kind, op, err)
	}
}

func (this *Form) canWrite() bool {
	if this.err != nil {
		return false
	}
	if this.closed {
		this.setError(ErrRequestBody, "modify multipart form", fmt.Errorf("form is already encoded"))
		return false
	}
	return true
}

// GetContentType 返回该表单对应的 Content-Type，包含 boundary
func (this *Form) GetContentType() string {
	return this.data.FormDataContentType()
}

// Encode 关闭 multipart.Writer 并返回完整的 multipart/form-data 编码内容
// 返回的 io.Reader 可直接作为 HTTP 请求 body
// 注意：调用 Encode() 后将无法再次向 Form 中添加字段
func (this *Form) Encode() io.Reader {
	if this.err == nil && !this.closed {
		if err := this.data.Close(); err != nil {
			this.setError(ErrRequestBody, "encode multipart form", err)
		}
		this.closed = true
	}
	if this.err != nil {
		return errorReader{err: this.err}
	}
	return bytes.NewReader(this.dataBuf.Bytes())
}
