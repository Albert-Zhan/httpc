package tlsclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Albert-Zhan/httpc/v2"
	"github.com/Albert-Zhan/httpc/v2/body"
	fhttp "github.com/bogdanfinn/fhttp"
)

func TestResponseRedirectHistory(t *testing.T) {
	type historyEntry struct {
		status                         int
		method, path, location, marker string
	}
	for _, h1 := range []bool{true, false} {
		for _, tc := range []struct {
			name, path, method string
			follow             bool
			statuses           []int
		}{
			{"direct", "/finish", "GET", true, []int{200}},
			{"single", "/single", "GET", true, []int{200, 307}},
			{"multiple", "/first", "GET", true, []int{200, 307, 302, 301}},
			{"not followed", "/first", "GET", false, []int{301}},
			{"method change", "/post", "POST", true, []int{200, 303}},
		} {
			t.Run(fmt.Sprintf("http1=%v/%s", h1, tc.name), func(t *testing.T) {
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("X-History", r.URL.Path)
					switch r.URL.Path {
					case "/single", "/third":
						w.Header().Set("Location", "/finish")
						w.WriteHeader(307)
					case "/first":
						w.Header().Set("Location", "/second")
						w.WriteHeader(301)
					case "/second":
						w.Header().Set("Location", "/third")
						w.WriteHeader(302)
					case "/post":
						w.Header().Set("Location", "/finish")
						w.WriteHeader(303)
					default:
						_, _ = io.WriteString(w, "ok")
					}
				}))
				server.EnableHTTP2 = !h1
				server.StartTLS()
				defer server.Close()
				native, _ := newTestClient(t, h1)
				backend, client := newTestClient(t, h1)
				native.SetFollowRedirect(tc.follow)
				backend.SetFollowRedirect(tc.follow)
				key := struct{}{}
				ctx := context.WithValue(context.Background(), key, "kept")
				payload := ""
				if tc.method == "POST" {
					payload = "payload"
				}
				nreq, err := fhttp.NewRequestWithContext(ctx, tc.method, server.URL+tc.path, strings.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}
				nreq.Header.Set("User-Agent", httpc.DefaultUserAgent)
				if payload != "" {
					nreq.Header.Set("Content-Type", body.Text)
				}
				nresp, err := native.Do(nreq)
				if err != nil {
					t.Fatal(err)
				}
				_, err = io.Copy(io.Discard, nresp.Body)
				_ = nresp.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				var want []historyEntry
				for r := nresp; r != nil; r = r.Request.Response {
					want = append(want, historyEntry{r.StatusCode, r.Request.Method, r.Request.URL.Path, r.Header.Get("Location"), r.Header.Get("X-History")})
				}
				req := httpc.NewRequest(client).SetURL(server.URL + tc.path).SetMethod(tc.method)
				if payload != "" {
					data := body.NewRawData()
					data.SetData(payload, body.Text)
					req.SetBody(data)
				}
				resp, _, err := req.Send(ctx).End()
				if err != nil {
					t.Fatal(err)
				}
				var got []historyEntry
				var statuses []int
				for r := resp; r != nil; r = r.Request.Response {
					if r.Request == nil || r.Request.Context().Value(key) != "kept" {
						t.Fatal("missing historical request or context")
					}
					got = append(got, historyEntry{r.StatusCode, r.Request.Method, r.Request.URL.Path, r.Header.Get("Location"), r.Header.Get("X-History")})
					statuses = append(statuses, r.StatusCode)
				}
				if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(statuses, tc.statuses) {
					t.Fatalf("history=%v native=%v statuses=%v", got, want, statuses)
				}
			})
		}
	}
}

func TestResponseConversionPreservesGraphWithoutBodyIO(t *testing.T) {
	key := struct{}{}
	ctx := context.WithValue(context.Background(), key, "kept")
	request, err := fhttp.NewRequestWithContext(ctx, "GET", "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Request", "original")
	historyBody := &conversionBody{}
	_ = historyBody.Close()
	finalBody := &conversionBody{}
	previous := &fhttp.Response{StatusCode: 307, Request: request, Body: historyBody, Header: fhttp.Header{"Location": {"/finish"}}}
	final := &fhttp.Response{StatusCode: 200, Request: request, Body: finalBody}
	// 同一个请求被两份响应引用，并通过来源响应构成循环。
	request.Response = previous
	converted := toResponse(final)
	prior := converted.Request.Response
	if prior == nil || prior.StatusCode != 307 || prior.Request != converted.Request || prior.Request.Response != prior {
		t.Fatal("lost shared objects or cyclic response references")
	}
	if prior.Request.Context().Value(key) != "kept" {
		t.Fatal("lost context")
	}
	prior.Header.Set("Location", "/changed")
	prior.Request.Header.Set("X-Request", "changed")
	if previous.Header.Get("Location") != "/finish" || request.Header.Get("X-Request") != "original" {
		t.Fatal("conversion shares mutable header maps")
	}
	if historyBody.reads != 0 || historyBody.closes != 1 || finalBody.reads != 0 || finalBody.closes != 0 {
		t.Fatalf("conversion touched body: history=%+v final=%+v", historyBody, finalBody)
	}
	request.Response = final
	self := toResponse(final)
	if self.Request.Response != self {
		t.Fatal("lost response self-reference")
	}
}

type conversionBody struct{ reads, closes int }

func (b *conversionBody) Read([]byte) (int, error) { b.reads++; return 0, io.EOF }
func (b *conversionBody) Close() error             { b.closes++; return nil }
