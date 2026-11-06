// Package downloader 提供单线程和分段并行的流式文件下载。
package downloader

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"

	"github.com/Albert-Zhan/httpc/v2"
)

var (
	ErrInvalidOptions    = errors.New("downloader: invalid options")
	ErrInvalidResponse   = errors.New("downloader: invalid response")
	ErrHTTPStatus        = httpc.ErrHTTPStatus
	ErrTooLarge          = httpc.ErrResponseTooLarge
	ErrDestinationExists = errors.New("downloader: destination exists")
	errRestartSingle     = errors.New("downloader: restart as a single stream")
)

// Doer 与 http.Client 和 httpc.Client 兼容。须支持并发和 context 取消。
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Options 配置下载行为；Range、If-Range 和 Accept-Encoding 由下载器管理。
type Options struct {
	Workers    int            // 0 使用 4 个；1 强制单线程并跳过探测；最大 128。
	ChunkSize  int64          // 0 使用 8 MiB；不决定内存缓冲区大小。
	MaxBytes   int64          // 0 不限制；限制最终文件的字节数。
	Retries    int            // 每个分段额外重试 0–10 次；仅重试网络错误或正文截断。
	Headers    http.Header    // 自定义头；New 时复制，包含值切片。
	Debug      bool           // 配合 httpc.Client，开启发送阶段日志。
	OnProgress func(Progress) // 每次下载串行回调；回调须及时返回。
}

// Progress 描述已写入的有效字节；分段重试不重复计数，降级重启会归零。
// Total 为 -1 表示未知；回调开始、每约 100ms 和结束时触发。
type Progress struct {
	Downloaded, Total int64
	ActiveWorkers     int
}

// Result 仅在文件完整落盘后返回。Mode 为 single 或 parallel。
type Result struct {
	Path        string
	Size        int64
	Mode        string
	WorkersUsed int
}

// Downloader 配置后可以复用，下载任务之间不共享文件或进度状态。
// 调用方管理客户端生命周期；下载器不会关闭客户端或连接池。
type Downloader struct {
	client Doer
	opts   Options
}

// New 校验配置并创建下载器。
func New(client Doer, opts Options) (*Downloader, error) {
	if client == nil || isNil(client) {
		return nil, fmt.Errorf("%w: client is nil", ErrInvalidOptions)
	}
	if opts.Workers < 0 || opts.ChunkSize < 0 || opts.MaxBytes < 0 || opts.Retries < 0 {
		return nil, fmt.Errorf("%w: numeric options must not be negative", ErrInvalidOptions)
	}
	if opts.Workers == 0 {
		opts.Workers = 4
	}
	if opts.ChunkSize == 0 {
		opts.ChunkSize = 8 << 20
	}
	if opts.Workers > 128 || opts.Retries > 10 {
		return nil, fmt.Errorf("%w: Workers must be <=128 and Retries <=10", ErrInvalidOptions)
	}
	opts.Headers = opts.Headers.Clone()
	for key := range opts.Headers {
		if http.CanonicalHeaderKey(key) == "Range" || http.CanonicalHeaderKey(key) == "If-Range" || http.CanonicalHeaderKey(key) == "Accept-Encoding" {
			return nil, fmt.Errorf("%w: %s is managed by downloader", ErrInvalidOptions, key)
		}
	}
	return &Downloader{client: client, opts: opts}, nil
}

func isNil(v any) bool {
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Map, reflect.Slice, reflect.Interface, reflect.Chan:
		return r.IsNil()
	}
	return false
}

// Download 下载到 dst。目录必须已存在，目标存在时不覆盖。
// 先写同目录的私有临时文件，再以硬链接提交，避免覆盖并发创建的目标。
// 文件系统须支持硬链接；错误或取消时清理临时文件，不留下半成品目标。
func (d *Downloader) Download(ctx context.Context, rawURL, dst string) (*Result, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidOptions)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dst == "" {
		return nil, fmt.Errorf("%w: destination is empty", ErrInvalidOptions)
	}
	if _, err := os.Lstat(dst); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrDestinationExists, dst)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if d.opts.Debug {
		ctx = httpc.WithDebug(ctx)
	}
	p := newProgress(d.opts.OnProgress)
	defer p.close()
	f, err := os.CreateTemp(filepath.Dir(dst), ".httpc-download-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	mode, workers := "single", 1
	var size int64
	if d.opts.Workers == 1 {
		size, err = d.single(ctx, rawURL, f, nil, p)
	} else {
		var probe *probeResult
		probe, err = d.probe(ctx, rawURL)
		if err == nil {
			switch {
			case probe.body != nil:
				size, err = d.single(ctx, rawURL, f, probe.body, p)
			case probe.total == 0:
				p.reset(0)
			case probe.parallel && probe.total > d.opts.ChunkSize:
				mode = "parallel"
				workers, err = d.parallel(ctx, rawURL, f, probe, p)
				size = probe.total
				if errors.Is(err, errRestartSingle) {
					// parallel 返回前已取消并等待全部 worker，才能截断或复用文件。
					mode, workers = "single", 1
					if err = f.Truncate(0); err == nil {
						_, err = f.Seek(0, 0)
					}
					if err == nil {
						size, err = d.single(ctx, rawURL, f, nil, p)
					}
				}
			default:
				size, err = d.single(ctx, rawURL, f, nil, p)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = f.Sync(); err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = os.Link(f.Name(), dst); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s", ErrDestinationExists, dst)
		}
		return nil, fmt.Errorf("commit download: %w", err)
	}
	return &Result{Path: dst, Size: size, Mode: mode, WorkersUsed: workers}, nil
}

func (d *Downloader) get(ctx context.Context, rawURL, byteRange, etag string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header = d.opts.Headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Accept-Encoding", "identity")
	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	if etag != "" {
		req.Header.Set("If-Range", etag)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("%w: missing response or body", ErrInvalidResponse)
	}
	return resp, nil
}
