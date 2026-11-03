package httpc

import (
	"bytes"
	"compress/gzip"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEndFileClosesResponseBody(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		missingDir bool
	}{
		{name: "not found", status: http.StatusNotFound},
		{name: "server error", status: http.StatusInternalServerError},
		{name: "success", status: http.StatusOK},
		{name: "file creation failure", status: http.StatusOK, missingDir: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedReadCloser{Reader: strings.NewReader("download content")}
			client := NewClient()
			client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: body, Request: r}, nil
			})
			dir := t.TempDir()
			if tc.missingDir {
				dir = filepath.Join(dir, "missing")
			}
			resp, err := NewRequest(client).SetURL("http://example.test/file").Send().EndFile(dir, "file.txt")
			wantError := tc.status != http.StatusOK || tc.missingDir
			if (err != nil) != wantError || resp == nil || resp.StatusCode != tc.status {
				t.Fatalf("response = %v, error = %v, wantError = %v", resp, err, wantError)
			}
			if body.closes != 1 {
				t.Error("response body was not closed")
			}
			content, fileErr := os.ReadFile(filepath.Join(dir, "file.txt"))
			if wantError {
				if !os.IsNotExist(fileErr) {
					t.Fatalf("unexpected download file: %v", fileErr)
				}
			} else if fileErr != nil || string(content) != "download content" {
				t.Fatalf("content = %q, error = %v", content, fileErr)
			}
		})
	}
}

func TestResponseCanOnlyBeReadOnce(t *testing.T) {
	for _, first := range []string{"End", "EndByte", "EndTo", "EndFile"} {
		for _, second := range []string{"End", "EndByte", "EndTo", "EndFile"} {
			t.Run(first+"/"+second, func(t *testing.T) {
				responseBody := &trackedReadCloser{Reader: strings.NewReader("content")}
				client := NewClient()
				calls := 0
				client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: responseBody}, nil
				})
				req := NewRequest(client).SetURL("http://example.test/").Send()
				dir := t.TempDir()
				read := func(method, name string) error {
					switch method {
					case "End":
						_, _, err := req.End()
						return err
					case "EndTo":
						_, err := req.EndTo(io.Discard)
						return err
					case "EndByte":
						_, _, err := req.EndByte()
						return err
					default:
						_, err := req.EndFile(dir, name)
						return err
					}
				}
				if err := read(first, "first.txt"); err != nil {
					t.Fatal(err)
				}
				if responseBody.closes != 1 || responseBody.reads == 0 {
					t.Fatal("first read did not consume and close the body")
				}
				reads := responseBody.reads
				err := read(second, "second.txt")
				assertError(t, err, ErrInvalidRequest, nil)
				if req.GetError() != err || responseBody.reads != reads || responseBody.closes != 1 {
					t.Fatal("repeated read touched the response or lost its error")
				}
				if _, err := os.Stat(filepath.Join(dir, "second.txt")); !os.IsNotExist(err) {
					t.Fatal("repeated read created a download file")
				}
				req.Send()
				if calls != 1 || responseBody.closes != 1 {
					t.Fatal("Send after reading re-sent or re-closed the response")
				}
			})
		}
	}
}

func TestRepeatedReadPreservesOriginalFailure(t *testing.T) {
	cause := errors.New("response interrupted")
	responseBody := &trackedReadCloser{Reader: failingReader{cause}}
	client := NewClient()
	client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: responseBody}, nil
	})
	req := NewRequest(client).SetURL("http://example.test/").Send()
	_, _, first := req.EndByte()
	assertError(t, first, ErrReadResponse, cause)
	reads := responseBody.reads
	_, second := req.EndFile(t.TempDir(), "ignored.txt")
	if second != first || responseBody.reads != reads || responseBody.closes != 1 {
		t.Fatal("repeated read lost the original failure or touched the body")
	}
}

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

