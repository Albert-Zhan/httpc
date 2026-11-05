package tlsclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Albert-Zhan/httpc/v2"
	"github.com/Albert-Zhan/httpc/v2/body"
	"github.com/Albert-Zhan/httpc/v2/downloader"
	fhttp "github.com/bogdanfinn/fhttp"
	ftrace "github.com/bogdanfinn/fhttp/httptrace"
	tls_client "github.com/bogdanfinn/tls-client"
	utls "github.com/bogdanfinn/utls"
)

func newTestClient(t *testing.T, h1 bool) (tls_client.HttpClient, *httpc.Client) {
	t.Helper()
	opts := []tls_client.HttpClientOption{tls_client.WithInsecureSkipVerify(), tls_client.WithDisableHttp3(), tls_client.WithTimeoutSeconds(0), tls_client.WithCookieJar(tls_client.NewCookieJar())}
	if h1 {
		opts = append(opts, tls_client.WithForceHttp1())
	}
	raw, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(raw.Close)
	c, err := New(raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw, c
}

func TestRequestRedirectAndTrace(t *testing.T) {
	for _, h1 := range []bool{true, false} {
		t.Run(fmt.Sprintf("http1=%v", h1), func(t *testing.T) {
			binary := []byte{0, 255, 1, 2, 3}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/start" {
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "kept", Path: "/"})
					w.Header().Set("Location", "/finish")
					w.WriteHeader(307)
					return
				}
				data, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(data, binary) || r.Header.Get("User-Agent") != httpc.DefaultUserAgent || r.Header.Get("Cookie") != "session=kept" || len(r.Header.Values("X-Tag")) != 2 {
					t.Errorf("data=%x UA=%q Cookie=%q headers=%v err=%v", data, r.UserAgent(), r.Header.Get("Cookie"), r.Header, err)
				}
				w.Header().Set("Trailer", "X-Checksum")
				_, _ = w.Write(data)
				w.Header().Set("X-Checksum", "done")
			}))
			server.EnableHTTP2 = !h1
			server.StartTLS()
			defer server.Close()
			_, c := newTestClient(t, h1)
			var log bytes.Buffer
			c.SetDebugOutput(&log).AddDebugSensitiveFields("session")
			var wrote, gotConn, native atomic.Int32
			ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
				WroteRequest: func(httptrace.WroteRequestInfo) { wrote.Add(1) },
				GotConn:      func(httptrace.GotConnInfo) { gotConn.Add(1) },
			})
			ctx = ftrace.WithClientTrace(ctx, &ftrace.ClientTrace{WroteRequest: func(ftrace.WroteRequestInfo) { native.Add(1) }})
			payload := body.NewRawData()
			payload.SetData(binary, body.Protobuf)
			resp, data, err := httpc.NewRequest(c).SetURL(server.URL+"/start").SetMethod("POST").
				SetBody(payload).AddHeader("X-Tag", "one").AddHeader("X-Tag", "two").SetDebug(true).Send(ctx).EndByte()
			if err != nil || !bytes.Equal(data, binary) {
				t.Fatal(resp, data, err)
			}
			if resp.Request.URL.Path != "/finish" || resp.Trailer.Get("X-Checksum") != "done" || resp.TLS == nil || !resp.TLS.HandshakeComplete {
				t.Fatalf("response=%+v", resp)
			}
			if wrote.Load() < 2 || gotConn.Load() < 2 || native.Load() != wrote.Load() {
				t.Fatal("trace", wrote.Load(), gotConn.Load(), native.Load())
			}
			if !h1 && resp.ProtoMajor != 2 {
				t.Fatal(resp.Proto)
			}
			if !strings.Contains(log.String(), "Header-Source: transport") || !strings.Contains(log.String(), resp.Proto) {
				t.Fatal(log.String())
			}
		})
	}
}

