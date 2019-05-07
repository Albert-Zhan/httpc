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

type Request struct {
	httpc *HttpClient
	request *http.Request
	response *http.Response
	method string
	url string
	header map[string]string
	cookies *[]*http.Cookie
	data url.Values
	fileData map[bool]map[string]string
	debug bool
	err error
}

func NewRequest(client *HttpClient) *Request {
	return &Request{
		httpc:client,
		method:"GET",
		header:make(map[string]string),
		cookies:new([]*http.Cookie),
		data:url.Values{},
		fileData:make(map[bool]map[string]string),
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
	this.fileData[isFile]= map[string]string{name:value}
	return this
}

func (this *Request) Send(a ...interface{}) *Request {
	var err error

	if len(a)==0 || a[0]==false {
		this.request,err=http.NewRequest(this.method,this.url,strings.NewReader(this.data.Encode()))
		defer this.log(false)
		if err!=nil {
			this.err=err
			return this
		}

		if this.method=="POST" {
			this.request.Header.Set("Content-Type","application/x-www-form-urlencoded")
		}
	}else{
		bodyBuf := &bytes.Buffer{}
		bodyWriter := multipart.NewWriter(bodyBuf)
		for h,m:=range this.fileData {
			for k,v:= range m {
				if h {
					fd,err:=os.Open(v)
					if err!=nil {
						this.err=err
						return this
					}
					fileWriter,_:=bodyWriter.CreateFormFile(k,filepath.Base(v))
					_,_=io.Copy(fileWriter,fd)
					fd.Close()
				}else{
					_ = bodyWriter.WriteField(k,v)
				}
			}
		}

		contentType:=bodyWriter.FormDataContentType()
		_ = bodyWriter.Close()
		this.request,err=http.NewRequest(this.method,this.url,ioutil.NopCloser(bodyBuf))
		defer this.log(true)
		if err!=nil {
			this.err=err
			return this
		}

		this.request.Header.Set("Content-Type",contentType)
	}
	for k,v:=range this.header {
		this.request.Header.Set(k,v)
	}

	for _,v:= range *this.cookies {
		this.request.AddCookie(v)
	}

	this.response,err=this.httpc.client.Do(this.request)
	if err!=nil {
		this.err=err
		return this
	}

	return this
}

func (this *Request) log(isFile bool) {
	if this.debug==true {
		fmt.Printf("[httpc Debug]\n")
		fmt.Printf("-------------------------------------------------------------------\n")
		fmt.Printf("Request: %s %s\nHeader: %v\nCookies: %v\n",this.method,this.url,this.request.Header,this.request.Cookies())
		if isFile {
			fmt.Printf("Body: %v\n",this.fileData)
		}else{
			fmt.Printf("Body: %v\n",this.data)
		}
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