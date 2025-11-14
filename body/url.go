package body

import (
	"io"
	"net/url"
	"strings"
)

// Url 用于构建 application/x-www-form-urlencoded 请求体
// 通过内部维护的 url.Values 实现键值对表单数据的编码
type Url struct {
	data *url.Values
}

// NewUrlEncode 创建一个新的 Url 实例，用于构建 URL-encoded 表单数据
// 返回的 Url 可继续通过 SetData 添加字段，最终 Encode 为 io.Reader
func NewUrlEncode() *Url {
	return &Url{
		data: &url.Values{},
	}
}

// SetData 向表单中添加一个字段（name=value）
// 若同一个 name 被多次添加，将作为多值字段存在
// 支持多次链式调用
func (this *Url) SetData(name, value string) *Url {
	this.data.Add(name, value)
	return this
}

// GetData 返回当前表单的 URL-encoded 文本形式
func (this *Url) GetData() string {
	return this.data.Encode()
}

// GetContentType 返回该类型的 Content-Type
func (this *Url) GetContentType() string {
	return "application/x-www-form-urlencoded"
}

// Encode 返回编码后的表单内容，用于作为 HTTP 请求的 body
func (this *Url) Encode() io.Reader {
	return strings.NewReader(this.data.Encode())
}