func TestReadBeforeSendReturnsError(t *testing.T) {
	_, _, err := NewRequest(NewClient()).End()
	assertError(t, err, ErrInvalidRequest, nil)
	_, err = NewRequest(NewClient()).EndFile(t.TempDir(), "file")
	assertError(t, err, ErrInvalidRequest, nil)
}

func TestDownloadErrorsAreClassified(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		reader io.Reader
		dir    string
		kind   error
		cause  error
	}{
		{"HTTP status", http.StatusNotFound, strings.NewReader("not found"), t.TempDir(), ErrHTTPStatus, nil},
		{"file creation", http.StatusOK, strings.NewReader("content"), filepath.Join(t.TempDir(), "missing"), ErrSaveFile, os.ErrNotExist},
		{"file copy", http.StatusOK, failingReader{io.ErrUnexpectedEOF}, t.TempDir(), ErrSaveFile, io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responseBody := &trackedReadCloser{Reader: tc.reader}
			client := NewClient()
			client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: responseBody}, nil
			})
			req := NewRequest(client).SetURL("http://example.test/file").Send()
			_, err := req.EndFile(tc.dir, "file")
			assertError(t, err, tc.kind, tc.cause)
			if req.GetError() != err || responseBody.closes != 1 {
				t.Fatal("download error was not retained or response body was not closed")
			}
		})
	}
}

func TestEndDoesNotTreatHTTPStatusAsError(t *testing.T) {
	client := NewClient()
	client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("not found"))}, nil
	})
	resp, text, err := NewRequest(client).SetURL("http://example.test/").Send().End()
	if err != nil || resp.StatusCode != http.StatusNotFound || text != "not found" {
		t.Fatalf("response = %v, body = %q, error = %v", resp, text, err)
	}
}

type responseTestWriter struct {
	data   bytes.Buffer
	err    error
	short  bool
	closes int
}

func (w *responseTestWriter) Write(p []byte) (int, error) {
	if w.err != nil || w.short {
		n := min(3, len(p))
		_, _ = w.data.Write(p[:n])
		return n, w.err
	}
	return w.data.Write(p)
}

func (w *responseTestWriter) Close() error { w.closes++; return nil }

func responseRequest(t *testing.T, reader io.Reader, encoding string) (*Request, *trackedReadCloser) {
	t.Helper()
	source := &trackedReadCloser{Reader: reader}
	client := NewClient()
	client.client.Transport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Encoding": {encoding}},
			Body: source, Request: req, ContentLength: -1}, nil
	})
	return NewRequest(client).SetURL("http://example.test/content").Send(), source
}

func compressedResponse(t *testing.T, content string) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := gzip.NewWriter(&data)
	if _, err := io.WriteString(writer, content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestEndToStreamsWithoutClosingWriter(t *testing.T) {
	req, source := responseRequest(t, strings.NewReader("response content"), "")
	req.response.StatusCode = http.StatusNotFound
	dst := &responseTestWriter{}
	resp, err := req.EndTo(dst)
	if err != nil || resp.StatusCode != http.StatusNotFound || dst.data.String() != "response content" ||
		source.closes != 1 || dst.closes != 0 {
		t.Fatalf("stream response=%v error=%v output=%q source closes=%d writer closes=%d",
			resp, err, dst.data.String(), source.closes, dst.closes)
	}
}

func TestEndToErrorsPreserveCausesAndStopReading(t *testing.T) {
	cause := errors.New("writer unavailable")
	for _, tc := range []struct {
		name  string
		err   error
		short bool
		want  error
	}{
		{"writer error", cause, false, cause},
		{"short write", nil, true, io.ErrShortWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := strings.NewReader(strings.Repeat("x", 1<<20))
			req, source := responseRequest(t, content, "")
			dst := &responseTestWriter{err: tc.err, short: tc.short}
			resp, err := req.EndTo(dst)
			assertError(t, err, ErrWriteResponse, tc.want)
			if resp == nil || content.Len() == 0 || source.closes != 1 || dst.closes != 0 ||
				dst.data.String() != "xxx" || req.GetError() != err {
				t.Fatal("writer failure buffered the full response, lost partial output or changed ownership")
			}
			reads := source.reads
			_, _, repeated := req.EndByte()
			if repeated != err || source.reads != reads || source.closes != 1 {
				t.Fatal("repeated read lost the original writer failure or accessed the source")
			}
		})
	}
	req, source := responseRequest(t, failingReader{io.ErrUnexpectedEOF}, "")
	_, err := req.EndTo(io.Discard)
	assertError(t, err, ErrReadResponse, io.ErrUnexpectedEOF)
	if errors.Is(err, ErrWriteResponse) || source.closes != 1 {
		t.Fatal("source failure was misclassified or body was not closed")
	}
}

