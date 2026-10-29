package body

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
)

// Form 构建包含普通字段和文件的 multipart/form-data 正文，不支持并发共享。
type Form struct {
	dataBuf *bytes.Buffer
	data    *multipart.Writer
	err     error
	closed  bool
}

// NewFormData 创建空的 multipart 表单。
func NewFormData() *Form {
	bodyBuf := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(bodyBuf)
	return &Form{
		dataBuf: bodyBuf,
		data:    bodyWriter,
	}
}

// SetBoundary 设置 multipart 边界，应在添加字段或文件前调用。
func (f *Form) SetBoundary(boundary string) *Form {
	if !f.canWrite() {
		return f
	}
	if err := f.data.SetBoundary(boundary); err != nil {
		f.setError(ErrRequestBody, "set multipart boundary", err)
	}
	return f
}

// SetData 添加普通字段，允许同名字段重复。
func (f *Form) SetData(name, value string) *Form {
	if !f.canWrite() {
		return f
	}
	if err := f.data.WriteField(name, value); err != nil {
		f.setError(ErrRequestBody, "write multipart field", err)
	}
	return f
}

// SetFile 添加本地文件，name 为字段名，file 为文件路径。
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

// GetContentType 返回包含 boundary 的 multipart/form-data 类型。
func (f *Form) GetContentType() string {
	return f.data.FormDataContentType()
}

// Encode 完成表单编码并返回正文 Reader，此后不能再添加字段或文件。
// 编码失败时，返回的 Reader 会在读取时报告错误。
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
