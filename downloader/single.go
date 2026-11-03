package downloader

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func (d *Downloader) single(ctx context.Context, rawURL string, f *os.File, resp *http.Response, p *progress) (int64, error) {
	var err error
	if resp == nil {
		resp, err = d.get(ctx, rawURL, "", "")
		if err != nil {
			return 0, err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%w: download returned %s", ErrHTTPStatus, resp.Status)
	}
	var src io.Reader = resp.Body
	total := resp.ContentLength
	if !identity(resp) {
		total = -1
		if !resp.Uncompressed {
			if !strings.EqualFold(strings.TrimSpace(resp.Header.Get("Content-Encoding")), "gzip") {
				return 0, fmt.Errorf("%w: unsupported Content-Encoding", ErrInvalidResponse)
			}
			gz, gzipErr := gzip.NewReader(resp.Body)
			if gzipErr == io.EOF {
				p.reset(0)
				return 0, nil
			}
			if gzipErr != nil {
				return 0, gzipErr
			}
			defer gz.Close()
			src = gz
		}
	}
	if total > 0 && d.opts.MaxBytes > 0 && total > d.opts.MaxBytes {
		return 0, ErrTooLarge
	}
	p.reset(total)
	p.active(1)
	defer p.active(-1)
	w := &progressWriter{dst: f, p: p}
	src = contextReader{ctx, src}
	if total >= 0 {
		err = copyExact(w, src, total)
	} else if d.opts.MaxBytes > 0 {
		_, err = io.CopyBuffer(w, io.LimitReader(src, d.opts.MaxBytes), make([]byte, 32<<10))
		if err == nil {
			var b [1]byte
			var n int
			n, err = io.ReadFull(src, b[:])
			if n > 0 {
				err = ErrTooLarge
			} else if err == io.EOF {
				err = nil
			}
		}
	} else {
		_, err = io.CopyBuffer(w, src, make([]byte, 32<<10))
	}
	return w.n, err
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.src.Read(b)
}

type progressWriter struct {
	dst io.Writer
	p   *progress
	key int64
	n   int64
}

func (w *progressWriter) Write(b []byte) (int, error) {
	n, err := w.dst.Write(b)
	w.n += int64(n)
	w.p.set(w.key, w.n)
	return n, err
}
