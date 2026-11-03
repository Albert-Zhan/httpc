package httpc

import (
	"bytes"
	"compress/gzip"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResponseErrorsKeepTheirCauses(t *testing.T) {
	readErr := errors.New("response stream failed")
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	_, _ = w.Write([]byte("response content"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), compressed.Bytes()...)
	corrupt[len(corrupt)-8] ^= 0xff
	for _, tc := range []struct {
		name, encoding string
		reader         io.Reader
		kind, cause    error
	}{
		{"plain read", "", failingReader{readErr}, ErrReadResponse, readErr},
		{"gzip header", "gzip", strings.NewReader("invalid gzip header"), ErrDecodeResponse, gzip.ErrHeader},
		{"gzip source read", "gzip", failingReader{readErr}, ErrDecodeResponse, readErr},
		{"gzip checksum", "gzip", bytes.NewReader(corrupt), ErrDecodeResponse, gzip.ErrChecksum},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responseBody := &trackedReadCloser{Reader: tc.reader}
			client := NewClient()
			client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Encoding": {tc.encoding}}, Body: responseBody}, nil
			})
			req := NewRequest(client).SetURL("http://example.test/").Send()
			_, _, err := req.EndByte()
			assertError(t, err, tc.kind, tc.cause)
			if req.GetError() != err || responseBody.closes != 1 {
				t.Fatal("read failure was not retained or response body was not closed")
			}
		})
	}
}

func TestAutomaticGzipDecodeError(t *testing.T) {
	var data bytes.Buffer
	w := gzip.NewWriter(&data)
	_, _ = w.Write([]byte("response content"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), data.Bytes()...)
	corrupt[len(corrupt)-8] ^= 0xff
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(corrupt)
	}))
	defer server.Close()
	client := NewClient()
	t.Cleanup(client.CloseIdleConnections)
	resp, _, err := NewRequest(client).SetURL(server.URL).Send().EndByte()
	assertError(t, err, ErrDecodeResponse, gzip.ErrChecksum)
	if resp == nil || !resp.Uncompressed {
		t.Fatal("test did not exercise standard transport gzip decompression")
	}
}

func TestResponseDecodingErrorsAndEncodingBoundaries(t *testing.T) {
	for _, method := range []string{"EndByte", "EndTo", "EndFile"} {
		for _, encoding := range []string{"gzip", " GZIP "} {
			t.Run(method+"/"+encoding, func(t *testing.T) {
				req, source := responseRequest(t, strings.NewReader("invalid gzip header"), encoding)
				var err error
				switch method {
				case "EndByte":
					_, _, err = req.EndByte()
				case "EndTo":
					_, err = req.EndTo(io.Discard)
				case "EndFile":
					_, err = req.EndFile(t.TempDir(), "data")
				}
				assertError(t, err, ErrDecodeResponse, gzip.ErrHeader)
				if source.closes != 1 {
					t.Fatal("decode failure did not close response")
				}
			})
		}
	}
	for _, encoding := range []string{"", "notgzip", "br, gzip"} {
		t.Run("opaque/"+encoding, func(t *testing.T) {
			req, _ := responseRequest(t, strings.NewReader("opaque content"), encoding)
			req.response.Header.Set("Content-Type", "application/gzip")
			var dst bytes.Buffer
			_, err := req.EndTo(&dst)
			if err != nil || dst.String() != "opaque content" {
				t.Fatal("unsupported encoding or file MIME was decoded as gzip")
			}
		})
	}
}