func TestEndToRejectsInvalidCalls(t *testing.T) {
	req := NewRequest(NewClient())
	_, err := req.EndTo(io.Discard)
	assertError(t, err, ErrInvalidRequest, nil)

	req, source := responseRequest(t, strings.NewReader("content"), "")
	_, err = req.EndTo(nil)
	assertError(t, err, ErrInvalidRequest, nil)
	if source.closes != 1 || source.reads != 0 {
		t.Fatal("nil writer did not close the unread response")
	}
}

func TestResponseLimitAcrossReadMethods(t *testing.T) {
	for _, method := range []string{"End", "EndByte", "EndTo", "EndFile"} {
		for _, encoding := range []string{"plain", "gzip"} {
			for _, tc := range []struct {
				name, content string
				limit         int64
				exceeds       bool
			}{
				{"default", "abcdef", 0, false},
				{"negative disables", "abcdef", -1, false},
				{"under", "abcdef", 7, false},
				{"exact", "abcdef", 6, false},
				{"over", "abcdef", 5, true},
				{"empty", "", 1, false},
				{"largest limit", "abcdef", math.MaxInt64, false},
			} {
				t.Run(method+"/"+encoding+"/"+tc.name, func(t *testing.T) {
					var reader io.Reader = strings.NewReader(tc.content)
					header := ""
					if encoding == "gzip" {
						reader = bytes.NewReader(compressedResponse(t, tc.content))
						header = "gzip"
					}
					req, source := responseRequest(t, reader, header)
					// A limit is enforced by actual reads, independent of length metadata.
					req.response.ContentLength = 1
					if req.SetResponseLimit(tc.limit) != req {
						t.Fatal("setter changed request")
					}
					var dst bytes.Buffer
					var resp *http.Response
					var got []byte
					var err error
					switch method {
					case "End":
						var text string
						resp, text, err = req.End()
						got = []byte(text)
					case "EndByte":
						resp, got, err = req.EndByte()
					case "EndTo":
						resp, err = req.EndTo(&dst)
						got = dst.Bytes()
					case "EndFile":
						dir := t.TempDir()
						resp, err = req.EndFile(dir, "data")
						var fileErr error
						got, fileErr = os.ReadFile(filepath.Join(dir, "data"))
						if fileErr != nil {
							t.Fatal(fileErr)
						}
					}
					if tc.exceeds {
						assertError(t, err, ErrResponseTooLarge, nil)
						if method == "EndFile" && !errors.Is(err, ErrSaveFile) {
							t.Fatal("download lost save-file classification")
						}
						if method == "End" || method == "EndByte" {
							if len(got) != 0 {
								t.Fatal("buffered methods returned partial success data")
							}
						} else if string(got) != tc.content[:tc.limit] {
							t.Fatalf("stream output=%q, want limited prefix", got)
						}
						reads := source.reads
						_, repeated := req.EndTo(io.Discard)
						if repeated != err || source.reads != reads {
							t.Fatal("limit error was not retained")
						}
					} else if err != nil || string(got) != tc.content {
						t.Fatalf("output=%q error=%v want=%q", got, err, tc.content)
					}
					if resp == nil || source.closes != 1 || req.GetError() != err {
						t.Fatalf("response ownership or error retention: response=%v closes=%d error=%v", resp, source.closes, err)
					}
				})
			}
		}
	}
}

