package httpc

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Albert-Zhan/httpc/body"
)

func rawDebugBody(data, contentType string) body.Body {
	b := body.NewRawData()
	b.SetData(data, contentType)
	return b
}

func TestDebugRedactsWithoutChangingRequests(t *testing.T) {
	file := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(file, []byte("file-content-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		body    body.Body
		secrets []string
	}{
		{"JSON", rawDebugBody(`{"password":"json-secret","nested":[{"api_key":"nested-secret"}],"business_credential":"business-secret","keep":"visible-value","amount":9007199254740993}`, body.Json), []string{"json-secret", "nested-secret", "business-secret"}},
		{"URL encoded", body.NewUrlEncode().SetData("PASSWORD", "form-secret").SetData("business_credential", "business-secret").SetData("keep", "visible-value"), []string{"form-secret", "business-secret"}},
		{"multipart", body.NewFormData().SetData("token", "multipart-secret").SetData("business_credential", "business-secret").SetData("keep", "visible-value").SetFile("upload", file), []string{"multipart-secret", "business-secret", "file-content-secret"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type receivedRequest struct {
				query url.Values
				head  http.Header
				body  string
			}
			received := make(chan receivedRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				received <- receivedRequest{r.URL.Query(), r.Header.Clone(), string(data)}
			}))
			defer server.Close()
			var logs bytes.Buffer
			client := NewHttpClient().SetDebugOutput(&logs).AddDebugSensitiveFields("Business-Credential")
			defer client.CloseIdleConnections()
			u, _ := url.Parse(server.URL)
			u.User = url.UserPassword("private-user", "private-password")
			u.RawQuery = "access_token=query-secret&keep=visible-query&business_credential=query-business-secret"
			u.Fragment = "fragment-secret"
			cookies := []*http.Cookie{{Name: "ordinary", Value: "cookie-secret"}}
			req := NewRequest(client).SetUrl(u.String()).SetMethod("post").SetBody(tc.body).
				SetHeader("Authorization", "Bearer auth-secret").SetHeader("Proxy-Authorization", "proxy-secret").
				SetHeader("X-API-Key", "header-key-secret").SetHeader("Cookie", "header=header-cookie-secret").
				SetHeader("Referer", "https://example.test/?token=referer-secret").SetCookies(&cookies).SetDebug(true)
			if _, _, err := req.Send().End(); err != nil {
				t.Fatal(err)
			}
			got := <-received
			if got.query.Get("access_token") != "query-secret" || got.query.Get("business_credential") != "query-business-secret" ||
				got.head.Get("Authorization") != "Bearer auth-secret" || !strings.Contains(got.head.Get("Cookie"), "ordinary=cookie-secret") {
				t.Fatalf("debugging changed request credentials: %+v", got)
			}
			text := logs.String()
			secrets := append([]string{"private-user", "private-password", "query-secret", "query-business-secret", "fragment-secret", "auth-secret", "proxy-secret", "header-key-secret", "header-cookie-secret", "cookie-secret", "referer-secret"}, tc.secrets...)
			for _, secret := range secrets {
				if strings.Contains(text, secret) || strings.Contains(text, url.QueryEscape(secret)) {
					t.Errorf("secret %q appeared in debug log", secret)
				}
			}
			for _, secret := range tc.secrets {
				if !strings.Contains(got.body, secret) {
					t.Errorf("request body lost original content %q", secret)
				}
			}
			if !strings.Contains(text, "visible-value") || !strings.Contains(text, "visible-query") || !strings.Contains(text, "REDACTED") {
				t.Fatalf("log did not retain safe fields: %s", text)
			}
			if tc.name == "JSON" && !strings.Contains(text, "9007199254740993") {
				t.Fatal("JSON debug output changed numeric precision")
			}
		})
	}
}

type debugBodyProbe struct {
	reader io.Reader
}

func (b debugBodyProbe) GetData() string        { panic("debug logging must not call GetData") }
func (b debugBodyProbe) GetContentType() string { return "application/json" }
func (b debugBodyProbe) Encode() io.Reader      { return b.reader }

func TestDebugOmitsUnsafeBodies(t *testing.T) {
	for _, tc := range []struct {
		name string
		body body.Body
	}{
		{"invalid JSON", rawDebugBody(`{"password":"body-secret"`, body.Json)},
		{"trailing JSON", rawDebugBody(`{"password":"body-secret"} "trailing-secret"`, body.Json)},
		{"scalar JSON", rawDebugBody(`"body-secret"`, body.Json)},
		{"plain text", rawDebugBody("body-secret", body.Text)},
		{"large body", rawDebugBody(`{"password":"body-secret","data":"`+strings.Repeat("x", maxDebugBody)+`"}`, body.Json)},
		{"streaming body", debugBodyProbe{io.NopCloser(strings.NewReader(`{"password":"body-secret"}`))}},
		{"empty filename", rawDebugBody("--boundary\r\nContent-Disposition: form-data; name=\"upload\"; filename=\"\"\r\n\r\nbody-secret\r\n--boundary--\r\n", "multipart/form-data; boundary=boundary")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(w, r.Body)
			}))
			defer server.Close()
			var logs bytes.Buffer
			client := NewHttpClient().SetDebugOutput(&logs)
			defer client.CloseIdleConnections()
			_, text, err := NewRequest(client).SetUrl(server.URL).SetMethod("post").SetBody(tc.body).SetDebug(true).Send().End()
			if err != nil || !strings.Contains(text, "body-secret") {
				t.Fatalf("body was changed or consumed by debug logging: %q, %v", text, err)
			}
			if strings.Contains(logs.String(), "body-secret") || strings.Contains(logs.String(), "trailing-secret") {
				t.Fatal("unsafe body appeared in debug log")
			}
			if !strings.Contains(logs.String(), "omitted") {
				t.Fatalf("log did not explain omission: %s", logs.String())
			}
		})
	}
}

type failingDebugWriter struct{}

func (failingDebugWriter) Write([]byte) (int, error) { return 0, errors.New("log output unavailable") }

func TestDebugOutputDoesNotAffectRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	var disabled bytes.Buffer
	for _, tc := range []struct {
		name   string
		writer io.Writer
		debug  bool
	}{
		{"debug disabled", &disabled, false},
		{"output failure", failingDebugWriter{}, true},
		{"nil output", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewHttpClient().SetDebugOutput(tc.writer)
			defer client.CloseIdleConnections()
			_, text, err := NewRequest(client).SetUrl(server.URL).SetDebug(tc.debug).Send().End()
			if err != nil || text != "ok" {
				t.Fatalf("debug output affected request: %q, %v", text, err)
			}
		})
	}
	if disabled.Len() != 0 {
		t.Fatal("debug logging ran without SetDebug(true)")
	}
}

func TestConcurrentRequestsShareDebugOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	var logs bytes.Buffer // The shared client must serialize access to this non-thread-safe writer.
	client := NewHttpClient().SetDebugOutput(&logs)
	defer client.CloseIdleConnections()
	const count = 20
	var workers sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			_, text, err := NewRequest(client).SetUrl(server.URL).SetParam("n", fmt.Sprint(i)).SetHeader("X-Token", "concurrent-secret").SetDebug(true).Send().End()
			if err != nil || text != "ok" {
				errs <- fmt.Errorf("request %d: body %q, error %v", i, text, err)
			}
		}(i)
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if strings.Count(logs.String(), "[httpc Debug]") != count || strings.Contains(logs.String(), "concurrent-secret") {
		t.Fatal("concurrent debug records were lost or not redacted")
	}
}
