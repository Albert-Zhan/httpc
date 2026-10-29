package httpc

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	debugRedacted = "[REDACTED]"
	maxDebugBody  = 64 << 10
)

func normalizeDebugKey(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, name)
}

func (c *HttpClient) sensitiveDebugKey(name string) bool {
	_, ok := c.debugKeys[normalizeDebugKey(name)]
	return ok
}

func (c *HttpClient) debugURL(u *url.URL) string {
	copyURL := *u
	if len(c.debugKeys) == 0 {
		return copyURL.String()
	}
	if copyURL.User != nil {
		username := copyURL.User.Username()
		password, hasPassword := copyURL.User.Password()
		if c.sensitiveDebugKey("userinfo") {
			copyURL.User = url.User(debugRedacted)
		} else {
			if c.sensitiveDebugKey("username") {
				username = debugRedacted
			}
			if c.sensitiveDebugKey("password") && hasPassword {
				password = debugRedacted
			}
			if hasPassword {
				copyURL.User = url.UserPassword(username, password)
			} else {
				copyURL.User = url.User(username)
			}
		}
	}
	query, err := url.ParseQuery(copyURL.RawQuery)
	if err != nil {
		copyURL.RawQuery = "[query omitted]"
	} else {
		if c.redactDebugValues(query) {
			copyURL.RawQuery = query.Encode()
		}
	}
	if c.sensitiveDebugKey("fragment") && (copyURL.Fragment != "" || copyURL.RawFragment != "") {
		copyURL.Fragment = debugRedacted
		copyURL.RawFragment = ""
	}
	return copyURL.String()
}

func (c *HttpClient) redactDebugValues(values url.Values) bool {
	changed := false
	for key := range values {
		if c.sensitiveDebugKey(key) {
			values[key] = []string{debugRedacted}
			changed = true
		}
	}
	return changed
}

func (c *HttpClient) debugHeaders(headers http.Header) http.Header {
	copyHeaders := headers.Clone()
	if len(c.debugKeys) == 0 {
		return copyHeaders
	}
	for name, values := range copyHeaders {
		if c.sensitiveDebugKey(name) {
			copyHeaders[name] = []string{debugRedacted}
		} else if strings.EqualFold(name, "Cookie") {
			for i, value := range values {
				parts := strings.Split(value, ";")
				for j, part := range parts {
					if key, _, found := strings.Cut(part, "="); found && c.sensitiveDebugKey(strings.TrimSpace(key)) {
						parts[j] = key + "=" + debugRedacted
					}
				}
				values[i] = strings.Join(parts, ";")
			}
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
	encoded, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return "[omitted: cannot safely encode]"
	}
	return string(encoded)
}

func (c *HttpClient) redactDebugJSON(value jsontext.Value) (jsontext.Value, error) {
	switch value.Kind() {
	case '{':
		var fields map[string]jsontext.Value
		if err := json.Unmarshal(value, &fields); err != nil {
			return nil, err
		}
		for key, item := range fields {
			if c.sensitiveDebugKey(key) {
				fields[key] = jsontext.Value(`"[REDACTED]"`)
			} else {
				redacted, err := c.redactDebugJSON(item)
				if err != nil {
					return nil, err
				}
				fields[key] = redacted
			}
		}
		return json.Marshal(fields, json.Deterministic(true))
	case '[':
		var items []jsontext.Value
		if err := json.Unmarshal(value, &items); err != nil {
			return nil, err
		}
		for i, item := range items {
			redacted, err := c.redactDebugJSON(item)
			if err != nil {
				return nil, err
			}
			items[i] = redacted
		}
		return json.Marshal(items, json.Deterministic(true))
	default:
		return value, nil
	}
}

// debugBody 只读取可重放的正文副本，避免消耗实际请求体。
func (c *HttpClient) debugBody(req *http.Request) string {
	if req.Body == nil || req.Body == http.NoBody {
		return ""
	}
	if req.GetBody == nil {
		return "[omitted: body is not replayable]"
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
	mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if len(c.debugKeys) == 0 && mediaType != "multipart/form-data" {
		if !utf8.Valid(data) {
			return "[omitted: binary body]"
		}
		return string(data)
	}
	if err != nil {
		return "[omitted: unknown content type]"
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
			filename := part.FileName()
			if c.sensitiveDebugKey("filename") {
				filename = debugRedacted
			}
			value := fmt.Sprintf("[file: %s; content omitted]", filename)
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
		if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
			return "[omitted: unsupported content type for field redaction]"
		}
		value := jsontext.Value(data)
		if !value.IsValid() {
			return "[omitted: invalid JSON]"
		}
		redacted, err := c.redactDebugJSON(value)
		if err != nil {
			return "[omitted: invalid JSON]"
		}
		return string(redacted)
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
		cookies[cookie.Name] = cookie.Value
		if c.sensitiveDebugKey("Cookie") || c.sensitiveDebugKey(cookie.Name) {
			cookies[cookie.Name] = debugRedacted
		}
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
