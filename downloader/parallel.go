package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

type part struct{ start, end int64 }

func (d *Downloader) parallel(ctx context.Context, rawURL string, f *os.File, probe *probeResult, p *progress) (int, error) {
	if err := f.Truncate(probe.total); err != nil {
		return 0, err
	}
	workers := d.opts.Workers
	chunks := 1 + (probe.total-1)/d.opts.ChunkSize
	if int64(workers) > chunks {
		workers = int(chunks)
	}
	p.reset(probe.total)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan part)
	failures := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if workerCtx.Err() != nil {
					return
				}
				p.active(1)
				err := d.chunk(workerCtx, rawURL, f, job, probe, p)
				if err == nil {
					p.complete(job.start)
				}
				p.active(-1)
				if err != nil {
					failures <- err
					cancel()
					return
				}
			}
		}()
	}
	for start := int64(0); start < probe.total; {
		length := min(d.opts.ChunkSize, probe.total-start)
		select {
		case jobs <- part{start, start + length - 1}:
			start += length
		case <-workerCtx.Done():
			start = probe.total
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return workers, err
	}
	close(failures)
	var firstErr error
	restart := false
	for err := range failures {
		if firstErr == nil {
			firstErr = err
		}
		if errors.Is(err, errRestartSingle) {
			restart = true
			continue
		}
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			return workers, err
		}
	}
	if restart {
		return workers, errRestartSingle
	}
	return workers, firstErr
}

func (d *Downloader) chunk(ctx context.Context, rawURL string, f *os.File, job part, probe *probeResult, p *progress) error {
	for attempt := 0; ; attempt++ {
		err := d.chunkOnce(ctx, rawURL, f, job, probe, p)
		if err == nil || attempt >= d.opts.Retries || !retryable(err) {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (d *Downloader) chunkOnce(ctx context.Context, rawURL string, f *os.File, job part, probe *probeResult, p *progress) error {
	resp, err := d.get(ctx, rawURL, fmt.Sprintf("bytes=%d-%d", job.start, job.end), probe.etag)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		return errRestartSingle
	}
	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("%w: chunk returned %s", ErrHTTPStatus, resp.Status)
	}
	if !identity(resp) {
		return errRestartSingle
	}
	if probe.finalURL != "" && (resp.Request == nil || resp.Request.URL == nil || resp.Request.URL.String() != probe.finalURL) {
		return errRestartSingle
	}
	start, end, total, err := contentRange(resp.Header.Get("Content-Range"))
	if err != nil {
		return err
	}
	if start != job.start || end != job.end || total != probe.total {
		return fmt.Errorf("%w: chunk Content-Range does not match requested range", ErrInvalidResponse)
	}
	if etag := resp.Header.Get("ETag"); etag != "" && etag != probe.etag {
		return errRestartSingle
	}
	w := &progressWriter{dst: io.NewOffsetWriter(f, start), p: p, key: start}
	return copyExact(w, contextReader{ctx, resp.Body}, end-start+1)
}

func retryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var network net.Error
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &network)
}
