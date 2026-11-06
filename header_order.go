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

// SetHeaderOrder 设置普通头顺序，排序名称转小写，Raw 头写法不变。
// 需要支持 fhttp 排序控制键的外部后端，建议使用 tls-client；标准后端返回 ErrUnsupportedBackend。
// 复制 names，重复调用替换配置，空参数清除配置，未列出的头仍保留。
// 名称非法或重复时，Send 返回 ErrInvalidRequest。
func (r *Request) SetHeaderOrder(names ...string) *Request {
	r.headerOrderSet = len(names) > 0
	r.setHeaderOrder(headerOrderKey, names, true)
	return r
}

// SetPseudoHeaderOrder 设置 HTTP/2 伪头顺序，须包含 :method、:authority、:scheme、:path 各一次。
// 伪头仍在普通头前，HTTP/1.1 忽略此配置；后端和替换规则同 [Request.SetHeaderOrder]。
// 标准后端返回 ErrUnsupportedBackend，列表无效时 Send 返回 ErrInvalidRequest。
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
