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
	request *http.Request
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

func (this *Request) SetDebug(d bool) *Request {
	this.debug=d
	return this
}

func (this *Request) SetData(name,value string) *Request {
	this.data.Set(name,value)
	return this
}

func (this *Request) SetFileData(name,value string,isFile bool) *Request  {
	if isFile==true{
		fd,err:=os.Open(value)

		if err!=nil {
			this.err=err
			return this
		}
		defer fd.Close()

		fileWriter,_:=this.fileData.bodyWrite.CreateFormFile(name,filepath.Base(value))
		_,_=io.Copy(fileWriter,fd)
	}else{
		_ = this.fileData.bodyWrite.WriteField(name, value)
	}

	return this
}

func (this *Request) Send() *Request {
	_ = this.fileData.bodyWrite.Close()
	var err error
	this.request,err=http.NewRequest(this.method,this.url,strings.NewReader(this.data.Encode()))
	defer this.log(false)
	if err!=nil {
		this.err=err
		return this
	}

	if this.method=="POST" {
		this.request.Header.Set("Content-Type","application/x-www-form-urlencoded")
	}
	for k,v:=range this.header {
		this.request.Header.Set(k,v)
	}

	for _,v:= range *this.cookies {
		this.request.AddCookie(v)
	}
	
	response,err:=this.httpc.client.Do(this.request)
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
	var err error

	this.request,err=http.NewRequest(this.method,this.url,ioutil.NopCloser(this.fileData.bodyBuf))
	defer this.log(true)
	if err!=nil {
		this.err=err
		return this
	}

	this.request.Header.Set("Content-Type",contentType)
	for k,v:=range this.header {
		this.request.Header.Set(k,v)
	}

	for _,v:= range *this.cookies {
		this.request.AddCookie(v)
	}

	response,err:=this.httpc.client.Do(this.request)
	if err!=nil {
		this.err=err
		return this
	}

	this.response=response
	return this
}

func (this *Request) log(isFile bool) {
	if this.debug==true {
		var data =make(map[string][]string)
		if isFile {

		}else{
			for k,v:= range this.data {
				data[k]=v
			}
		}
		fmt.Printf("[httpc Debug]\n")
		fmt.Printf("-------------------------------------------------------------------\n")
		fmt.Printf("Request: %s %s\nHeader: %v\nCookies: %v\nBody: %v\n",this.method,this.url,this.request.Header,this.request.Cookies(),data)
		fmt.Printf("-------------------------------------------------------------------\n")
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

func (this *Request) EndFile(saveFile string) (*http.Response,error)  {
	if this.err!=nil {
		return nil,errors.New(this.err.Error())
	}

	bodyByte,_:=ioutil.ReadAll(this.response.Body)
	err:= ioutil.WriteFile(saveFile, bodyByte, 0777)
	if err!=nil {
		return nil,errors.New(err.Error())
	}
	
	return this.response,nil
}