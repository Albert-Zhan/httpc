package httpc

import "net/http/cookiejar"

// CookieJar 管理 Cookie 的域名、路径和有效期，使用标准库 cookiejar.Jar。
type CookieJar = cookiejar.Jar

// NewCookieJar 创建不配置公共后缀列表的标准库 CookieJar。
// 自定义公共后缀规则时，请创建带 cookiejar.Options.PublicSuffixList 的标准库 Jar，
// 再传入 Client.SetCookieJar。
func NewCookieJar() *CookieJar {
	// 标准库使用 nil 选项创建 Jar 时不会返回错误。
	jar, _ := cookiejar.New(nil)
	return jar
}