func TestRequestHostThroughAdapter(t *testing.T) {
	for _, h1 := range []bool{true, false} {
		t.Run(fmt.Sprintf("http1=%v", h1), func(t *testing.T) {
			type observation struct {
				host, sni, path, cookie string
				proto                   int
			}
			seen := make(chan observation, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- observation{r.Host, r.TLS.ServerName, r.URL.Path, r.Header.Get("Cookie"), r.ProtoMajor}
			}))
			server.EnableHTTP2 = !h1
			server.StartTLS()
			defer server.Close()
			raw, client := newTestClient(t, h1)
			origin, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			raw.SetCookies(origin, []*fhttp.Cookie{{Name: "origin_cookie", Value: "kept"}})
			virtual := *origin
			virtual.Host = "virtual.example"
			raw.SetCookies(&virtual, []*fhttp.Cookie{{Name: "host_cookie", Value: "not_sent"}})
			for _, host := range []string{"virtual.example:8443", ""} {
				req := httpc.NewRequest(client).SetHost("old.example").SetHost(host).SetURL(server.URL + "/path")
				resp, _, err := req.Send().End()
				if err != nil {
					t.Fatal(err)
				}
				got := <-seen
				want := host
				if want == "" {
					want = resp.Request.URL.Host
				}
				proto := 2
				if h1 {
					proto = 1
				}
				if got.host != want || got.proto != proto || got.path != "/path" || got.sni != "" || got.cookie != "origin_cookie=kept" {
					t.Fatalf("observed=%+v want Host=%q proto=%d", got, want, proto)
				}
			}
		})
	}
}

func TestRawHeadersMatchNative(t *testing.T) {
	for _, h1 := range []bool{true, false} {
		for _, tc := range []struct {
			name, userAgent string
			redirect        bool
		}{
			{"custom", "raw-agent", false},
			{"empty UA", "", false},
			{"redirect", "raw-agent", true},
		} {
			t.Run(fmt.Sprintf("http1=%v/%s", h1, tc.name), func(t *testing.T) {
				payload := `{"password":"body-secret","keep":"visible"}`
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					data, err := io.ReadAll(r.Body)
					if err != nil || string(data) != payload || r.UserAgent() != tc.userAgent || len(r.Header.Values("Content-Type")) != 1 {
						t.Errorf("body=%q UA=%q headers=%v err=%v", data, r.UserAgent(), r.Header, err)
					}
					if h1 {
						// 每跳独立连接，便于捕获各次请求的原始 HTTP/1.1 头行。
						w.Header().Set("Connection", "close")
					}
					if r.URL.Path == "/start" {
						http.SetCookie(w, &http.Cookie{Name: "session", Value: "kept", Path: "/"})
						w.Header().Set("Location", "/finish")
						w.WriteHeader(http.StatusTemporaryRedirect)
						return
					}
					_, _ = io.WriteString(w, "ok")
				}))
				wire := make(chan string, 8)
				if h1 {
					server.Listener = &headerCaptureListener{Listener: server.Listener, headers: wire}
					server.Start()
				} else {
					server.EnableHTTP2 = true
					server.StartTLS()
				}
				defer server.Close()
				native, _ := newTestClient(t, h1)
				_, wrapped := newTestClient(t, h1)
				var logs bytes.Buffer
				wrapped.SetDebugOutput(&logs).AddDebugSensitiveFields("x-token", "password")
				target := server.URL + "/finish"
				if tc.redirect {
					target = server.URL + "/start"
				}
				headers := fhttp.Header{
					"user-agent": {tc.userAgent}, "content-type": {"application/json"},
					"content-length": {fmt.Sprint(len(payload))}, "Accept-Encoding": {"identity"},
					"x-device-id": {"abc", "def"}, "X-Device-Id": {"separate"},
					"x-token": {"header-secret"}, "cookie": {"manual=kept"},
					fhttp.HeaderOrderKey:  {"host", "user-agent", "content-type", "content-length", "x-device-id", "x-token", "accept-encoding", "cookie"},
					fhttp.PHeaderOrderKey: {":method", ":authority", ":scheme", ":path"},
				}
				var mu sync.Mutex
				var nativeFields, wrappedFields []string
				capture := func(dst *[]string, name string, values []string) {
					mu.Lock()
					defer mu.Unlock()
					for _, value := range values {
						*dst = append(*dst, name+": "+value)
					}
				}
				nativeCtx := ftrace.WithClientTrace(context.Background(), &ftrace.ClientTrace{
					WroteHeaderField: func(name string, values []string) { capture(&nativeFields, name, values) },
				})
				nativeReq, err := fhttp.NewRequestWithContext(nativeCtx, "POST", target, strings.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}
				nativeReq.Header = headers.Clone()
				nativeResp, err := native.Do(nativeReq)
				if err != nil {
					t.Fatal(err)
				}
				_, readErr := io.Copy(io.Discard, nativeResp.Body)
				_ = nativeResp.Body.Close()
				if readErr != nil {
					t.Fatal(readErr)
				}
				ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
					WroteHeaderField: func(name string, values []string) { capture(&wrappedFields, name, values) },
				})
				data := body.NewRawData()
				data.SetData(payload, body.JSON)
				req := httpc.NewRequest(wrapped).SetURL(target).SetMethod("POST").SetBody(data).SetDebug(true)
				for name, values := range headers {
					req.SetHeaderRaw(name, values[0])
					for _, value := range values[1:] {
						req.AddHeaderRaw(name, value)
					}
				}
				resp, text, err := req.Send(ctx).End()
				if err != nil || text != "ok" {
					t.Fatalf("response=%v text=%q err=%v", resp, text, err)
				}
				wantProto := 2
				if h1 {
					wantProto = 1
				}
				if resp.ProtoMajor != wantProto || nativeResp.ProtoMajor != wantProto {
					t.Fatalf("wrapped=%s native=%s", resp.Proto, nativeResp.Proto)
				}
				mu.Lock()
				slices.Sort(nativeFields)
				slices.Sort(wrappedFields)
				if len(nativeFields) == 0 || !reflect.DeepEqual(nativeFields, wrappedFields) {
					t.Errorf("wrapped headers=%v native=%v", wrappedFields, nativeFields)
				}
				if !h1 {
					for _, field := range wrappedFields {
						name, _, _ := strings.Cut(field, ": ")
						if name != strings.ToLower(name) {
							t.Errorf("HTTP/2 field is not lowercase: %s", field)
						}
					}
				}
				mu.Unlock()
				if h1 {
					hops := 1
					if tc.redirect {
						hops = 2
					}
					blocks := make([][]string, hops*2)
					for i := range blocks {
						select {
						case block := <-wire:
							blocks[i] = strings.Split(block, "\r\n")
							slices.Sort(blocks[i][1:])
						case <-time.After(5 * time.Second):
							t.Fatal("missing raw HTTP/1.1 request headers")
						}
					}
					if !reflect.DeepEqual(blocks[:hops], blocks[hops:]) {
						t.Fatalf("raw HTTP/1.1 headers differ: native=%v wrapped=%v", blocks[:hops], blocks[hops:])
					}
				}
				output := logs.String()
				if strings.Contains(output, "header-secret") || strings.Contains(output, "body-secret") ||
					!strings.Contains(output, `"x-token":["[REDACTED]"]`) || !strings.Contains(output, "Header-Source: transport") {
					t.Fatal(output)
				}
			})
		}
	}
}

