package body

import (
	"io"
	"net/url"
	"strings"
)

// Url 用于构建 application/x-www-form-urlencoded 请求体
// 通过内部维护的 url.Values 实现键值对表单数据的编码
//
// Deprecated: 请使用 URL。此名称在 v1.x 中保留，可能在未来主版本中移除。
type Url struct {
	// Keep Url comparable and preserve shared state when callers copy its value.
	data *url.Values
}

// URL 用于构建 application/x-www-form-urlencoded 请求体，不支持并发修改。
// URL 与 Url 是同一类型。
type URL = Url

// NewURLEncode 创建用于构建 URL-encoded 表单数据的请求体。
func NewURLEncode() *URL {
	return &Url{
		data: &url.Values{},
	}
}

// NewUrlEncode 创建用于构建 URL-encoded 表单数据的请求体。
//
// Deprecated: 请使用 NewURLEncode。此函数在 v1.x 中保留，可能在未来主版本中移除。
func NewUrlEncode() *URL {
	return NewURLEncode()
}

// SetData 向表单中添加一个字段（name=value）
// 若同一个 name 被多次添加，将作为多值字段存在
// 支持多次链式调用
func (u *Url) SetData(name, value string) *URL {
	if u.data == nil {
		u.data = &url.Values{}
	}
	u.data.Add(name, value)
	return u
}

// GetContentType 返回该类型的 Content-Type
func (u *Url) GetContentType() string {
	return "application/x-www-form-urlencoded"
}

// Encode 返回编码后的表单内容，用于作为 HTTP 请求的 body
func (u *Url) Encode() io.Reader {
	if u.data == nil {
		return strings.NewReader("")
	}
	return strings.NewReader(u.data.Encode())
}
