package httpc

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Albert-Zhan/httpc/v2/body"
)

func TestDebugCapturesTransportHeaders(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", h2), func(t *testing.T) {
			type observation struct {
				header       http.Header
				host, method string
				data         []byte
			}
			observed := make(chan observation, 1)
			wroteRequest := make(chan struct{}, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				observed <- observation{r.Header.Clone(), r.Host, r.Method, data}
				<-wroteRequest
			}))
			server.EnableHTTP2 = h2
			if h2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			var logs bytes.Buffer
			client := NewClient().SetDebugOutput(&logs)
			t.Cleanup(client.CloseIdleConnections)
			if h2 {
				pool := x509.NewCertPool()
				pool.AddCert(server.Certificate())
				client.CustomizeTransport(func(tr *http.Transport) { tr.TLSClientConfig.RootCAs = pool })
			}
			client.WrapTransport(func(http.RoundTripper) http.RoundTripper { return client.transport.Clone() })
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(server.URL)
			jar.SetCookies(u, []*http.Cookie{{Name: "automatic", Value: "jar-value"}})
			virtual := *u
			virtual.Host = "virtual.example"
			jar.SetCookies(&virtual, []*http.Cookie{{Name: "automatic", Value: "jar-value"}})
			client.SetCookieJar(jar)
			for _, tc := range []struct {
				name, method string
				body         bool
				configure    func(*Request)
			}{
				{"default", "GET", false, func(*Request) {}},
				{"custom Host", "GET", false, func(r *Request) { r.SetHost("virtual.example:8443") }},
				{"body", "POST", true, func(*Request) {}},
				{"head", "HEAD", false, func(*Request) {}},
				{"range", "GET", false, func(r *Request) { r.SetHeader("Range", "bytes=0-9") }},
				{"empty UA", "GET", false, func(r *Request) { r.SetHeader("User-Agent", "") }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					logs.Reset()
					target, _ := url.Parse(server.URL + "/path?keep=value#not-sent")
					target.User = url.UserPassword("url-user", "url-password")
					req := NewRequest(client).SetURL(target.String()).
						SetMethod(tc.method).SetCookieList([]*http.Cookie{{Name: "manual", Value: "cookie-value"}}).SetDebug(true)
					if tc.body {
						req.SetBody(rawDebugBody("abc", body.Text))
					}
					tc.configure(req)
					var callerHeaders, callerWrites atomic.Int32
					ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
						WroteHeaderField: func(string, []string) { callerHeaders.Add(1) },
						WroteRequest:     func(httptrace.WroteRequestInfo) { callerWrites.Add(1); wroteRequest <- struct{}{} },
					})
					_, _, err := req.Send(ctx).End()
					if err != nil {
						t.Fatal(err)
					}
					got := <-observed
					records := debugRecords(t, logs.String())
					if len(records) != 1 {
						t.Fatalf("records=%v", records)
					}
					record := records[0]
					headers := debugRecordedHeaders(t, record)
					if record["Header-Source"] != "transport" || record["Write"] != "completed" ||
						callerHeaders.Load() == 0 || callerWrites.Load() != 1 {
						t.Fatalf("missing trace data or caller hooks: %v", record)
					}
					for _, name := range []string{"Accept-Encoding", "User-Agent", "Content-Type", "Content-Length", "Range", "Authorization"} {
						if headers.Get(name) != got.header.Get(name) {
							t.Errorf("%s: log=%q server=%q", name, headers.Get(name), got.header.Get(name))
						}
					}
					if strings.Contains(record["Request"], "not-sent") {
						t.Fatal("fragment was logged as request target")
					}
					var cookies map[string]string
					if err := json.Unmarshal([]byte(record["Cookies"]), &cookies); err != nil {
						t.Fatal(err)
					}
					if cookies["automatic"] != "jar-value" || cookies["manual"] != "cookie-value" {
						t.Fatalf("missing actual cookies: %v", cookies)
					}
					if h2 {
						if record["Protocol"] != "HTTP/2.0" || headers.Get(":authority") != got.host ||
							headers.Get(":method") != got.method || headers.Get(":path") != "/path?keep=value" {
							t.Fatalf("incorrect HTTP/2 fields: %v", record)
						}
					} else if record["Protocol"] != "HTTP/1.1" || headers.Get("Host") != got.host {
						t.Fatalf("incorrect HTTP/1 fields: %v", record)
					}
					if tc.body && (record["Body-Preview"] != "abc" || string(got.data) != "abc") {
						t.Fatal("preview changed sent body")
					}
				})
			}
		})
	}
}

