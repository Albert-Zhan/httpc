package httpc

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Albert-Zhan/httpc/body"
)

func TestRawBinaryBodyReplaysOnRedirect(t *testing.T) {
	// Encoded protobuf fields: string field 1 and bytes field 2.
	payload := []byte{0x0a, 0x05, 'h', 'e', 'l', 'l', 'o', 0x12, 0x03, 0x00, 0xff, 0x80}
	for _, code := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			type observation struct {
				method, contentType string
				length              int64
				data                []byte
			}
			observed := make(chan observation, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				observed <- observation{r.Method, r.Header.Get("Content-Type"), r.ContentLength, data}
				if r.URL.Path == "/start" {
					w.Header().Set("Location", "/final")
					w.WriteHeader(code)
					return
				}
				w.Header().Set("Content-Type", "application/x-protobuf")
				_, _ = w.Write(data)
			}))
			defer server.Close()
			var logs bytes.Buffer
			client := NewClient().SetDebugOutput(&logs)
			defer client.CloseIdleConnections()
			raw := body.NewRawData()
			raw.SetData(payload, body.Protobuf)
			resp, got, err := NewRequest(client).SetURL(server.URL + "/start").SetMethod("post").SetBody(raw).SetDebug(true).Send().EndByte()
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("binary round trip=%x want=%x error=%v", got, payload, err)
			}
			if resp.Request.URL.Path != "/final" || len(observed) != 2 {
				t.Fatal("request body was not replayed through the redirect")
			}
			for range 2 {
				r := <-observed
				if r.method != http.MethodPost || r.contentType != "application/x-protobuf" ||
					r.length != int64(len(payload)) || !bytes.Equal(r.data, payload) {
					t.Fatalf("received request changed binary bytes or metadata: %+v", r)
				}
			}
		})
	}
}

func TestUnsupportedRawBodyStopsSending(t *testing.T) {
	client := NewClient()
	calls := 0
	client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
	})
	raw := body.NewRawData()
	raw.SetData(map[string]string{"key": "value"}, body.JSON)
	req := NewRequest(client).SetURL("http://example.test/").SetBody(raw).Send()
	_, _, err := req.End()
	assertError(t, err, ErrRequestBody, raw.Err())
	if !errors.Is(err, body.ErrRequestBody) || calls != 0 {
		t.Fatalf("invalid raw body was sent or lost its category: calls=%d error=%v", calls, err)
	}
	raw.SetData([]byte{0, 0xff}, body.Protobuf)
	_, _, err = NewRequest(client).SetURL("http://example.test/").SetBody(raw).Send().End()
	if err != nil || calls != 1 {
		t.Fatalf("corrected raw body did not send: calls=%d error=%v", calls, err)
	}
}
