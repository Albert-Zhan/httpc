package body

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// StreamForm 按需读取本地普通文件，构建可重放的 multipart 正文，不支持并发修改。
// 从 Encode 到发送及重定向结束，源文件必须保持不变。
type StreamForm struct {
	boundary string
	entries  []streamFormEntry
	plan     *streamFormPlan
	err      error
}

type streamFormEntry struct {
	name, value string
	file        bool
}

type streamFormPiece struct {
	prefix []byte
	path   string
	info   os.FileInfo
}

type streamFormPlan struct {
	pieces []streamFormPiece
	length int64
}

// NewStreamFormData 创建流式表单；文件在 Encode 时检查，在读取正文时打开。
func NewStreamFormData() *StreamForm {
	return &StreamForm{boundary: multipart.NewWriter(io.Discard).Boundary()}
}

// SetBoundary 设置 multipart 边界，应在添加字段或文件前调用。
func (f *StreamForm) SetBoundary(boundary string) *StreamForm {
	if !f.canWrite() {
		return f
	}
	writer := multipart.NewWriter(io.Discard)
	err := writer.SetBoundary(boundary)
	if len(f.entries) != 0 {
		err = fmt.Errorf("boundary must be set before adding fields or files")
	}
	if err != nil {
		f.err = fmt.Errorf("%w: set multipart boundary: %w", ErrRequestBody, err)
	} else {
		f.boundary = boundary
	}
	return f
}

// SetData 添加普通字段，允许同名字段重复。
func (f *StreamForm) SetData(name, value string) *StreamForm {
	if f.canWrite() {
		f.entries = append(f.entries, streamFormEntry{name: name, value: value})
	}
	return f
}

// SetFile 添加本地普通文件的路径，不读取文件内容；文件检查错误在 Encode 后通过 Err 获取。
func (f *StreamForm) SetFile(name, file string) *StreamForm {
	if f.canWrite() {
		f.entries = append(f.entries, streamFormEntry{name: name, value: file, file: true})
	}
	return f
}

func (f *StreamForm) canWrite() bool {
	if f.err == nil && f.plan != nil {
		f.err = fmt.Errorf("%w: modify multipart form: form is already encoded", ErrRequestBody)
	}
	return f.err == nil
}

// Err 返回第一个构建错误；发送期间的文件读取错误由正文 Reader 返回。
func (f *StreamForm) Err() error { return f.err }

// GetContentType 返回包含 boundary 的 multipart/form-data 类型。
func (f *StreamForm) GetContentType() string {
	return mime.FormatMediaType("multipart/form-data", map[string]string{"boundary": f.boundary})
}

// ContentLength 返回编码后的精确长度；未编码或构建失败时返回 -1。
func (f *StreamForm) ContentLength() int64 {
	if f.err != nil || f.plan == nil {
		return -1
	}
	return f.plan.length
}

// Encode 固定字段、文件元信息和边界，返回可关闭的流式 Reader，不读取文件内容。
// 此后不能修改表单；调用方直接使用 Encode 时负责关闭返回的 io.ReadCloser。
func (f *StreamForm) Encode() io.Reader {
	reader, err := f.GetBody()
	if err != nil {
		return errorReader{err: err}
	}
	return reader
}

// GetBody 创建独立的正文 Reader，用于重放；首次调用也会固定表单。
// 每个 Reader 由调用方关闭，文件内容不缓存，源文件须保持不变。
func (f *StreamForm) GetBody() (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.plan == nil {
		f.plan, f.err = f.prepare()
		if f.err != nil {
			return nil, f.err
		}
		f.entries = nil
	}
	reader := &streamFormReader{}
	var parts []io.Reader
	for _, piece := range f.plan.pieces {
		parts = append(parts, bytes.NewReader(piece.prefix))
		if piece.info != nil {
			file := &streamFileReader{path: piece.path, info: piece.info, remaining: piece.info.Size()}
			reader.files = append(reader.files, file)
			parts = append(parts, file)
		}
	}
	reader.reader = io.MultiReader(parts...)
	return reader, nil
}

