package httpc

import (
	"compress/gzip"
	"errors"
	"fmt"
	"github.com/Albert-Zhan/httpc/body"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
)

type Request struct {
	httpc *HttpClient
	request *http.Request
	response *http.Response
	method string
	url string
	param *url.Values
	header map[string]string
	cookies *[]*http.Cookie
	data body.Body
	debug bool
	err error
}

func NewRequest(client *HttpClient) *Request {
	return &Request{
		httpc:client,
		method:"GET",
		param:&url.Values{},
		header:make(map[string]string),
		cookies:new([]*http.Cookie),
		debug: false,
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

func (this *Request) SetParam(name,value string) *Request {
	this.param.Add(name,value)
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

func (this *Request) SetBody(body body.Body) *Request {
	this.data=body
	return this
}

func (this *Request) Send() *Request {
	param:=this.param.Encode()
	if param!="" {
		this.url+="?"+param
	}

	var err error
	this.request,err=http.NewRequest(this.method,this.url,this.data.Encode())
	defer this.log()
	if err!=nil {
		this.err=err
		return this
	}

	contentType:=this.data.GetContentType()
	if contentType!="" {
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

func (this *Request) log() {
	if this.debug==true {
		fmt.Printf("[httpc Debug]\n")
		fmt.Printf("-------------------------------------------------------------------\n")
		fmt.Printf("Request: %s %s\nHeader: %v\nCookies: %v\n",this.method,this.url,this.request.Header,this.request.Cookies())
		fmt.Printf("Body: %s\n",this.data.GetData())
		fmt.Printf("-------------------------------------------------------------------\n")
	}
}

func (this *Request) End() (*http.Response,string,error) {
	if this.err!=nil {
		return nil,"",errors.New(this.err.Error())
	}

	var bodyByte []byte

	if this.response.Header.Get("Content-Encoding")=="gzip"{
		reader,_:=gzip.NewReader(this.response.Body)
		defer reader.Close()
		bodyByte,_=ioutil.ReadAll(reader)
	}else{
		bodyByte,_=ioutil.ReadAll(this.response.Body)
	}
	_=this.response.Body.Close()
	return this.response,string(bodyByte),nil

}

func (this *Request) EndByte() (*http.Response,[]byte,error) {
	if this.err!=nil {
		return nil,[]byte(""),errors.New(this.err.Error())
	}

	var bodyByte []byte

	if this.response.Header.Get("Content-Encoding")=="gzip"{
		reader,_:=gzip.NewReader(this.response.Body)
		defer reader.Close()
		bodyByte,_=ioutil.ReadAll(reader)
	}else{
		bodyByte,_=ioutil.ReadAll(this.response.Body)
	}
	_=this.response.Body.Close()
	return this.response,bodyByte,nil

}

func (this *Request) EndFile(savePath,saveFileName string) (*http.Response,error)  {
	if this.err!=nil {
		return nil,errors.New(this.err.Error())
	}

	if this.response.StatusCode!=http.StatusOK {
		return nil,errors.New("Not written")
	}

	if saveFileName=="" {
		path:=strings.Split(this.request.URL.String(),"/")
		if len(path)>1 {
			saveFileName=path[len(path)-1]
		}
	}

	bodyByte,_:=ioutil.ReadAll(this.response.Body)
	_=this.response.Body.Close()
	err:= ioutil.WriteFile(savePath+saveFileName, bodyByte, 0777)
	if err!=nil {
		return nil,errors.New(err.Error())
	}
	
	return this.response,nil
}
