package body

import (
	"io"
)

type Body interface {
	GetData() string
	GetContentType() string
	Encode() io.Reader
}