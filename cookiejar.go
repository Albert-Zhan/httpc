package httpc

import "net/http/cookiejar"

// CookieJar 是标准库 cookiejar.Jar 的别名，保留原有类型名。
// Cookie 的域名、路径和有效期均由标准库管理。
type CookieJar = cookiejar.Jar

// NewCookieJar 创建标准库 CookieJar，保留原有的无参数调用方式。
// 与 cookiejar.New(nil) 一致，不配置公共后缀列表。
// 如需公共后缀校验，请使用 cookiejar.Options.PublicSuffixList 创建 Jar，
// 再通过 Client.SetCookieJar 传入。
func NewCookieJar() *CookieJar {
	// 标准库使用 nil 选项创建 Jar 时不会返回错误。
	jar, _ := cookiejar.New(nil)
	return jar
}