func (f *StreamForm) prepare() (*streamFormPlan, error) {
	plan := &streamFormPlan{}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.SetBoundary(f.boundary); err != nil {
		return nil, fmt.Errorf("%w: set multipart boundary: %w", ErrRequestBody, err)
	}
	for _, entry := range f.entries {
		if !entry.file {
			if err := writer.WriteField(entry.name, entry.value); err != nil {
				return nil, fmt.Errorf("%w: write multipart field: %w", ErrRequestBody, err)
			}
			continue
		}
		path, err := filepath.Abs(entry.value)
		if err != nil {
			return nil, uploadFileError("resolve path", entry.value, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, uploadFileError("stat", entry.value, err)
		}
		if !info.Mode().IsRegular() {
			return nil, uploadFileError("stat", entry.value, fmt.Errorf("not a regular file"))
		}
		// Writer 只生成边界和字段头，文件内容由相邻的 Reader 输出。
		if _, err := writer.CreateFormFile(entry.name, filepath.Base(entry.value)); err != nil {
			return nil, uploadFileError("create multipart file field", entry.value, err)
		}
		if err := plan.appendPiece(buffer.Bytes(), path, info); err != nil {
			return nil, err
		}
		buffer.Reset()
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("%w: close multipart writer: %w", ErrRequestBody, err)
	}
	if err := plan.appendPiece(buffer.Bytes(), "", nil); err != nil {
		return nil, err
	}
	return plan, nil
}

func (p *streamFormPlan) appendPiece(prefix []byte, path string, info os.FileInfo) error {
	size := int64(len(prefix))
	if info != nil {
		if info.Size() < 0 || info.Size() > math.MaxInt64-size {
			return fmt.Errorf("%w: multipart length overflows int64", ErrRequestBody)
		}
		size += info.Size()
	}
	if p.length > math.MaxInt64-size {
		return fmt.Errorf("%w: multipart length overflows int64", ErrRequestBody)
	}
	p.length += size
	p.pieces = append(p.pieces, streamFormPiece{prefix: bytes.Clone(prefix), path: path, info: info})
	return nil
}

type streamFormReader struct {
	reader io.Reader
	files  []*streamFileReader
	closed atomic.Bool
}

func (r *streamFormReader) Read(p []byte) (int, error) {
	if r.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	return r.reader.Read(p)
}

func (r *streamFormReader) Close() error {
	if r.closed.Swap(true) {
		return nil
	}
	var first error
	for _, file := range r.files {
		if err := file.Close(); first == nil && err != nil {
			first = err
		}
	}
	return first
}

// 文件按需打开，Close 可与 Read 并发执行，不持有读取期间的互斥锁。
type streamFileReader struct {
	path      string
	info      os.FileInfo
	remaining int64
	done      bool
	readErr   error
	mu        sync.Mutex
	file      *os.File
	closed    bool
}

func (r *streamFileReader) open() (*os.File, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, io.ErrClosedPipe
	}
	file := r.file
	r.mu.Unlock()
	if file != nil {
		return file, nil
	}
	file, err := os.Open(r.path)
	if err != nil {
		return nil, err
	}
	if err := r.check(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		_ = file.Close()
		return nil, io.ErrClosedPipe
	}
	r.file = file
	return file, nil
}

func (r *streamFileReader) check(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !os.SameFile(r.info, info) || info.Size() != r.info.Size() || !info.ModTime().Equal(r.info.ModTime()) {
		return fmt.Errorf("upload file changed after encoding")
	}
	return nil
}

func (r *streamFileReader) Read(p []byte) (int, error) {
	if r.readErr != nil {
		return 0, r.readErr
	}
	if r.done {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	file, err := r.open()
	if err != nil {
		r.readErr = uploadFileError("open", r.path, err)
		return 0, r.readErr
	}
	n := 0
	if r.remaining > 0 {
		n, err = file.Read(p[:min(int64(len(p)), r.remaining)])
		r.remaining -= int64(n)
		if err == io.EOF && r.remaining > 0 {
			err = io.ErrUnexpectedEOF
		}
	}
	if r.remaining == 0 && (err == nil || err == io.EOF) {
		err = r.check(file)
	}
	if err != nil || r.remaining == 0 {
		closeErr := r.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			r.readErr = uploadFileError("read", r.path, err)
			return n, r.readErr
		}
		r.done = true
		return n, io.EOF
	}
	return n, nil
}

func (r *streamFileReader) Close() error {
	r.mu.Lock()
	file := r.file
	r.file = nil
	r.closed = true
	r.mu.Unlock()
	if file != nil {
		if err := file.Close(); err != nil {
			return uploadFileError("close", r.path, err)
		}
	}
	return nil
}

func uploadFileError(op, path string, err error) error {
	return fmt.Errorf("%w: %s %q: %w", ErrUploadFile, op, path, err)
}
