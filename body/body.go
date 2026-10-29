package body

import (
	"io"
)

// Body 提供请求正文的内容类型和编码结果。
// 实现可额外提供 Err() error，httpc 在编码前后检查错误并决定是否发送。
// 并发共享前需确认实现保证并发安全。
type Body interface {
	// GetContentType 返回正文的 MIME 类型。
	GetContentType() string
	// Encode 返回用于发送的正文 Reader。
	Encode() io.Reader
}
