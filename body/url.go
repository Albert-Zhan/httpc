package body

import (
	"io"
	"net/url"
	"strings"
)

// URL 用于构建 application/x-www-form-urlencoded 请求体，不支持并发修改。
// 零值可以使用；独立的表单应分别创建，不要复制正在使用的表单。
type URL struct {
	data url.Values
}

// NewURLEncode 创建用于构建 URL-encoded 表单数据的请求体。
func NewURLEncode() *URL {
	return &URL{
		data: make(url.Values),
	}
}

// SetData 添加表单字段，同名字段保留多个值。
func (u *URL) SetData(name, value string) *URL {
	if u.data == nil {
		u.data = make(url.Values)
	}
	u.data.Add(name, value)
	return u
}

// GetContentType 返回 application/x-www-form-urlencoded。
func (u *URL) GetContentType() string {
	return "application/x-www-form-urlencoded"
}

// Encode 返回 URL 编码后的正文 Reader。
func (u *URL) Encode() io.Reader {
	if u.data == nil {
		return strings.NewReader("")
	}
	return strings.NewReader(u.data.Encode())
}