func TestRequestHeaderOrder(t *testing.T) {
	ordinary := []string{"X-SeCoNd", "USER-AGENT", "x-first"}
	pseudo := []string{":path", ":method", ":scheme", ":authority"}
	for _, h1 := range []bool{true, false} {
		for _, tc := range []struct {
			name             string
			ordinary, pseudo []string
		}{
			{"both", ordinary, pseudo},
			{"ordinary only", ordinary, nil},
			{"pseudo only", nil, pseudo},
			{"clear both", nil, nil},
		} {
			t.Run(fmt.Sprintf("http1=%v/%s", h1, tc.name), func(t *testing.T) {
				wire := make(chan string, 4)
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-First") != "first" || r.Header.Get("X-Second") != "second" || r.Header.Get("X-Other") != "unlisted" {
						t.Errorf("lost headers: %v", r.Header)
					}
					if h1 {
						w.Header().Set("Connection", "close")
					}
					if r.URL.Path == "/start" {
						http.SetCookie(w, &http.Cookie{Name: "session", Value: "kept", Path: "/"})
						w.Header().Set("Location", "/finish")
						w.WriteHeader(http.StatusTemporaryRedirect)
						return
					}
					if r.Header.Get("Cookie") != "session=kept" {
						t.Errorf("redirect cookie=%q", r.Header.Get("Cookie"))
					}
					_, _ = io.WriteString(w, "ok")
				}))
				if h1 {
					server.Listener = &headerCaptureListener{Listener: server.Listener, headers: wire}
					server.Start()
				} else {
					server.EnableHTTP2 = true
					server.StartTLS()
				}
				defer server.Close()
				native, _ := newTestClient(t, h1)
				_, client := newTestClient(t, h1)
				var logs bytes.Buffer
				client.SetDebugOutput(&logs)
				headers := fhttp.Header{
					"user-agent": {"order-agent"}, "Accept-Encoding": {"identity"},
					"X-SeCoNd": {"second"}, "x-first": {"first"}, "X-Other": {"unlisted"},
				}
				var mu sync.Mutex
				var nativeSequence, wrappedSequence []string
				record := func(dst *[]string, name string) {
					mu.Lock()
					defer mu.Unlock()
					*dst = append(*dst, name)
				}
				ctx := ftrace.WithClientTrace(context.Background(), &ftrace.ClientTrace{
					WroteHeaderField: func(name string, _ []string) { record(&nativeSequence, name) },
					WroteHeaders:     func() { record(&nativeSequence, "end") },
				})
				nativeReq, err := fhttp.NewRequestWithContext(ctx, "GET", server.URL+"/start", nil)
				if err != nil {
					t.Fatal(err)
				}
				nativeReq.Header = headers.Clone()
				if len(tc.ordinary) > 0 {
					nativeReq.Header[fhttp.HeaderOrderKey] = slices.Clone(tc.ordinary)
				}
				if len(tc.pseudo) > 0 {
					nativeReq.Header[fhttp.PHeaderOrderKey] = slices.Clone(tc.pseudo)
				}
				nativeResp, err := native.Do(nativeReq)
				if err != nil {
					t.Fatal(err)
				}
				_, readErr := io.Copy(io.Discard, nativeResp.Body)
				_ = nativeResp.Body.Close()
				if readErr != nil {
					t.Fatal(readErr)
				}
				stdCtx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
					WroteHeaderField: func(name string, _ []string) { record(&wrappedSequence, name) },
					WroteHeaders:     func() { record(&wrappedSequence, "end") },
				})
				req := httpc.NewRequest(client).SetURL(server.URL + "/start").SetDebug(true)
				for name, values := range headers {
					req.SetHeaderRaw(name, values[0])
				}
				req.SetHeaderOrder("x-other").SetHeaderOrder(tc.ordinary...).
					SetPseudoHeaderOrder(":method", ":authority", ":scheme", ":path").SetPseudoHeaderOrder(tc.pseudo...)
				resp, text, err := req.Send(stdCtx).End()
				if err != nil || text != "ok" || resp.ProtoMajor != nativeResp.ProtoMajor || (!h1 && resp.ProtoMajor != 2) {
					t.Fatalf("response=%v body=%q err=%v", resp, text, err)
				}
				mu.Lock()
				if !reflect.DeepEqual(wrappedSequence, nativeSequence) {
					t.Errorf("header order wrapped=%v native=%v", wrappedSequence, nativeSequence)
				}
				sequence := slices.Clone(wrappedSequence)
				mu.Unlock()
				hops := 0
				for len(sequence) > 0 {
					end := slices.Index(sequence, "end")
					if end < 0 {
						t.Fatal("incomplete header trace", sequence)
					}
					fields := sequence[:end]
					sequence = sequence[end+1:]
					hops++
					if !h1 {
						if len(fields) < 4 || (len(tc.pseudo) > 0 && !slices.Equal(fields[:4], tc.pseudo)) {
							t.Fatalf("pseudo header order=%v want=%v", fields, tc.pseudo)
						}
						fields = fields[4:]
					}
					if len(tc.ordinary) > 0 {
						if len(fields) < len(tc.ordinary) {
							t.Fatal("missing ordinary headers", fields)
						}
						for i, name := range tc.ordinary {
							if !strings.EqualFold(fields[i], name) {
								t.Fatalf("ordinary order=%v want=%v", fields, tc.ordinary)
							}
						}
					}
					for _, name := range fields {
						if name == fhttp.HeaderOrderKey || name == fhttp.PHeaderOrderKey || strings.HasPrefix(name, ":") {
							t.Fatalf("unexpected control field on wire: %q", name)
						}
					}
				}
				if hops != 2 {
					t.Fatalf("trace hops=%d want=2", hops)
				}
				if h1 {
					blocks := make([]string, 4)
					for i := range blocks {
						select {
						case blocks[i] = <-wire:
						case <-time.After(5 * time.Second):
							t.Fatal("missing raw HTTP/1.1 headers")
						}
					}
					if !slices.Equal(blocks[:2], blocks[2:]) {
						t.Fatalf("raw HTTP/1.1 header order differs: native=%v wrapped=%v", blocks[:2], blocks[2:])
					}
				}
				if !strings.Contains(logs.String(), "Header-Source: transport") {
					t.Fatal(logs.String())
				}
			})
		}
	}
}

