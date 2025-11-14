package body

import (
	"io"
)

// Body 定义了可作为 HTTP 请求体的统一接口
// 所有请求体类型（如 Raw、Url、Form 等）都应实现该接口
// 通过该接口，外部在构建请求时可以以统一方式获取内容、类型与最终编码形式
type Body interface {
	// GetData 返回当前请求体的原始数据内容（字符串形式）
	// 通常用于调试或日志，不一定是最终发送的格式
	GetData() string
	// GetContentType 返回该请求体对应的 Content-Type 值
	// 用于在 HTTP 请求头中设置正确的 MIME 类型
	GetContentType() string
	// Encode 将当前请求体编码为 io.Reader，供 HTTP 请求作为 body 使用
	// 每个实现通常根据自身所需格式返回相应的 Reader
	Encode() io.Reader
}
