package tlsclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
