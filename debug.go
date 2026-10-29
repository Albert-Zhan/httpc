package httpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

const (
	debugRedacted = "[REDACTED]"
	maxDebugBody  = 64 << 10
)

var defaultDebugKeys = []string{
	"authorization", "auth", "cookie", "password", "passwordconfirmation", "passwd", "pwd",
	"passphrase", "secret", "token", "apikey", "session", "sessionid", "sid",
	"csrftoken", "signature", "privatekey", "otp", "code", "credentials",
}

func normalizeDebugKey(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, name)
}

func (c *HttpClient) sensitiveDebugKey(name string) bool {
	key := normalizeDebugKey(name)
	if _, ok := c.debugKeys[key]; ok {
		return true
	}
	for _, sensitive := range defaultDebugKeys {
		if strings.HasSuffix(key, sensitive) {
			return true
		}
	}
	return false
}

func (c *HttpClient) debugURL(u *url.URL) string {
	copyURL := *u
	if copyURL.User != nil {
		copyURL.User = url.User(debugRedacted)
	}
	query, err := url.ParseQuery(copyURL.RawQuery)
	if err != nil {
		copyURL.RawQuery = "[query omitted]"
	} else {
		c.redactDebugValues(query)
		copyURL.RawQuery = query.Encode()
	}
	if copyURL.Fragment != "" || copyURL.RawFragment != "" {
		copyURL.Fragment = debugRedacted
		copyURL.RawFragment = ""
	}
	return copyURL.String()
}

func (c *HttpClient) redactDebugValues(values url.Values) {
	for key := range values {
		if c.sensitiveDebugKey(key) {
			values[key] = []string{debugRedacted}
		}
	}
}

func (c *HttpClient) debugHeaders(headers http.Header) http.Header {
	copyHeaders := headers.Clone()
	for name, values := range copyHeaders {
		if c.sensitiveDebugKey(name) {
			copyHeaders[name] = []string{debugRedacted}
		} else if strings.EqualFold(name, "Referer") {
			for i, value := range values {
				if u, err := url.Parse(value); err == nil && u.IsAbs() && u.Host != "" {
					values[i] = c.debugURL(u)
				} else {
					values[i] = debugRedacted
				}
			}
		}
	}
	return copyHeaders
}

func debugJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "[omitted: cannot safely encode]"
	}
	return string(encoded)
}

func (c *HttpClient) redactDebugJSON(value any) {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if c.sensitiveDebugKey(key) {
				value[key] = debugRedacted
			} else {
				c.redactDebugJSON(item)
			}
		}
	case []any:
		for _, item := range value {
			c.redactDebugJSON(item)
		}
	}
}

// debugBody reads only a replayable copy, never the live request body or Body.GetData.
func (c *HttpClient) debugBody(req *http.Request) string {
	if req.Body == nil || req.Body == http.NoBody {
		return ""
	}
	if req.GetBody == nil {
		return "[omitted: body is not replayable]"
	}
	mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		return "[omitted: unknown content type]"
	}
	switch {
	case mediaType == "application/json", strings.HasSuffix(mediaType, "+json"),
		mediaType == "application/x-www-form-urlencoded", mediaType == "multipart/form-data":
	default:
		return "[omitted: unsupported content type]"
	}
	reader, err := req.GetBody()
	if err != nil || reader == nil {
		return "[omitted: cannot copy body]"
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxDebugBody+1))
	if err != nil || len(data) > maxDebugBody {
		return "[omitted: unreadable body or size exceeds 64 KiB]"
	}
	switch mediaType {
	case "application/x-www-form-urlencoded":
		values, err := url.ParseQuery(string(data))
		if err != nil {
			return "[omitted: invalid form encoding]"
		}
		c.redactDebugValues(values)
		return values.Encode()
	case "multipart/form-data":
		parts := multipart.NewReader(bytes.NewReader(data), params["boundary"])
		values := make(map[string][]string)
		for {
			part, err := parts.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "[omitted: invalid multipart body]"
			}
			name := part.FormName()
			disposition, attributes, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			if err != nil || disposition != "form-data" || name == "" {
				return "[omitted: invalid multipart disposition]"
			}
			_, isFile := attributes["filename"]
			value := "[file omitted]"
			if !isFile {
				field, err := io.ReadAll(part)
				if err != nil {
					return "[omitted: unreadable multipart field]"
				}
				value = string(field)
			}
			if c.sensitiveDebugKey(name) {
				value = debugRedacted
			}
			values[name] = append(values[name], value)
		}
		return debugJSON(values)
	default:
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return "[omitted: invalid JSON]"
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return "[omitted: invalid JSON]"
		}
		switch value.(type) {
		case map[string]any, []any:
		default:
			return "[omitted: scalar JSON]"
		}
		c.redactDebugJSON(value)
		return debugJSON(value)
	}
}

func (r *Request) log() {
	if !r.debug {
		return
	}
	c := r.httpc
	c.debugMu.Lock()
	defer c.debugMu.Unlock()
	if c.debugOut == nil || c.debugOut == io.Discard {
		return
	}
	cookies := make(map[string]string)
	for _, cookie := range r.request.Cookies() {
		cookies[cookie.Name] = debugRedacted
	}
	var record strings.Builder
	fmt.Fprintln(&record, "[httpc Debug]")
	fmt.Fprintln(&record, "-------------------------------------------------------------------")
	fmt.Fprintf(&record, "Request: %s %s\nHeader: %s\nCookies: %s\nBody: %s\n",
		r.request.Method, c.debugURL(r.request.URL), debugJSON(c.debugHeaders(r.request.Header)),
		debugJSON(cookies), c.debugBody(r.request))
	fmt.Fprintln(&record, "-------------------------------------------------------------------")
	_, _ = io.WriteString(c.debugOut, record.String())
}
