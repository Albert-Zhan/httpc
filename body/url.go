package body

import (
	"io"
	"net/url"
	"strings"
)

type Url struct {
	data *url.Values
}

func NewUrlEncode() *Url {
	return &Url{
		data: &url.Values{},
	}
}

func (this *Url) SetData(name,value string) *Url {
	this.data.Add(name,value)
	return this
}

func (this *Url) GetData() string {
	return this.data.Encode()
}

func (this *Url) GetContentType() string {
	return "application/x-www-form-urlencoded"
}

func (this *Url) Encode() io.Reader {
	return strings.NewReader(this.data.Encode())
}