func TestEmptyGzipResponses(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", h2), func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				switch r.URL.Path {
				case "/unknown":
					w.WriteHeader(http.StatusBadRequest)
					w.(http.Flusher).Flush()
				case "/head":
					w.Header().Set("Content-Length", "100")
					w.WriteHeader(http.StatusOK)
				case "/204":
					w.WriteHeader(http.StatusNoContent)
				case "/304":
					w.WriteHeader(http.StatusNotModified)
				case "/missing":
					w.Header().Set("Content-Length", "20")
					w.WriteHeader(http.StatusOK)
				default:
					w.Header().Set("Content-Length", "0")
					status := http.StatusOK
					if r.URL.Path == "/400" {
						status = http.StatusBadRequest
					}
					w.WriteHeader(status)
				}
			}))
			server.EnableHTTP2 = h2
			if h2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			client := NewClient()
			t.Cleanup(client.CloseIdleConnections)
			if h2 {
				pool := x509.NewCertPool()
				pool.AddCert(server.Certificate())
				client.CustomizeTransport(func(tr *http.Transport) { tr.TLSClientConfig.RootCAs = pool })
			}
			for _, tc := range []struct {
				path, method string
				status       int
				truncated    bool
			}{
				{"/200", "GET", 200, false},
				{"/400", "GET", 400, false},
				{"/unknown", "GET", 400, false},
				{"/head", "HEAD", 200, false},
				{"/204", "GET", 204, false},
				{"/304", "GET", 304, false},
				{"/missing", "GET", 200, true},
			} {
				for _, explicit := range []bool{false, true} {
					for _, method := range []string{"End", "EndByte", "EndTo", "EndFile"} {
						t.Run(fmt.Sprintf("%s%s/explicit=%v", method, tc.path, explicit), func(t *testing.T) {
							req := NewRequest(client).SetURL(server.URL + tc.path).SetMethod(tc.method).SetResponseLimit(1)
							if explicit {
								req.SetHeader("Accept-Encoding", "gzip")
							}
							req.Send()
							var resp *http.Response
							var err error
							var content []byte
							dst := &responseTestWriter{}
							switch method {
							case "End":
								var text string
								resp, text, err = req.End()
								content = []byte(text)
							case "EndByte":
								resp, content, err = req.EndByte()
							case "EndTo":
								resp, err = req.EndTo(dst)
								content = dst.data.Bytes()
								if dst.closes != 0 {
									t.Fatal("writer was closed")
								}
							case "EndFile":
								dir := t.TempDir()
								resp, err = req.EndFile(dir, "data")
								var fileErr error
								content, fileErr = os.ReadFile(filepath.Join(dir, "data"))
								if tc.status == 200 && fileErr != nil {
									t.Fatal(fileErr)
								}
								if tc.status != 200 && !os.IsNotExist(fileErr) {
									t.Fatal("non-200 response created a file")
								}
							}
							switch {
							case method == "EndFile" && tc.status != 200:
								assertError(t, err, ErrHTTPStatus, nil)
								if errors.Is(err, ErrDecodeResponse) {
									t.Fatal("empty error response was decoded before checking download status")
								}
							case tc.truncated:
								assertError(t, err, ErrDecodeResponse, io.ErrUnexpectedEOF)
							case err != nil:
								t.Fatal(err)
							}
							if resp == nil || resp.StatusCode != tc.status || len(content) != 0 || req.GetError() != err {
								t.Fatalf("response=%v error=%v content=%q", resp, err, content)
							}
							wantProto := 1
							if h2 {
								wantProto = 2
							}
							if resp.ProtoMajor != wantProto {
								t.Fatalf("protocol=%s", resp.Proto)
							}
						})
					}
				}
			}
		})
	}
}

func TestGzipEmptyBodyAndTruncationRemainDistinct(t *testing.T) {
	t.Run("empty source closes once", func(t *testing.T) {
		req, source := responseRequest(t, strings.NewReader(""), "gzip")
		req.response.StatusCode = http.StatusBadRequest
		req.SetResponseLimit(1)
		dst := &responseTestWriter{err: errors.New("must not write")}
		resp, err := req.EndTo(dst)
		if err != nil || resp.StatusCode != 400 || source.closes != 1 || dst.data.Len() != 0 || dst.closes != 0 {
			t.Fatalf("empty response: error=%v closes=%d output=%q", err, source.closes, dst.data.String())
		}
		reads := source.reads
		_, _, err = req.EndByte()
		assertError(t, err, ErrInvalidRequest, nil)
		if source.closes != 1 || source.reads != reads {
			t.Fatal("empty response was read or closed again")
		}
	})
	emptyMember := compressedResponse(t, "")
	for cut := 1; cut < len(emptyMember); cut++ {
		t.Run(fmt.Sprintf("truncated/%d", cut), func(t *testing.T) {
			req, source := responseRequest(t, bytes.NewReader(emptyMember[:cut]), "gzip")
			// Actual bytes take precedence over a custom Transport's incomplete metadata.
			req.response.ContentLength = 0
			_, _, err := req.EndByte()
			assertError(t, err, ErrDecodeResponse, io.ErrUnexpectedEOF)
			if source.closes != 1 {
				t.Fatal("truncated body was not closed")
			}
		})
	}
	t.Run("wrapped EOF is a source failure", func(t *testing.T) {
		cause := fmt.Errorf("source failed: %w", io.EOF)
		req, source := responseRequest(t, failingReader{cause}, "gzip")
		_, _, err := req.EndByte()
		assertError(t, err, ErrDecodeResponse, cause)
		if source.closes != 1 {
			t.Fatal("failed source was not closed")
		}
	})
}
