package httpc

// Version 是库版本号，不包含发布标签的 v 前缀。
const Version = "1.4.1-dev"

// DefaultUserAgent 是未显式设置 User-Agent 时发送的默认值。
const DefaultUserAgent = "httpc/" + Version
