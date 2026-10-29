package body

import (
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

	// Json 表示 JSON 的 MIME 类型。
	//
	// Deprecated: 请使用 JSON。此名称在 v1.x 中保留，可能在未来主版本中移除。
	Json = JSON
	// Html 表示 HTML 的 MIME 类型。
	//
	// Deprecated: 请使用 HTML。此名称在 v1.x 中保留，可能在未来主版本中移除。
	Html = HTML
	// Xml 表示 XML 的 MIME 类型。
	//
	// Deprecated: 请使用 XML。此名称在 v1.x 中保留，可能在未来主版本中移除。
	Xml = XML
)

// Raw 构建指定 MIME 类型的字符串正文。
type Raw struct {
	data   string
	format string
}

// NewRawData 创建空的字符串请求体。
func NewRawData() *Raw {
	return &Raw{
		data:   "",
		format: "",
	}
}

// SetData 设置正文及其 MIME 类型。
func (r *Raw) SetData(data, format string) {
	r.data = data
	r.format = format
}

// GetContentType 返回正文的 MIME 类型。
func (r *Raw) GetContentType() string {
	return r.format
}

// Encode 返回正文 Reader。
func (r *Raw) Encode() io.Reader {
	return strings.NewReader(r.data)
}