func TestDebugRecordsRedirectHops(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.SetCookie(w, &http.Cookie{Name: "from_redirect", Value: "saved", Path: "/"})
			http.Redirect(w, r, "/final", http.StatusSeeOther)
			return
		}
		if r.Method != "GET" {
			t.Errorf("redirect method=%s", r.Method)
		}
		data, _ := io.ReadAll(r.Body)
		if len(data) != 0 {
			t.Error("redirect retained POST body")
		}
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	var logs bytes.Buffer
	jar, _ := cookiejar.New(nil)
	client := NewClient().SetCookieJar(jar).SetDebugOutput(&logs)
	t.Cleanup(client.CloseIdleConnections)
	_, text, err := NewRequest(client).SetURL(server.URL + "/start").SetMethod("POST").
		SetBody(rawDebugBody("original", body.Text)).SetDebug(true).Send().End()
	if err != nil || text != "ok" {
		t.Fatalf("body=%q err=%v", text, err)
	}
	records := debugRecords(t, logs.String())
	if len(records) != 2 {
		t.Fatalf("records=%v", records)
	}
	first, second := records[0], records[1]
	if first["Request-ID"] != second["Request-ID"] || first["Hop"] != "1" || second["Hop"] != "2" ||
		first["Attempt"] != "1" || second["Attempt"] != "1" ||
		first["Request"] != "POST "+server.URL+"/start" || second["Request"] != "GET "+server.URL+"/final" ||
		first["Body-Preview"] != "original" || second["Body-Preview"] != "" {
		t.Fatalf("redirect records mixed: %v", records)
	}
	headers := debugRecordedHeaders(t, second)
	if !strings.Contains(headers.Get("Cookie"), "from_redirect=saved") || headers.Get("Referer") != server.URL+"/start" {
		t.Fatalf("redirect-generated headers missing: %v", headers)
	}
}

func TestDebugRecordsTransportRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/retry" && calls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	var logs bytes.Buffer
	client := NewClient().SetDebugOutput(&logs)
	t.Cleanup(client.CloseIdleConnections)
	if _, _, err := NewRequest(client).SetURL(server.URL + "/warm").Send().End(); err != nil {
		t.Fatal(err)
	}
	_, text, err := NewRequest(client).SetURL(server.URL + "/retry").SetDebug(true).Send().End()
	if err != nil || text != "ok" || calls.Load() != 2 {
		t.Fatalf("retry calls=%d body=%q err=%v", calls.Load(), text, err)
	}
	records := debugRecords(t, logs.String())
	if len(records) != 2 || records[0]["Request-ID"] != records[1]["Request-ID"] ||
		records[0]["Hop"] != "1" || records[1]["Hop"] != "1" ||
		records[0]["Attempt"] != "1" || records[1]["Attempt"] != "2" {
		t.Fatalf("retry records mixed: %v", records)
	}
	for _, record := range records {
		if record["Header-Source"] != "transport" {
			t.Fatalf("missing retry headers: %v", record)
		}
	}
}

