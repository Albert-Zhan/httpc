httpc
=======
[![GoDoc](https://godoc.org/github.com/2654709623/goreq?status.svg)](https://godoc.org/github.com/2654709623/httpc)
[![License](https://img.shields.io/badge/license-apache2-blue.svg)](LICENSE)

**Go的一个功能强大、易扩展、易使用的http客户端请求库。适合用于接口请求，模拟浏览器请求，爬虫请求。**

## 特点

- Cookie管理器(适合爬虫和模拟请求)
- 支持HEADER、GET、POST、PUT、DELETE
- 轻松上传文件下载文件
- 支持断点下载断点续传(开发中)
- 支持链式调用

## 安装

```shell
go get github.com/2654709623/httpc
```

## API文档

[httpc在线文档](https://godoc.org/github.com/2654709623/httpc)

## 快速入门

### 1. 简单的请求

```go
//新建一个请求和http客户端
req:=httpc.NewRequest(httpc.NewHttpClient())
//get请求,返回string类型的body
resp,body,err:=req.SetUrl("http://127.0.0.1").Send().End()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(body)
}
```

### 2. 设置头信息

```go
//新建一个http客户端
client:=httpc.NewHttpClient()
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("post").SetUrl("http://127.0.0.1")
//设置头信息,返回byte类型的body
resp,bodyByte,err:=req.SetHeader("HOST","127.0.0.1").Send().EndByte()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(bodyByte)
}
```

### 3. 设置请求信息

```go
//新建一个http客户端
client:=httpc.NewHttpClient()
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("post").SetUrl("http://127.0.0.1")
//设置头信息
req.SetHeader("HOST","127.0.0.1")
//设置请求信息
resp,body,err:=req.SetData("client", "httpc").Send().End()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(body)
}
```

### 4. 设置Cookie

```go
//新建一个http客户端
client:=httpc.NewHttpClient()
//新建一个请求
req:=httpc.NewRequest(client)
//设置请求地址和头信息
req.SetUrl("http://127.0.0.1").SetHeader("HOST","127.0.0.1")
//设置请求数据
req.SetData("client", "httpc")
var cookies []*http.Cookie
cookie:=&http.Cookie{Name:"client",Value:"httpc"}
cookies= append(cookies, cookie)
//添加cookie并请求
resp,bodyByte,err:=req.SetCookies(&cookies).Send().End()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(bodyByte)
}
```

### 5. 上传文件

```go
//新建一个http客户端
client:=httpc.NewHttpClient()
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("post").SetUrl("http://127.0.0.1")
//设置上传的文件
req.SetFileData("img1","./img.png",true)
//设置附加参数
req.SetFileData("client","httpc",false)
resp,body,err:=req.Send(true).End()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(body)
}
```

### 6. 下载文件

```go
//新建一个http客户端
client:=httpc.NewHttpClient()
//新建一个请求
req:=httpc.NewRequest(client)
//请求保存文件
resp,body,err:=req.SetUrl("http://127.0.0.1/1.zip").Send().EndFile("./test.zip")
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(body)
}
```

### 7. 开启调试

```go
req:=httpc.NewRequest(httpc.NewHttpClient())
req.SetMethod("post").SetUrl("https://127.0.0.1")
req.SetHeader("HOST","127.0.0.1")
req.SetData("client","httpc")
var cookies []*http.Cookie
cookie:=&http.Cookie{Name:"client",Value:"httpc"}
cookies= append(cookies, cookie)
_, _, _ = req.SetCookies(&cookies).SetDebug(true).Send().End()
```

> ⚠ 在实际场景中不建议复用Request，建议每个请求对应一个Request。

## 高级用法

### 1. 设置请求超时

```go
//新建http客户端
client:=httpc.NewHttpClient()
//设置请求超时,默认值为30秒
client.SetTimeout(5*time.Second)
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("post").SetUrl("http://127.0.0.1")
//设置头信息,返回byte类型的body
resp,bodyByte,err:=req.SetHeader("HOST","127.0.0.1").Send().EndByte()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(bodyByte)
}
```

### 2. 设置COOKIE管理器

```go
//新建http客户端
client:=httpc.NewHttpClient()
//新建一个cookie管理器,后面所有请求的cookie将保存在这
cookieJar:=httpc.NewCookieJar()
//设置cookie管理器,
client.SetCookieJar(cookieJar)
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("post").SetUrl("http://127.0.0.1")
//设置头信息,返回byte类型的body
resp,bodyByte,err:=req.SetHeader("HOST","127.0.0.1").Send().EndByte()
if err!=nil {
    fmt.Println(err)
}else{
    //从cookie管理器中获取当前访问url保存的cookie
    u, _ := url.Parse("http://127.0.0.1")
    cookies:=cookieJar.Cookies(u)
    fmt.Println(cookies)
    fmt.Println(resp)
    fmt.Println(bodyByte)
}
```

### 3. 设置传输连接参数

```go
//新建http客户端
client:=httpc.NewHttpClient()
//设置连接传输参数
client.SetTransport(&http.Transport{
    Proxy:                 http.ProxyFromEnvironment,
    MaxIdleConns:          100,
    IdleConnTimeout:       30 * time.Second,
    TLSHandshakeTimeout:   10 * time.Second,
    ExpectContinueTimeout: 1 * time.Second,
    ResponseHeaderTimeout: 10 * time.Second,
})
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("post").SetUrl("http://127.0.0.1")
//设置头信息,返回byte类型的body
resp,bodyByte,err:=req.SetHeader("HOST","127.0.0.1").Send().EndByte()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(bodyByte)
}
```

### 4. 设置重定向处理

```go
//新建http客户端
client:=httpc.NewHttpClient()
//设置http客户端重定向处理函数
client.SetRedirect(func(req *http.Request, via []*http.Request) error {
    return http.ErrUseLastResponse
})
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("post").SetUrl("http://127.0.0.1")
//设置头信息,返回byte类型的body
resp,bodyByte,err:=req.SetHeader("HOST","127.0.0.1").Send().EndByte()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(bodyByte)
}
```

## License

Apache License Version 2.0 see http://www.apache.org/licenses/LICENSE-2.0.html