type headerCaptureListener struct {
	net.Listener
	headers chan<- string
}

func (l *headerCaptureListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &headerCaptureConn{Conn: conn, headers: l.headers}, nil
}

type headerCaptureConn struct {
	net.Conn
	headers chan<- string
	pending []byte
	done    bool
}

func (c *headerCaptureConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if !c.done {
		c.pending = append(c.pending, p[:n]...)
		if end := bytes.Index(c.pending, []byte("\r\n\r\n")); end >= 0 {
			c.headers <- string(c.pending[:end])
			c.pending, c.done = nil, true
		}
	}
	return n, err
}

func TestStreamingAndCancellation(t *testing.T) {
	flushed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		flushed <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	_, c := newTestClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := httpc.NewRequest(c).SetURL(server.URL).Send(ctx)
	if r.GetError() != nil {
		t.Fatal(r.GetError())
	}
	<-flushed                // Send 已返回响应头，未等待完整正文。
	c.CloseIdleConnections() // 不应关闭仍在使用的响应。
	cancel()
	_, _, err := r.EndByte()
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDownloaderWithTLSClient(t *testing.T) {
	data := bytes.Repeat([]byte("binary\x00\xff"), 10000)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			_, _ = w.Write(data)
			return
		}
		var start, end int
		_, _ = fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
		w.Header().Set("ETag", `"file-v1"`)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.WriteHeader(206)
		_, _ = w.Write(data[start : end+1])
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	_, c := newTestClient(t, false)
	var logs bytes.Buffer
	c.SetDebugOutput(&logs)
	d, err := downloader.New(c, downloader.Options{Workers: 3, ChunkSize: 8192, Debug: true})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "data")
	result, err := d.Download(context.Background(), server.URL, path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) || result.Mode != "parallel" || !strings.Contains(strings.ToLower(logs.String()), "range") {
		t.Fatal(result, err, logs.String())
	}
}