func TestDebugReportsWriteFailures(t *testing.T) {
	t.Run("before connection", func(t *testing.T) {
		var logs bytes.Buffer
		var callerConnects atomic.Int32
		cause := errors.New("dial unavailable")
		client := NewClient().SetDebugOutput(&logs).CustomizeTransport(func(tr *http.Transport) {
			tr.DialContext = func(context.Context, string, string) (net.Conn, error) { return nil, cause }
		})
		ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
			GetConn: func(string) { callerConnects.Add(1) },
		})
		_, _, err := NewRequest(client).SetURL("http://example.test/").SetDebug(true).Send(ctx).End()
		assertError(t, err, ErrSendRequest, cause)
		records := debugRecords(t, logs.String())
		if len(records) != 1 || records[0]["Write"] != "not_started" || records[0]["Header-Source"] != "request snapshot" || callerConnects.Load() != 1 {
			t.Fatalf("incorrect pre-send failure: %v", records)
		}
	})
	t.Run("body read", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body) }))
		defer server.Close()
		var logs bytes.Buffer
		client := NewClient().SetDebugOutput(&logs)
		t.Cleanup(client.CloseIdleConnections)
		data := debugBodyProbe{io.NopCloser(failingReader{io.ErrUnexpectedEOF})}
		_, _, err := NewRequest(client).SetURL(server.URL).SetMethod("POST").SetBody(data).SetDebug(true).Send().End()
		assertError(t, err, ErrSendRequest, io.ErrUnexpectedEOF)
		records := debugRecords(t, logs.String())
		if len(records) != 1 || records[0]["Write"] != "failed" || records[0]["Body-Preview"] != "[omitted: body is not replayable]" {
			t.Fatalf("incorrect body write failure: %v", records)
		}
		if debugRecordedHeaders(t, records[0]).Get("Transfer-Encoding") != "chunked" {
			t.Fatal("missing transfer encoding")
		}
	})
}

func TestDebugIgnoresLateEventsAndComposesCallerTrace(t *testing.T) {
	var logs bytes.Buffer
	var late *httptrace.ClientTrace
	var callerEvents atomic.Int32
	client := NewClient().SetDebugOutput(&logs)
	client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
		late = httptrace.ContextClientTrace(r.Context())
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
	})
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		WroteHeaderField: func(string, []string) { callerEvents.Add(1) },
		WroteRequest:     func(httptrace.WroteRequestInfo) { callerEvents.Add(1) },
	})
	_, _, err := NewRequest(client).SetURL("http://example.test/").SetDebug(true).Send(ctx).End()
	if err != nil {
		t.Fatal(err)
	}
	before := logs.String()
	var workers sync.WaitGroup
	for range 10 {
		workers.Go(func() {
			late.WroteHeaderField("X-Late", []string{"late"})
			late.WroteRequest(httptrace.WroteRequestInfo{})
			late.GotConn(httptrace.GotConnInfo{})
		})
	}
	workers.Wait()
	if logs.String() != before || callerEvents.Load() != 20 {
		t.Fatal("late event changed output or caller hooks were lost")
	}
}

func TestDebugRedactsHTTP2FieldsAndAutomaticCookies(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "query-secret" || r.Header.Get("Authorization") != "Bearer auth-secret" {
			t.Error("logging changed request credentials")
		}
		cookie, err := r.Cookie("chosen_cookie")
		if err != nil || cookie.Value != "cookie-secret" {
			t.Error("logging changed automatic cookie")
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(server.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "chosen_cookie", Value: "cookie-secret"}, {Name: "visible", Value: "cookie-visible"}})
	var logs bytes.Buffer
	client := NewClient().SetCookieJar(jar).SetDebugOutput(&logs).
		AddDebugSensitiveFields("token", "chosen_cookie", "Authorization", "Host").
		CustomizeTransport(func(tr *http.Transport) { tr.TLSClientConfig.RootCAs = pool })
	t.Cleanup(client.CloseIdleConnections)
	_, _, err := NewRequest(client).SetURL(server.URL+"/path?token=query-secret&keep=visible").
		SetHeader("Authorization", "Bearer auth-secret").SetDebug(true).Send().End()
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"query-secret", "auth-secret", "cookie-secret"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("secret appeared in transport log: %s", secret)
		}
	}
	records := debugRecords(t, logs.String())
	if len(records) != 1 {
		t.Fatalf("records=%v", records)
	}
	headers := debugRecordedHeaders(t, records[0])
	if headers.Get(":authority") != debugRedacted ||
		headers.Get(":path") != "/path?keep=visible&token=%5BREDACTED%5D" ||
		!strings.Contains(logs.String(), "cookie-visible") {
		t.Fatalf("incorrect HTTP/2 redaction: %v", records)
	}
}

