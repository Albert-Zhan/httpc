httpc
=======
[![Go Reference](https://pkg.go.dev/badge/github.com/Albert-Zhan/httpc.svg)](https://pkg.go.dev/github.com/Albert-Zhan/httpc)
[![License](https://img.shields.io/badge/license-apache2-blue.svg)](LICENSE)

**Go的一个功能强大、易扩展、易使用的http客户端请求库。适合用于接口请求，模拟浏览器请求，爬虫请求。**

## 特点

- Cookie管理器(适合爬虫和模拟请求)
- 支持HEAD、GET、POST、PUT、DELETE
- 轻松上传文件下载文件
- 支持链式调用
- 支持代理、TLS证书校验、超时和自定义重定向
- 支持Basic Auth、Debug和context超时或取消

## 安装

需要Go 1.23或更新版本。在Go模块目录中执行：

```shell
go get github.com/Albert-Zhan/httpc
```

## API文档

[httpc在线文档](https://pkg.go.dev/github.com/Albert-Zhan/httpc)

## 快速入门

第一个示例可保存为`main.go`，执行`go run main.go`，无需另行启动服务。
后续示例为`main`函数内的代码片段，需按使用情况导入`fmt`、`net/http`、`net/url`、`time`、`context`以及以下包，并将请求地址替换为实际服务地址：

```go
import (
    "github.com/Albert-Zhan/httpc"
    "github.com/Albert-Zhan/httpc/body"
)
```

### 1. 简单的请求

```go
package main

import (
    "fmt"
    "net/http"
    "net/http/httptest"

    "github.com/Albert-Zhan/httpc"
)

func main() {
    //启动本地示例服务
    server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprint(w, "hello httpc")
    }))
    defer server.Close()

    //新建一个请求和http客户端,默认使用GET方法
    req:=httpc.NewRequest(httpc.NewHttpClient())
    resp,body,err:=req.SetUrl(server.URL).Send().End()
    if err!=nil {
        fmt.Println(err)
        return
    }
    fmt.Println(resp.StatusCode)
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
resp,bodyByte,err:=req.SetHeader("Accept","application/json").Send().EndByte()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(bodyByte)
}
```

### 3. 设置请求信息(get)

```go
//新建一个http客户端
client:=httpc.NewHttpClient()
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("get").SetUrl("http://127.0.0.1")
//设置头信息
req.SetHeader("Accept","application/json")
//设置请求信息
resp,body,err:=req.SetParam("client", "httpc").Send().End()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(body)
}
```

### 4. 设置请求信息(post)

```go
//新建一个http客户端
client:=httpc.NewHttpClient()
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("post").SetUrl("http://127.0.0.1")
//设置头信息
req.SetHeader("Accept","application/json")
//设置请求信息
b:=body.NewUrlEncode()
b.SetData("client","httpc")
resp,body,err:=req.SetBody(b).Send().End()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(body)
}
```

### 5. 设置Cookie

```go
//新建一个http客户端
client:=httpc.NewHttpClient()
//新建一个请求
req:=httpc.NewRequest(client)
//设置请求地址
req.SetUrl("http://127.0.0.1")
//设置请求数据
req.SetParam("client", "httpc")
var cookies []*http.Cookie
cookie:=&http.Cookie{Name:"client",Value:"httpc"}
cookies= append(cookies, cookie)
//添加cookie并请求
resp,body,err:=req.SetCookies(&cookies).Send().End()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(body)
}
```

### 6. 上传文件

```go
//新建一个http客户端
client:=httpc.NewHttpClient()
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("post").SetUrl("http://127.0.0.1")
//设置上传的文件
//运行前请准备好./img.png文件
b:=body.NewFormData()
b.SetFile("img1","./img.png")
//设置附加参数
b.SetData("client","httpc")
resp,body,err:=req.SetBody(b).Send().End()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(body)
}
```

### 7. 下载文件

```go
//新建一个http客户端
client:=httpc.NewHttpClient()
//新建一个请求
req:=httpc.NewRequest(client)
//请求保存文件
//保存目录必须已存在,文件名为空时从URL获取
resp,err:=req.SetUrl("http://127.0.0.1/1.zip").Send().EndFile("./","")
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
}
```

### 8. 开启调试

```go
req:=httpc.NewRequest(httpc.NewHttpClient())
req.SetMethod("post").SetUrl("http://127.0.0.1")
b:=body.NewUrlEncode()
b.SetData("client","httpc")
var cookies []*http.Cookie
cookie:=&http.Cookie{Name:"client",Value:"httpc"}
cookies= append(cookies, cookie)
_, _, err:=req.SetBody(b).SetCookies(&cookies).SetDebug(true).Send().End()
if err!=nil {
    fmt.Println(err)
}
```

Debug默认关闭，开启后会打印请求方法、URL、头信息、Cookie和Body，可能包含认证信息。

### 9. 设置Basic Auth

```go
req:=httpc.NewRequest(httpc.NewHttpClient())
resp,body,err:=req.SetUrl("https://example.com/private").
    SetBasicAuth("username","password").Send().End()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp.StatusCode)
    fmt.Println(body)
}
```

`SetBasicAuth`设置`Authorization`请求头，传输凭据时应使用HTTPS。

### 10. 使用context控制请求

```go
ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second)
defer cancel()

client:=httpc.NewHttpClient()
req:=httpc.NewRequest(client)
resp,body,err:=req.SetUrl("http://127.0.0.1").Send(ctx).End()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp.StatusCode)
    fmt.Println(body)
}
```

`Send()`默认使用`context.Background()`；`Send(ctx)`使用传入的context，可通过`cancel()`取消请求。
context截止时间与客户端超时同时生效，以先到者为准。先调用`Send`发送请求，再使用`End`、`EndByte`或`EndFile`读取结果并关闭响应体。

> ⚠ 在实际场景中不建议复用Request，建议每个请求对应一个Request。

## 高级用法

### 1. 设置请求超时

```go
//新建http客户端
client:=httpc.NewHttpClient()
//设置请求超时,默认值为30秒
client.SetTimeout(5*time.Second)
//取消客户端总请求超时,Transport的响应头超时等配置仍然生效
//client.SetTimeout(0)
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("post").SetUrl("http://127.0.0.1")
//设置头信息,返回byte类型的body
resp,bodyByte,err:=req.SetHeader("Accept","application/json").Send().EndByte()
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
resp,bodyByte,err:=req.SetHeader("Accept","application/json").Send().EndByte()
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

复用同一个客户端创建后续请求时，CookieJar会自动保存响应中的Cookie，并为匹配的请求发送Cookie；`Request.SetCookies`则用于手动设置当前请求的Cookie。

`httpc.NewCookieJar()`使用Go标准库`net/http/cookiejar`，`httpc.CookieJar`是`cookiejar.Jar`的类型别名。也可以直接传入标准库Jar或其他实现了`http.CookieJar`接口的对象：

```go
//另需导入 "net/http/cookiejar"
jar,err:=cookiejar.New(nil)
if err!=nil {
    panic(err)
}
client:=httpc.NewHttpClient().SetCookieJar(jar)
//禁用自动Cookie管理：client.SetCookieJar(nil)
```

Cookie作用域遵循标准库规则，与旧版复制实现存在以下区别：

- `jar.Cookies(u)`返回适合向`u`发送的Cookie，不保留`Domain`、`Path`、`Expires`等完整属性。请求头只需要名称和值，无需手动补`Domain`；需要响应原始属性时应读取`resp.Cookies()`，不要将`jar.Cookies(u)`当作完整的Cookie导出或持久化接口。
- 响应未指定有效`Path`时，默认作用域为响应URL的目录。例如`/account/login`设置的Cookie默认用于`/account`及其子路径，不会发送到`/api`。确实需要全站使用时，服务端应设置`Path=/`；手动调用`jar.SetCookies`时也可显式指定`Path: "/"`。原来依赖自动扩展到根路径的代码需要调整。
- `.example.com.cn`等多级域名可由标准库正常匹配。`NewCookieJar()`与`cookiejar.New(nil)`均未配置公共后缀列表；需要阻止向`.com.cn`等公共后缀设置Cookie时，可使用`golang.org/x/net/publicsuffix.List`作为`cookiejar.Options.PublicSuffixList`构造标准库Jar，再传给`SetCookieJar`。

详情参见[标准库cookiejar文档](https://pkg.go.dev/net/http/cookiejar)。

### 3. 设置代理

```go
//新建http客户端
client:=httpc.NewHttpClient()
//设置请求代理
client.SetProxy("http://127.0.0.1:10809")
//恢复直连时,在发送后续请求前调用client.ClearProxy()
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("post").SetUrl("http://127.0.0.1")
//设置头信息,返回byte类型的body
resp,bodyByte,err:=req.SetHeader("Accept","application/json").Send().EndByte()
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
resp,bodyByte,err:=req.SetHeader("Accept","application/json").Send().EndByte()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(bodyByte)
}
```

返回`http.ErrUseLastResponse`时停止跟随重定向，并保留当前3xx响应供读取；默认使用Go标准库的重定向策略。

### 5. 设置TLS证书校验

```go
//新建http客户端
client:=httpc.NewHttpClient()
//默认校验TLS证书,false表示不跳过校验
client.SetSkipVerify(false)
//仅在本地自签名证书测试时使用client.SetSkipVerify(true)
//新建一个请求
req:=httpc.NewRequest(client)
req.SetMethod("get").SetUrl("https://example.com")
//设置头信息,返回byte类型的body
resp,bodyByte,err:=req.SetHeader("Accept","application/json").Send().EndByte()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp)
    fmt.Println(bodyByte)
}
```

### 6. 自定义Transport

```go
//在发送请求前完成客户端配置,客户端可供多个Request复用
client:=httpc.NewHttpClient().SetTimeout(10*time.Second)
client.CustomizeTransport(func(tr *http.Transport) {
    tr.MaxIdleConns=100
    tr.MaxIdleConnsPerHost=10
    tr.ResponseHeaderTimeout=5*time.Second
})
req:=httpc.NewRequest(client)
resp,body,err:=req.SetUrl("http://127.0.0.1").Send().End()
if err!=nil {
    fmt.Println(err)
}else{
    fmt.Println(resp.StatusCode)
    fmt.Println(body)
}
```

客户端配置方法支持链式调用；已有Request可通过`req.SetClient(client)`更换客户端。共享客户端发送请求期间不要修改配置。

## License

Apache License Version 2.0 see https://www.apache.org/licenses/LICENSE-2.0.html