func TestTraceBridgeTLSAndErrors(t *testing.T) {
	stop := errors.New("stop informational response")
	var count int
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		TLSHandshakeDone: func(s tls.ConnectionState, err error) {
			count++
			if s.NegotiatedProtocol != "h2" || err != stop {
				t.Error(s, err)
			}
		},
		Got1xxResponse: func(int, textproto.MIMEHeader) error { return stop },
	})
	ctx = ftrace.WithClientTrace(ctx, &ftrace.ClientTrace{Got1xxResponse: func(int, textproto.MIMEHeader) error { t.Error("called after stop"); return nil }})
	f := ftrace.ContextClientTrace(bridgeTrace(ctx))
	f.TLSHandshakeDone(utls.ConnectionState{NegotiatedProtocol: "h2"}, stop)
	if count != 1 || !errors.Is(f.Got1xxResponse(103, nil), stop) {
		t.Fatal(count)
	}
}

type fakeRaw struct {
	tls_client.HttpClient
	send   func(*fhttp.Request) (*fhttp.Response, error)
	closes int
}

func (f *fakeRaw) Do(r *fhttp.Request) (*fhttp.Response, error) { return f.send(r) }
func (f *fakeRaw) CloseIdleConnections()                        { f.closes++ }

func TestConversionPreservesStreamAndTrailers(t *testing.T) {
	trailer := http.Header{"X-Checksum": nil}
	src := &trailerReader{Reader: strings.NewReader("data"), trailer: trailer}
	raw := &fakeRaw{send: func(r *fhttp.Request) (*fhttp.Response, error) {
		if r.Body == nil || r.Host != "virtual.example" || r.ContentLength != 4 {
			t.Error(r)
		}
		b, err := io.ReadAll(r.Body)
		if err != nil || string(b) != "data" || r.Trailer.Get("X-Checksum") != "sent" {
			t.Error(string(b), err, r.Trailer)
		}
		return &fhttp.Response{StatusCode: 200, ContentLength: 4, Body: io.NopCloser(strings.NewReader("body")), Request: r}, nil
	}}
	c, err := New(raw)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", "http://example.com", src)
	req.Host = "virtual.example"
	req.ContentLength = 4
	req.Trailer = trailer
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	c.CloseIdleConnections()
	if raw.closes != 1 {
		t.Fatal(raw.closes)
	}
	if req.Header.Get("User-Agent") != "" {
		t.Fatal("caller mutated")
	}
}

type trailerReader struct {
	*strings.Reader
	trailer http.Header
}

func (r *trailerReader) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	if err == io.EOF {
		r.trailer.Set("X-Checksum", "sent")
	}
	return n, err
}
func (r *trailerReader) Close() error { return nil }