func TestDebugRecordsFailedReconnect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/retry" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	cause := errors.New("reconnect unavailable")
	var dials atomic.Int32
	var logs bytes.Buffer
	client := NewClient().SetDebugOutput(&logs).CustomizeTransport(func(tr *http.Transport) {
		dial := tr.DialContext
		tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			if dials.Add(1) > 1 {
				return nil, cause
			}
			return dial(ctx, network, address)
		}
	})
	t.Cleanup(client.CloseIdleConnections)
	if _, _, err := NewRequest(client).SetURL(server.URL + "/warm").Send().End(); err != nil {
		t.Fatal(err)
	}
	_, _, err := NewRequest(client).SetURL(server.URL + "/retry").SetDebug(true).Send().End()
	assertError(t, err, ErrSendRequest, cause)
	records := debugRecords(t, logs.String())
	if len(records) != 2 || records[0]["Header-Source"] != "transport" ||
		records[1]["Attempt"] != "2" || records[1]["Write"] != "not_started" ||
		records[1]["Header-Source"] != "request snapshot" {
		t.Fatalf("failed reconnect was not recorded separately: %v", records)
	}
}

func TestDebugPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	defer server.Close()
	var logs bytes.Buffer
	client := NewClient().SetDebugOutput(&logs).WrapTransport(func(next http.RoundTripper) http.RoundTripper { return testRoundTripper(next.RoundTrip) })
	t.Cleanup(client.CloseIdleConnections)
	_, _, err := NewRequest(client).SetURL(server.URL).SetDebug(true).Send(ctx).End()
	assertError(t, err, ErrSendRequest, context.Canceled)
	if len(debugRecords(t, logs.String())) != 1 {
		t.Fatal("cancelled request record missing")
	}
}

func TestDebugCapturesRewrittenWrappedRequest(t *testing.T) {
	type observation struct{ path, query, method, body, signature string }
	observed := make(chan observation, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		observed <- observation{r.URL.Path, r.URL.Query().Get("token"), r.Method, string(content), r.Header.Get("X-Signature")}
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	var logs bytes.Buffer
	client := NewClient().SetDebugOutput(&logs).AddDebugSensitiveFields("token", "password", "X-Signature")
	t.Cleanup(client.CloseIdleConnections)
	const payload = `{"keep":"wrapped-body","password":"body-secret"}`
	client.WrapTransport(func(next http.RoundTripper) http.RoundTripper {
		return testRoundTripper(func(req *http.Request) (*http.Response, error) {
			changed := req.Clone(req.Context())
			changed.URL.Path = "/wrapped"
			changed.URL.RawQuery = "token=query-secret"
			changed.Method = "POST"
			if changed.Body != nil {
				_ = changed.Body.Close()
			}
			changed.Body = io.NopCloser(strings.NewReader(payload))
			changed.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(payload)), nil }
			changed.ContentLength = int64(len(payload))
			changed.Header.Set("Content-Type", "application/json")
			changed.Header.Set("X-Signature", "signature-secret")
			return next.RoundTrip(changed)
		})
	}).WrapTransport(func(next http.RoundTripper) http.RoundTripper {
		return testRoundTripper(func(req *http.Request) (*http.Response, error) {
			changed := req.Clone(req.Context())
			changed.Header.Set("X-Outer", "visible")
			return next.RoundTrip(changed)
		})
	})
	req := NewRequest(client).SetURL(server.URL + "/original").SetBody(rawDebugBody("original-body", body.Text)).SetDebug(true)
	_, text, err := req.Send().End()
	if err != nil || text != "ok" {
		t.Fatalf("body=%q error=%v", text, err)
	}
	got := <-observed
	if got.path != "/wrapped" || got.query != "query-secret" || got.method != "POST" || got.body != payload || got.signature != "signature-secret" {
		t.Fatalf("wrong wrapped request: %+v", got)
	}
	records := debugRecords(t, logs.String())
	if len(records) != 1 {
		t.Fatalf("duplicate logs for wrapper layers: %v", records)
	}
	record := records[0]
	if record["Header-Source"] != "transport" || record["Request"] != "POST "+server.URL+"/wrapped?token=%5BREDACTED%5D" ||
		record["Body-Preview"] != `{"keep":"wrapped-body","password":"[REDACTED]"}` {
		t.Fatalf("log shows pre-wrapper request: %v", record)
	}
	headers := debugRecordedHeaders(t, record)
	if headers.Get("X-Signature") != debugRedacted || headers.Get("X-Outer") != "visible" || headers.Get("Content-Length") != fmt.Sprint(len(payload)) {
		t.Fatalf("wrong wrapped headers: %v", headers)
	}
	for _, secret := range []string{"query-secret", "body-secret", "signature-secret", "original-body"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("incorrect preview or redaction: %s", secret)
		}
	}
	if req.request.URL.Path != "/original" || req.header.Get("X-Signature") != "" {
		t.Fatal("wrapper changed the original builder")
	}
}

