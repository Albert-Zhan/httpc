package body

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
)

// Form 用于构建 multipart/form-data 类型的表单请求体
// 支持添加普通字段与文件字段，并最终编码为可用于 HTTP 请求的 io.Reader
type Form struct {
	dataBuf *bytes.Buffer
	data    *multipart.Writer
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
	_ = this.data.SetBoundary(boundary)
	return this
}

// SetData 向表单中添加一个普通字段（name=value）
// 参数 name 表示字段名，value 表示字段内容
// 支持多次链式调用
func (this *Form) SetData(name, value string) *Form {
	_ = this.data.WriteField(name, value)
	return this
}

// SetFile 向表单中添加一个文件字段
// 参数 name 为表单字段名，file 为本地文件路径
// 支持多次链式调用
func (this *Form) SetFile(name, file string) *Form {
	fd, err := os.Open(file)
	if err != nil {
		panic("file does not exist")
	}
	defer fd.Close()
	fileWriter, _ := this.data.CreateFormFile(name, filepath.Base(file))
	_, _ = io.Copy(fileWriter, fd)
	return this
}

// GetData 返回当前 Form 的 multipart 数据文本形式
func (this *Form) GetData() string {
	b := this.dataBuf.Bytes()
	br := bytes.NewReader(b)
	r := multipart.NewReader(br, this.data.Boundary())
	form, err := r.ReadForm(32 << 20)
	if err == nil {
		var sb strings.Builder
		boundary := this.data.Boundary()
		header := fmt.Sprintf("--%s\r\n", boundary)
		footer := fmt.Sprintf("--%s--", boundary)

		for k, v := range form.Value {
			sb.WriteString(header)
			sb.WriteString(fmt.Sprintf(`Content-Disposition: form-data; name="%s"`, k))
			sb.WriteString("\r\n")
			sb.WriteString(v[0])
			sb.WriteString("\r\n")
		}

		for fk, fv := range form.File {
			for _, fh := range fv {
				sb.WriteString(header)
				sb.WriteString(fmt.Sprintf(
					`Content-Disposition: form-data; name="%s"; filename="%s"`, fk, fh.Filename))
				sb.WriteString("\r\n")
				sb.WriteString(fmt.Sprintf("Content-Type: %s\r\n", fh.Header.Get("Content-Type")))
			}
		}
		sb.WriteString(footer)
		return sb.String()
	}
	return ""
}

// GetContentType 返回该表单对应的 Content-Type，包含 boundary
func (this *Form) GetContentType() string {
	return this.data.FormDataContentType()
}

// Encode 关闭 multipart.Writer 并返回完整的 multipart/form-data 编码内容
// 返回的 io.Reader 可直接作为 HTTP 请求 body
// 注意：调用 Encode() 后将无法再次向 Form 中添加字段
func (this *Form) Encode() io.Reader {
	_ = this.data.Close()
	return io.NopCloser(this.dataBuf)
}
