package body

import (
	"fmt"
	"io"
	"strings"
)

const (
	// Text 表示纯文本的 MIME 类型。
	Text = "text/plain"
	// JavaScript 表示 JavaScript 的 MIME 类型。
	JavaScript = "application/javascript"
	// JSON 表示 JSON 的 MIME 类型。
	JSON = "application/json"
	// HTML 表示 HTML 的 MIME 类型。
	HTML = "text/html"
	// XML 表示 XML 的 MIME 类型。
	XML = "application/xml"
	// Protobuf 表示 protobuf 二进制正文的 MIME 类型。
	Protobuf = "application/x-protobuf"

	// Json 表示 JSON 的 MIME 类型。
	//
	// Deprecated: 请使用 [JSON]。v1.x 继续保留。
	Json = JSON
	// Html 表示 HTML 的 MIME 类型。
	//
	// Deprecated: 请使用 [HTML]。v1.x 继续保留。
	Html = HTML
	// Xml 表示 XML 的 MIME 类型。
	//
	// Deprecated: 请使用 [XML]。v1.x 继续保留。
	Xml = XML
)

// Raw 构建指定 MIME 类型的文本或二进制正文，不支持并发修改。
type Raw struct {
	// 不可变字节快照使 Raw 值保持可比较。
	data   string
	format string
	err    error
}

// NewRawData 创建空的原始请求体。
func NewRawData() *Raw {
	return &Raw{
		data:   "",
		format: "",
	}
}

// SetData 替换正文及其 MIME 类型，接受 string、[]byte 或 nil。
// []byte 会被复制，nil 表示空正文；其他类型通过 Err 报告 ErrRequestBody。
// 每次调用都会替换此前的内容和错误状态。
func (r *Raw) SetData(data any, format string) {
	r.data = ""
	r.format = format
	r.err = nil
	switch value := data.(type) {
	case nil:
	case string:
		r.data = value
	case []byte:
		r.data = string(value)
	default:
		r.err = fmt.Errorf("%w: unsupported raw body type %T", ErrRequestBody, data)
	}
}

// Err 返回最近一次 SetData 的错误；后续设置合法正文可清除错误。
func (r *Raw) Err() error { return r.err }

// GetContentType 返回正文的 MIME 类型。
func (r *Raw) GetContentType() string {
	return r.format
}

// Encode 返回正文 Reader。
func (r *Raw) Encode() io.Reader {
	if r.err != nil {
		return errorReader{err: r.err}
	}
	return strings.NewReader(r.data)
}
