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

type Form struct {
	dataBuf *bytes.Buffer
	data    *multipart.Writer
}

func NewFormData() *Form {
	bodyBuf := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(bodyBuf)
	return &Form{
		dataBuf: bodyBuf,
		data:    bodyWriter,
	}
}

func (this *Form) SetBoundary(boundary string) *Form {
	_ = this.data.SetBoundary(boundary)
	return this
}

func (this *Form) SetData(name, value string) *Form {
	_ = this.data.WriteField(name, value)
	return this
}

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

func (this *Form) GetContentType() string {
	return this.data.FormDataContentType()
}

func (this *Form) Encode() io.Reader {
	_ = this.data.Close()
	return io.NopCloser(this.dataBuf)
}
