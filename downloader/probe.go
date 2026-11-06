package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

type probeResult struct {
	body     *http.Response
	total    int64
	etag     string
	parallel bool
	finalURL string
}

func (d *Downloader) probe(ctx context.Context, rawURL string) (*probeResult, error) {
	resp, err := d.get(ctx, rawURL, "bytes=0-0", "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return &probeResult{body: resp, total: resp.ContentLength}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && resp.Header.Get("Content-Range") == "bytes */0" {
		return &probeResult{total: 0}, nil
	}
	if resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("%w: probe returned %s", ErrHTTPStatus, resp.Status)
	}
	start, end, total, err := contentRange(resp.Header.Get("Content-Range"))
	if err != nil {
		return nil, err
	}
	if start != 0 || end != 0 {
		return nil, fmt.Errorf("%w: probe range must be 0-0", ErrInvalidResponse)
	}
	if !identity(resp) {
		return &probeResult{total: -1}, nil
	}
	if err = copyExact(io.Discard, resp.Body, 1); err != nil {
		return nil, err
	}
	if total > 0 && d.opts.MaxBytes > 0 && total > d.opts.MaxBytes {
		return nil, ErrTooLarge
	}
	etag := resp.Header.Get("ETag")
	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return &probeResult{total: total, etag: etag, parallel: total > 0 && strongETag(etag), finalURL: finalURL}, nil
}
