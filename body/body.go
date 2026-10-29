package body

import (
	"io"
)

// Body 定义了可作为 HTTP 请求体的统一接口
// 所有请求体类型（如 Raw、URL、Form 等）都应实现该接口
// 通过该接口，外部在构建请求时可以以统一方式获取类型与最终编码形式
// 实现可额外提供 Err() error；httpc 会在编码前后检查该方法，发现错误时停止发送。
// 除非实现明确保证，否则不要在多个并发请求之间共享可变的请求体对象。
type Body interface {
	// GetContentType 返回该请求体对应的 Content-Type 值
	// 用于在 HTTP 请求头中设置正确的 MIME 类型
	GetContentType() string
	// Encode 将当前请求体编码为 io.Reader，供 HTTP 请求作为 body 使用
	// 每个实现通常根据自身所需格式返回相应的 Reader
	Encode() io.Reader
}
