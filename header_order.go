package httpc

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
)

const (
	headerOrderKey       = "Header-Order:"
	pseudoHeaderOrderKey = "PHeader-Order:"
)

// SetHeaderOrder 设置普通请求头的顺序，名称转为小写，不改变 Raw 头的名称写法。
// 建议用于 tls-client 的 HTTP/1.1、HTTP/2；其他外部后端须支持 fhttp 排序控制键。
// 复制 names；重复调用替换配置，不传参数清除配置，未列出的头仍由后端处理。
// 内置标准库后端在 Send 时返回 ErrUnsupportedBackend；空名称、非法名称或重复项返回 ErrInvalidRequest。
func (r *Request) SetHeaderOrder(names ...string) *Request {
	r.headerOrderSet = len(names) > 0
	r.setHeaderOrder(headerOrderKey, names, true)
	return r
}

// SetPseudoHeaderOrder 设置 HTTP/2 伪头之间的顺序，伪头始终在普通头之前。
// names 须是 :method、:authority、:scheme、:path 的完整排列，各出现一次。
// 建议用于 tls-client 后端；HTTP/1.1 不使用伪头配置。
// 复制 names；重复调用替换配置，不传参数清除配置。
// 内置标准库后端在 Send 时返回 ErrUnsupportedBackend；列表无效时返回 ErrInvalidRequest。
func (r *Request) SetPseudoHeaderOrder(names ...string) *Request {
	r.pseudoHeaderOrderSet = len(names) > 0
	r.setHeaderOrder(pseudoHeaderOrderKey, names, false)
	return r
}

func (r *Request) setHeaderOrder(key string, names []string, lowercase bool) {
	if len(names) == 0 {
		delete(r.header, key)
		return
	}
	values := slices.Clone(names)
	if lowercase {
		for i, name := range values {
			if validHeaderOrderName(name) {
				values[i] = strings.ToLower(name)
			}
		}
	}
	if r.header == nil {
		r.header = make(http.Header)
	}
	r.header[key] = values
}

func (r *Request) validateHeaderOrders() error {
	if !r.headerOrderSet && !r.pseudoHeaderOrderSet {
		return nil
	}
	for _, config := range []struct {
		set    bool
		key    string
		pseudo bool
	}{
		{r.headerOrderSet, headerOrderKey, false},
		{r.pseudoHeaderOrderSet, pseudoHeaderOrderKey, true},
	} {
		if !config.set {
			continue
		}
		if err := validateHeaderOrder(r.header[config.key], config.pseudo); err != nil {
			return wrapError(ErrInvalidRequest, "configure request header order", err)
		}
		if r.httpc.doer == nil {
			return wrapError(ErrUnsupportedBackend, "configure request header order", fmt.Errorf("the built-in backend does not support configurable header order"))
		}
	}
	return nil
}

func validateHeaderOrder(names []string, pseudo bool) error {
	if pseudo && len(names) != 4 {
		return fmt.Errorf("pseudo header order must contain :method, :authority, :scheme and :path exactly once")
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		key := strings.ToLower(name)
		if pseudo {
			switch name {
			case ":method", ":authority", ":scheme", ":path":
			default:
				return fmt.Errorf("invalid pseudo header name %q", name)
			}
		} else if !validHeaderOrderName(name) {
			return fmt.Errorf("invalid header name %q in header order", name)
		}
		if seen[key] {
			return fmt.Errorf("duplicate header name %q in header order", name)
		}
		seen[key] = true
	}
	return nil
}

func validHeaderOrderName(name string) bool {
	if name == "" {
		return false
	}
	for _, ch := range name {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", ch)) {
			return false
		}
	}
	return true
}
