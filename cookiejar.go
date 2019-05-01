package httpc

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type CookieJar struct {

	mu sync.Mutex

	entries map[string]map[string]entry

	nextSeqNum uint64
}

func NewCookieJar() *CookieJar {
	jar := &CookieJar{
		entries: make(map[string]map[string]entry),
	}

	return jar
}

type entry struct {
	Name       string
	Value      string
	Domain     string
	Path       string
	SameSite   string
	Secure     bool
	HttpOnly   bool
	Persistent bool
	HostOnly   bool
	Expires    time.Time
	Creation   time.Time
	LastAccess time.Time
	seqNum uint64
}

func (e *entry) id() string {
	return fmt.Sprintf("%s;%s;%s", e.Domain, e.Path, e.Name)
}

func (e *entry) shouldSend(https bool, host, path string) bool {
	return e.domainMatch(host) && e.pathMatch(path) && (https || !e.Secure)
}

func (e *entry) domainMatch(host string) bool {
	if e.Domain == host {
		return true
	}

	return !e.HostOnly && hasDotSuffix(host, e.Domain)
}

func (e *entry) pathMatch(requestPath string) bool {
	if requestPath == e.Path {
		return true
	}

	if strings.HasPrefix(requestPath, e.Path) {
		if e.Path[len(e.Path)-1] == '/' {
			return true
		} else if requestPath[len(e.Path)] == '/' {
			return true
		}
	}

	return false
}

func hasDotSuffix(s, suffix string) bool {
	return len(s) > len(suffix) && s[len(s)-len(suffix)-1] == '.' && s[len(s)-len(suffix):] == suffix
}

func (j *CookieJar) Cookies(u *url.URL) (cookies []*http.Cookie) {
	return j.cookies(u, time.Now())
}

func (j *CookieJar) cookies(u *url.URL, now time.Time) (cookies []*http.Cookie) {
	if u.Scheme != "http" && u.Scheme != "https" {
		return cookies
	}
	host, err := canonicalHost(u.Host)
	if err != nil {
		return cookies
	}

	key := jarKey(host)

	j.mu.Lock()
	defer j.mu.Unlock()

	submap := j.entries[key]
	if submap == nil {
		return cookies
	}

	https := u.Scheme == "https"
	path := u.Path
	if path == "" {
		path = "/"
	}

	modified := false
	var selected []entry
	for id, e := range submap {
		if e.Persistent && !e.Expires.After(now) {
			delete(submap, id)
			modified = true
			continue
		}
		if !e.shouldSend(https, host, path) {
			continue
		}
		e.LastAccess = now
		submap[id] = e
		selected = append(selected, e)
		modified = true
	}

	if modified {
		if len(submap) == 0 {
			delete(j.entries, key)
		} else {
			j.entries[key] = submap
		}
	}

	sort.Slice(selected, func(i, j int) bool {
		s := selected
		if len(s[i].Path) != len(s[j].Path) {
			return len(s[i].Path) > len(s[j].Path)
		}
		if !s[i].Creation.Equal(s[j].Creation) {
			return s[i].Creation.Before(s[j].Creation)
		}
		return s[i].seqNum < s[j].seqNum
	})
	for _, e := range selected {
		//修复了读取cookie时缺少Domain,造成读取后的cookie请求失效问题
		cookies = append(cookies, &http.Cookie{Name: e.Name, Value: e.Value,Domain:e.Domain})
	}

	return cookies
}

func (j *CookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.setCookies(u, cookies, time.Now())
}

func (j *CookieJar) setCookies(u *url.URL, cookies []*http.Cookie, now time.Time) {
	if len(cookies) == 0 {
		return
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return
	}
	//There may be errors in setting local domain names, such as:.com.cm
	host, err := canonicalHost(u.Host)
	if err != nil {
		return
	}

	key := jarKey(host)
	defPath := defaultPath(u.Path)

	j.mu.Lock()
	defer j.mu.Unlock()

	submap := j.entries[key]

	modified := false
	for _, cookie := range cookies {
		e, remove, err := j.newEntry(cookie, now, defPath, host)
		if err != nil {
			continue
		}
		id := e.id()
		if remove {
			if submap != nil {
				if _, ok := submap[id]; ok {
					delete(submap, id)
					modified = true
				}
			}
			continue
		}
		if submap == nil {
			submap = make(map[string]entry)
		}

		if old, ok := submap[id]; ok {
			e.Creation = old.Creation
			e.seqNum = old.seqNum
		} else {
			e.Creation = now
			e.seqNum = j.nextSeqNum
			j.nextSeqNum++
		}
		e.LastAccess = now
		submap[id] = e
		modified = true
	}

	if modified {
		if len(submap) == 0 {
			delete(j.entries, key)
		} else {
			j.entries[key] = submap
		}
	}
}

func canonicalHost(host string) (string, error) {
	var err error

	host = strings.ToLower(host)
	if hasPort(host) {
		host, _, err = net.SplitHostPort(host)
		if err != nil {
			return "", err
		}
	}

	if strings.HasSuffix(host, ".") {
		host = host[:len(host)-1]
	}
	return toASCII(host)
}

func hasPort(host string) bool {
	colons := strings.Count(host, ":")
	if colons == 0 {
		return false
	}
	if colons == 1 {
		return true
	}

	return host[0] == '[' && strings.Contains(host, "]:")
}

