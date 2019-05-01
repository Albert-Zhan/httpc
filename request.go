package httpc

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type fileData struct {
	bodyBuf *bytes.Buffer
	bodyWrite *multipart.Writer
}

type Request struct {
	httpc *HttpClient
	response *http.Response
	method string
	url string
	header map[string]string
	cookies *[]*http.Cookie
	data url.Values
	fileData fileData
	debug bool
	err error
}

func NewRequest(client *HttpClient) *Request {
	bodyBuf := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(bodyBuf)
	return &Request{
		httpc:client,
		method:"GET",
		header:make(map[string]string),
		cookies:new([]*http.Cookie),
		data:url.Values{},
		fileData:fileData{bodyBuf:bodyBuf,bodyWrite:bodyWriter},
	}
}

func (this *Request) SetMethod(name string) *Request {
	this.method=strings.ToUpper(name)
	return this
}

func (this *Request) SetUrl(url string) *Request {
	this.url=url
	return this
}

func (this *Request) SetHeader(name,value string) *Request {
	this.header[name]=value
	return this
}

func (this *Request) SetCookies(cookies *[]*http.Cookie) *Request  {
	this.cookies=cookies
	return this
}

func (this *Request) SetData(name,value string) *Request {
	this.data.Set(name,value)
	return this
}

func (this *Request) SetDebug(d bool) *Request {
	this.debug=d
	return this
}

func (this *Request) SetFileData(name,value string,isFile bool) *Request  {
	if isFile==true{
		fileWriter,_:=this.fileData.bodyWrite.CreateFormFile(name,filepath.Base(value))

		fd,_:=os.Open(value)
		defer fd.Close()

		_,_=io.Copy(fileWriter,fd)
	}else{
		_ = this.fileData.bodyWrite.WriteField(name, value)
	}

	return this
}

func (this *Request) Send() *Request {

	_ = this.fileData.bodyWrite.Close()

	request,err:=http.NewRequest(this.method,this.url,strings.NewReader(this.data.Encode()))
	defer this.log(request)
	if err!=nil {
		this.err=err
		return this
	}

	if this.method=="POST" {
		request.Header.Set("Content-Type","application/x-www-form-urlencoded")
	}
	for k,v:=range this.header {
		request.Header.Set(k,v)
	}

	for _,v:= range *this.cookies {
		request.AddCookie(v)
	}
	
	response,err:=this.httpc.client.Do(request)
	if err!=nil {
		this.err=err
		return this
	}
	this.response=response

	return this
}

func (this *Request) SendFile() *Request {

	contentType:=this.fileData.bodyWrite.FormDataContentType()
	_ = this.fileData.bodyWrite.Close()

	request,err:=http.NewRequest(this.method,this.url,ioutil.NopCloser(this.fileData.bodyBuf))
	defer this.log(request)
	if err!=nil {
		this.err=err
		return this
	}

	request.Header.Set("Content-Type",contentType)
	request.Cookies()
	for k,v:=range this.header {
		request.Header.Set(k,v)
	}

	for _,v:= range *this.cookies {
		request.AddCookie(v)
	}

	response,err:=this.httpc.client.Do(request)
	if err!=nil {
		this.err=err
		return this
	}
	this.response=response

	return this

}

func (this *Request) log(req *http.Request) {
	if this.debug==true {
		fmt.Printf("[HttpRequest Debug]\n")
		fmt.Printf("-------------------------------------------------------------------\n")
		fmt.Printf("Request: %s %s\nHeader: %v\nBody: %s\n",this.method,this.url,req.Header,this.data)
		fmt.Printf("-------------------------------------------------------------------\n\n")
	}
}

func (this *Request) End() (*http.Response,string,error) {

	if this.err!=nil {
		return nil,"",errors.New(this.err.Error())
	}

	bodyByte,_:=ioutil.ReadAll(this.response.Body)

	return this.response,string(bodyByte),nil

}

func (this *Request) EndByte() (*http.Response,[]byte,error) {

	if this.err!=nil {
		return nil,[]byte(""),errors.New(this.err.Error())
	}

	bodyByte,_:=ioutil.ReadAll(this.response.Body)

	return this.response,bodyByte,nil

}