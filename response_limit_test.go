package httpc

import (
	"bytes"
	"compress/gzip"
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