func jarKey(host string) string {
	if isIP(host) {
		return host
	}

	i :=strings.LastIndex(host, ".")
	if i <= 0 {
		return host
	}

	prevDot := strings.LastIndex(host[:i-1], ".")
	return host[prevDot+1:]
}

func isIP(host string) bool {
	return net.ParseIP(host) != nil
}

func defaultPath(path string) string {
	/*if len(path) == 0 || path[0] != '/' {
		return "/"
	}

	i := strings.LastIndex(path, "/")
	if i == 0 {
		return "/"
	}
	return path[:i]*/
	//修复了设置cookie时path作用域的问题，统一设置/
	return "/"
}

func (j *CookieJar) newEntry(c *http.Cookie, now time.Time, defPath, host string) (e entry, remove bool, err error) {
	e.Name = c.Name

	if c.Path == "" || c.Path[0] != '/' {
		e.Path = defPath
	} else {
		e.Path = c.Path
	}

	e.Domain, e.HostOnly, err = j.domainAndType(host, c.Domain)
	if err != nil {
		return e, false, err
	}

	if c.MaxAge < 0 {
		return e, true, nil
	} else if c.MaxAge > 0 {
		e.Expires = now.Add(time.Duration(c.MaxAge) * time.Second)
		e.Persistent = true
	} else {
		if c.Expires.IsZero() {
			e.Expires = endOfTime
			e.Persistent = false
		} else {
			if !c.Expires.After(now) {
				return e, true, nil
			}
			e.Expires = c.Expires
			e.Persistent = true
		}
	}

	e.Value = c.Value
	e.Secure = c.Secure
	e.HttpOnly = c.HttpOnly

	switch c.SameSite {
	case http.SameSiteDefaultMode:
		e.SameSite = "SameSite"
	case http.SameSiteStrictMode:
		e.SameSite = "SameSite=Strict"
	case http.SameSiteLaxMode:
		e.SameSite = "SameSite=Lax"
	}

	return e, false, nil
}

var (
	errIllegalDomain   = errors.New("cookiejar: illegal cookie domain attribute")
	errMalformedDomain = errors.New("cookiejar: malformed cookie domain attribute")
	errNoHostname      = errors.New("cookiejar: no host name available (IP only)")
)

var endOfTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

func (j *CookieJar) domainAndType(host, domain string) (string, bool, error) {
	if domain == "" {
		return host, true, nil
	}

	if isIP(host) {
		return "", false, errNoHostname
	}

	if domain[0] == '.' {
		domain = domain[1:]
	}

	if len(domain) == 0 || domain[0] == '.' {
		return "", false, errMalformedDomain
	}
	domain = strings.ToLower(domain)

	if domain[len(domain)-1] == '.' {
		return "", false, errMalformedDomain
	}

	if host != domain && !hasDotSuffix(host, domain) {
		return "", false, errIllegalDomain
	}

	return domain, false, nil
}

const (
	base        int32 = 36
	damp        int32 = 700
	initialBias int32 = 72
	initialN    int32 = 128
	skew        int32 = 38
	tmax        int32 = 26
	tmin        int32 = 1
)

func encode(prefix, s string) (string, error) {
	output := make([]byte, len(prefix), len(prefix)+1+2*len(s))
	copy(output, prefix)
	delta, n, bias := int32(0), initialN, initialBias
	b, remaining := int32(0), int32(0)
	for _, r := range s {
		if r < utf8.RuneSelf {
			b++
			output = append(output, byte(r))
		} else {
			remaining++
		}
	}
	h := b
	if b > 0 {
		output = append(output, '-')
	}
	for remaining != 0 {
		m := int32(0x7fffffff)
		for _, r := range s {
			if m > r && r >= n {
				m = r
			}
		}
		delta += (m - n) * (h + 1)
		if delta < 0 {
			return "", fmt.Errorf("cookiejar: invalid label %q", s)
		}
		n = m
		for _, r := range s {
			if r < n {
				delta++
				if delta < 0 {
					return "", fmt.Errorf("cookiejar: invalid label %q", s)
				}
				continue
			}
			if r > n {
				continue
			}
			q := delta
			for k := base; ; k += base {
				t := k - bias
				if t < tmin {
					t = tmin
				} else if t > tmax {
					t = tmax
				}
				if q < t {
					break
				}
				output = append(output, encodeDigit(t+(q-t)%(base-t)))
				q = (q - t) / (base - t)
			}
			output = append(output, encodeDigit(q))
			bias = adapt(delta, h+1, h == b)
			delta = 0
			h++
			remaining--
		}
		delta++
		n++
	}
	return string(output), nil
}

func encodeDigit(digit int32) byte {
	switch {
	case 0 <= digit && digit < 26:
		return byte(digit + 'a')
	case 26 <= digit && digit < 36:
		return byte(digit + ('0' - 26))
	}
	panic("cookiejar: internal error in punycode encoding")
}

func adapt(delta, numPoints int32, firstTime bool) int32 {
	if firstTime {
		delta /= damp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	k := int32(0)
	for delta > ((base-tmin)*tmax)/2 {
		delta /= base - tmin
		k += base
	}
	return k + (base-tmin+1)*delta/(delta+skew)
}

const acePrefix = "xn--"

func toASCII(s string) (string, error) {
	if ascii(s) {
		return s, nil
	}
	labels := strings.Split(s, ".")
	for i, label := range labels {
		if !ascii(label) {
			a, err := encode(acePrefix, label)
			if err != nil {
				return "", err
			}
			labels[i] = a
		}
	}
	return strings.Join(labels, "."), nil
}

func ascii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}