func TestDebugWrapTransportWithoutTraceKeepsSnapshot(t *testing.T) {
	var logs bytes.Buffer
	client := NewClient().SetDebugOutput(&logs).WrapTransport(func(http.RoundTripper) http.RoundTripper {
		return testRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Proto: "HTTP/1.1", Header: make(http.Header), Body: http.NoBody}, nil
		})
	})
	t.Cleanup(client.CloseIdleConnections)
	_, _, err := NewRequest(client).SetURL("http://example.test/").SetHeader("X-Visible", "value").SetDebug(true).Send().End()
	if err != nil {
		t.Fatal(err)
	}
	records := debugRecords(t, logs.String())
	if len(records) != 1 || records[0]["Header-Source"] != "request snapshot" || records[0]["Write"] != "unobserved" || debugRecordedHeaders(t, records[0]).Get("X-Visible") != "value" {
		t.Fatalf("opaque transport log missing or misrepresented: %v", records)
	}
}

func TestDebugWrapTransportWithDroppedContextIsUnobserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "sent") }))
	defer server.Close()
	var logs bytes.Buffer
	client := NewClient().SetDebugOutput(&logs).WrapTransport(func(next http.RoundTripper) http.RoundTripper {
		return testRoundTripper(func(req *http.Request) (*http.Response, error) {
			return next.RoundTrip(req.WithContext(context.Background()))
		})
	})
	t.Cleanup(client.CloseIdleConnections)
	_, text, err := NewRequest(client).SetURL(server.URL).SetDebug(true).Send().End()
	if err != nil || text != "sent" {
		t.Fatalf("body=%q error=%v", text, err)
	}
	records := debugRecords(t, logs.String())
	if len(records) != 1 || records[0]["Header-Source"] != "request snapshot" || records[0]["Write"] != "unobserved" {
		t.Fatalf("missing trace was represented as actual capture: %v", records)
	}
}

func debugRecords(t *testing.T, output string) []map[string]string {
	t.Helper()
	var records []map[string]string
	for _, block := range strings.Split(output, "[httpc Debug]\n")[1:] {
		record := make(map[string]string)
		for _, line := range strings.Split(block, "\n") {
			if key, value, ok := strings.Cut(line, ": "); ok {
				record[key] = value
			}
		}
		if record["Request-ID"] == "" || record["Attempt"] == "" {
			t.Fatalf("incomplete record: %s", block)
		}
		records = append(records, record)
	}
	return records
}

func debugRecordedHeaders(t *testing.T, record map[string]string) http.Header {
	t.Helper()
	var raw http.Header
	if err := json.Unmarshal([]byte(record["Header"]), &raw); err != nil {
		t.Fatal(err)
	}
	headers := make(http.Header)
	for name, values := range raw {
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	return headers
}
