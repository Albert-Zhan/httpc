package body

import (
	"io"
	"strings"
)

const (
	// Text 纯文本类型
	Text = "text/plain"
	// JavaScript JavaScript 脚本内容类型
	JavaScript = "application/javascript"
	// Json JSON 数据类型
	Json = "application/json"
	// Html HTML 文本类型
	Html = "text/html"
	// Xml XML 数据类型
	Xml = "application/xml"
)

// Raw 用于构建原始字符串类型的请求体（raw body）
// 可用于发送 JSON、XML、纯文本或任意自定义格式的数据
type Raw struct {
	data   string
	format string
}

// NewRawData 创建一个空的 Raw 请求体对象
// 通过 SetData 设置内容和格式，最终 Encode 生成 io.Reader
func NewRawData() *Raw {
	return &Raw{
		data:   "",
		format: "",
	}
}

// SetData 设置原始数据内容以及对应的 MIME 格式（Content-Type）
// 参数 data 为内容，format 为 MIME 类型（例如 Json、Xml 等）
func (this *Raw) SetData(data, format string) {
	this.data = data
	this.format = format
}

// GetData 返回当前的原始字符串内容
func (this *Raw) GetData() string {
	return this.data
}

// GetContentType 返回当前 Raw 数据的 Content-Type 字段
func (this *Raw) GetContentType() string {
	return this.format
}

// Encode 将当前数据包装为 io.Reader，用于构建 HTTP 请求的 body
func (this *Raw) Encode() io.Reader {
	return strings.NewReader(this.data)
}
