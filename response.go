package httpc

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (r *Request) beginRead(op string) error {
	if r.err != nil {
		return r.err
	}
	if r.response == nil {
		r.err = wrapError(ErrInvalidRequest, op, fmt.Errorf("request has not been sent"))
		return r.err
	}
	if r.responseRead {
		r.err = wrapError(ErrInvalidRequest, op, fmt.Errorf("response has already been read"))
		return r.err
	}
	r.responseRead = true
	return nil
}

// End 读取已发送请求的响应正文并关闭响应体，返回字符串。
// 4xx 和 5xx 状态码不会自动视为错误。
func (r *Request) End() (*http.Response, string, error) {
	resp, bodyByte, err := r.EndByte()
	if err != nil {
		return resp, "", err
	}
	return resp, string(bodyByte), nil
}

// EndByte 读取已发送请求的响应正文并关闭响应体，返回字节并支持 gzip 解码。
// 4xx 和 5xx 状态码不会自动视为错误。
func (r *Request) EndByte() (*http.Response, []byte, error) {
	if err := r.beginRead("read response"); err != nil {
		return nil, nil, err
	}
	defer r.response.Body.Close()

	var buffer bytes.Buffer
	if _, err := r.copyResponse(&buffer); err != nil {
		r.err = err
		return r.response, nil, err
	}
	return r.response, buffer.Bytes(), nil
}

// EndTo 将响应正文流式写入 dst 并关闭响应体，支持 gzip 解码。
// dst 由调用方管理，此方法不关闭它；失败时可能已写入部分内容。
// 4xx 和 5xx 状态码不会自动视为错误。此方法与其他 End 方法共享一次读取状态。
func (r *Request) EndTo(dst io.Writer) (*http.Response, error) {
	if err := r.beginRead("write response"); err != nil {
		return nil, err
	}
	defer r.response.Body.Close()
	if dst == nil {
		r.err = wrapError(ErrInvalidRequest, "write response", fmt.Errorf("destination writer is nil"))
		return r.response, r.err
	}
	_, r.err = r.copyResponse(dst)
	return r.response, r.err
}

// EndFile 将已发送请求的响应保存为文件并关闭响应体，支持 gzip 解码，仅接受 200 OK。
// savePath 目录须已存在，已有文件会被覆盖；saveFileName 为空时从 URL 获取文件名。
func (r *Request) EndFile(savePath, saveFileName string) (resp *http.Response, err error) {
	if err := r.beginRead("save response file"); err != nil {
		return nil, err
	}

	defer func() {
		_ = r.response.Body.Close()
	}()

	if r.response.StatusCode != http.StatusOK {
		r.err = wrapError(ErrHTTPStatus, "save response file", fmt.Errorf("server responded with HTTP %d", r.response.StatusCode))
		return r.response, r.err
	}

	if saveFileName == "" {
		u, err := url.Parse(r.request.URL.String())
		if err == nil {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) > 0 {
				saveFileName = parts[len(parts)-1]
			}
		}
		if saveFileName == "" {
			saveFileName = "download.tmp"
		}
	}

	destPath := filepath.Join(savePath, saveFileName)
	file, err := os.Create(destPath)
	if err != nil {
		r.err = wrapError(ErrSaveFile, "create download file", err)
		return r.response, r.err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			r.err = wrapError(ErrSaveFile, "close download file", closeErr)
			err = r.err
		}
	}()
	_, err = r.copyResponse(file)
	if err != nil {
		r.err = wrapError(ErrSaveFile, "write download file", err)
		return r.response, r.err
	}

	return r.response, nil
}

// copyResponse 流式复制解码后的正文，调用方负责响应体和目标的关闭。
func (r *Request) copyResponse(dst io.Writer) (int64, error) {
	var source io.Reader = r.response.Body
	kind := ErrReadResponse
	if r.response.Uncompressed {
		kind = ErrDecodeResponse
	} else if strings.EqualFold(strings.TrimSpace(r.response.Header.Get("Content-Encoding")), "gzip") {
		reader, err := gzip.NewReader(source)
		// 初始化时正常 EOF 表示没有任何 gzip 数据；截断数据会返回其他错误。
		if err == io.EOF {
			return 0, nil
		}
		if err != nil {
			return 0, wrapError(ErrDecodeResponse, "decode gzip header", err)
		}
		defer reader.Close()
		source = reader
		kind = ErrDecodeResponse
	}
	source = responseErrorReader{Reader: source, kind: kind}
	writer := responseErrorWriter{Writer: dst}
	if r.responseLimit <= 0 {
		return io.Copy(writer, source)
	}

	limited := &io.LimitedReader{R: source, N: r.responseLimit}
	n, err := io.Copy(writer, limited)
	if err != nil || limited.N != 0 {
		return n, err
	}
	// 额外探测一个解码后字节，不写入目标，也不对上限做可能溢出的加法。
	var probe [1]byte
	extra, err := io.ReadFull(source, probe[:])
	if extra != 0 {
		return n, wrapError(ErrResponseTooLarge, "limit response body",
			fmt.Errorf("response exceeds %d bytes", r.responseLimit))
	}
	if err == io.EOF {
		err = nil
	}
	return n, err
}

type responseErrorReader struct {
	io.Reader
	kind error
}

func (r responseErrorReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && err != io.EOF {
		err = wrapError(r.kind, "read response body", err)
	}
	return n, err
}

type responseErrorWriter struct{ io.Writer }

func (w responseErrorWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		err = wrapError(ErrWriteResponse, "write response body", err)
	}
	return n, err
}
