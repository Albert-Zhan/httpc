package body

import (
	"io"
	"net/url"
	"strings"
)

// Url 构建 application/x-www-form-urlencoded 正文。
//
// Deprecated: 请使用 URL。此名称在 v1.x 中保留，可能在未来主版本中移除。
type Url struct {
	// 指针使表单值保持可比较，并在按值复制后共享数据。
	data *url.Values
}

// URL 用于构建 application/x-www-form-urlencoded 请求体，不支持并发修改。
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

// SetData 添加表单字段，同名字段保留多个值。
func (u *Url) SetData(name, value string) *URL {
	if u.data == nil {
		u.data = &url.Values{}
	}
	u.data.Add(name, value)
	return u
}

// GetContentType 返回 application/x-www-form-urlencoded。
func (u *Url) GetContentType() string {
	return "application/x-www-form-urlencoded"
}

// Encode 返回 URL 编码后的正文 Reader。
func (u *Url) Encode() io.Reader {
	if u.data == nil {
		return strings.NewReader("")
	}
	return strings.NewReader(u.data.Encode())
}
