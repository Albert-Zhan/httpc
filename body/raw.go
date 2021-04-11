package body

import (
	"io"
	"strings"
)

const (
	Text="text/plain"
	JavaScript="application/javascript"
	Json="application/json"
	Html="text/html"
	Xml="application/xml"
)

type Raw struct {
	data string
	format string
}

func NewRawData() *Raw {
	return &Raw{
		data: "",
		format: "",
	}
}

func (this *Raw) SetData(data,format string) {
	this.data=data
	this.format=format
}

func (this *Raw) GetData() string {
	return this.data
}

func (this *Raw) GetContentType() string {
	return this.format
}

func (this *Raw) Encode() io.Reader {
	return strings.NewReader(this.data)
}
