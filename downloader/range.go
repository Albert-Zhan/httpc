package downloader

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var contentRangePattern = regexp.MustCompile(`^bytes ([0-9]+)-([0-9]+)/([0-9]+|\*)$`)

func strongETag(value string) bool {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	for i := 1; i < len(value)-1; i++ {
		c := value[i]
		if c != 0x21 && (c < 0x23 || c == 0x7f) {
			return false
		}
	}
	return true
}

func contentRange(value string) (start, end, total int64, err error) {
	m := contentRangePattern.FindStringSubmatch(strings.TrimSpace(value))
	err = fmt.Errorf("%w: invalid Content-Range %q", ErrInvalidResponse, value)
	if m == nil {
		return
	}
	var e error
	if start, e = strconv.ParseInt(m[1], 10, 64); e != nil {
		return
	}
	if end, e = strconv.ParseInt(m[2], 10, 64); e != nil || end < start {
		return
	}
	total = -1
	if m[3] != "*" {
		if total, e = strconv.ParseInt(m[3], 10, 64); e != nil || total <= end {
			return
		}
	}
	err = nil
	return
}

func identity(resp *http.Response) bool {
	encoding := strings.TrimSpace(resp.Header.Get("Content-Encoding"))
	return !resp.Uncompressed && (encoding == "" || strings.EqualFold(encoding, "identity"))
}

// copyExact 不写出分段边界，仍检查多出来的数据，识别伪造的长度。
func copyExact(dst io.Writer, src io.Reader, n int64) error {
	if _, err := io.CopyN(dst, src, n); err != nil {
		if err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	var extra [1]byte
	nRead, err := io.ReadFull(src, extra[:])
	if nRead != 0 {
		return fmt.Errorf("%w: body exceeds declared range", ErrInvalidResponse)
	}
	if err != io.EOF {
		return err
	}
	return nil
}