func TestResponseLimitStopsAtOneProbeByte(t *testing.T) {
	content := strings.NewReader(strings.Repeat("x", 1<<20))
	req, source := responseRequest(t, content, "")
	req.SetResponseLimit(10)
	var dst bytes.Buffer
	_, err := req.EndTo(&dst)
	assertError(t, err, ErrResponseTooLarge, nil)
	if dst.Len() != 10 || content.Len() != (1<<20)-11 || source.closes != 1 {
		t.Fatal("limit read or wrote too much data")
	}

	req, _ = responseRequest(t, strings.NewReader("abcdef"), "")
	req.SetResponseLimit(1).SetResponseLimit(0)
	_, text, err := req.End()
	if err != nil || text != "abcdef" {
		t.Fatal("zero did not clear an earlier limit")
	}
}

func TestGzipResponseStreamingAndLimit(t *testing.T) {
	content := strings.Repeat("compressible response", 1024)
	compressed := compressedResponse(t, content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(compressed)
	}))
	defer server.Close()
	client := NewClient()
	t.Cleanup(client.CloseIdleConnections)
	for _, method := range []string{"EndByte", "EndTo", "EndFile"} {
		for _, explicit := range []bool{false, true} {
			for _, limited := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/explicit=%v/limited=%v", method, explicit, limited), func(t *testing.T) {
					req := NewRequest(client).SetURL(server.URL)
					if explicit {
						req.SetHeader("Accept-Encoding", "gzip")
					}
					if limited {
						req.SetResponseLimit(100)
					}
					req.Send()
					var dst bytes.Buffer
					var resp *http.Response
					var err error
					switch method {
					case "EndByte":
						var data []byte
						resp, data, err = req.EndByte()
						dst.Write(data)
					case "EndTo":
						resp, err = req.EndTo(&dst)
					case "EndFile":
						dir := t.TempDir()
						resp, err = req.EndFile(dir, "data")
						data, fileErr := os.ReadFile(filepath.Join(dir, "data"))
						if fileErr != nil {
							t.Fatal(fileErr)
						}
						dst.Write(data)
					}
					if limited {
						assertError(t, err, ErrResponseTooLarge, nil)
						if method != "EndByte" && dst.String() != content[:100] {
							t.Fatal("wrong limited decoded output")
						}
					} else if err != nil || dst.String() != content {
						t.Fatalf("gzip output length=%d error=%v", dst.Len(), err)
					}
					if resp == nil || resp.Uncompressed == explicit {
						t.Fatal("wrong gzip transport path")
					}
				})
			}
		}
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

func TestResponseLimitStillValidatesGzipChecksum(t *testing.T) {
	content := "response content"
	corrupt := compressedResponse(t, content)
	corrupt[len(corrupt)-8] ^= 0xff
	for _, limit := range []int64{0, int64(len(content))} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			req, source := responseRequest(t, bytes.NewReader(corrupt), "gzip")
			req.SetResponseLimit(limit)
			var dst bytes.Buffer
			_, err := req.EndTo(&dst)
			assertError(t, err, ErrDecodeResponse, gzip.ErrChecksum)
			if source.closes != 1 || dst.String() != content {
				t.Fatal("checksum failure lost partial output or leaked the response")
			}
		})
	}
}

func TestResponseLimitPreservesFailureAtBoundary(t *testing.T) {
	reader := io.MultiReader(strings.NewReader("abc"), failingReader{io.ErrUnexpectedEOF})
	req, source := responseRequest(t, reader, "")
	req.SetResponseLimit(3)
	var dst bytes.Buffer
	_, err := req.EndTo(&dst)
	assertError(t, err, ErrReadResponse, io.ErrUnexpectedEOF)
	if errors.Is(err, ErrResponseTooLarge) || dst.String() != "abc" || source.closes != 1 {
		t.Fatal("a failure during the boundary probe was lost or mistaken for overflow")
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
