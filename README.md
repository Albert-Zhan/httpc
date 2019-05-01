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

## 例子

### 1. 简单请求

```go
req:=httpc.NewRequest(httpc.NewHttpClient())
//get请求
resp,body,err:=req.SetUrl("http://127.0.0.1").Send().End()
post请求
resp,body,err:=req.SetMethod("post").SetUrl("http://127.0.0.1").Send().End()
put请求
resp,body,err:=req.SetMethod("put").SetUrl("http://127.0.0.1").Send().End()
//最简单的get请求，不带返回值
req.SetUrl("http://127.0.0.1").Send()
//带返回值，返回string类型的body
resp,body,err:=req.SetUrl("http://127.0.0.1").Send().End()
//设置头信息，返回byte类型的body
resp,bodyByte,err:=req.SetUrl("http://127.0.0.1").SetHeader("HOST","127.0.0.1").Send().EndByte()
//设置请求包体
_, _, _ = req.SetUrl("http://127.0.0.1").SetData("client", "httpc").Send().End()
//添加cookie
var cookies []*http.Cookie
cookie:=&http.Cookie{Name:"client",Value:"httpc"}
cookies= append(cookies, cookie)
resp,_,_:=req.SetUrl("http://127.0.0.1").SetCookies(&cookies).Send().End()
```

### 2. Search for text blocks

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$tesseract->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setImage(__DIR__.'/img/1.png');
$tesseract->getComponentImages('RIL_WORD',function ($x,$y,$w,$h,$text){
    echo "Result:{$text}X:{$x}Y:{$y}Width:{$w}Height:{$h}";
    echo '<br>';
});
```

### 3. Get result iterator

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$tesseract->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setImage(__DIR__.'/img/1.png')->recognize(0);
$tesseract->getIterator('RIL_TEXTLINE',function ($text,$x1,$y1,$x2,$y2){
    echo "Text:{$text}X1:{$x1}Y1:{$y1}X2:{$x2}Y2:{$y2}";
    echo '<br>';
});
echo $tesseract->getUTF8Text();
```

### 4. Setting image recognition area

Help to improve recognition speed
```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$text=$tesseract->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setImage(__DIR__.'/img/1.png')
->setRectangle(100,100,100,100)
->getUTF8Text();
echo $text;
```

### 5. Setting Page Segmentation Mode

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$tesseract->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setPageSegMode('PSM_AUTO')
->setImage(__DIR__.'/img/1.png')
->recognize(0)
->analyseLayout()
echo $tesseract->getUTF8Text();
```

## API

### setVariable($name,$value)

Setting additional parameters

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
//Example1
$tesseract->setVariable('save_blob_choices','T');
//Example2
$tesseract->setVariable('tessedit_char_whitelist','0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ');
//Example3
$tesseract->setVariable('tessedit_char_blacklist','xyz');
```

setVariable Options Reference:http://www.sk-spell.sk.cx/tesseract-ocr-parameters-in-302-version


### init($dir,$lang,$mod='OEM_DEFAULT')

Tesseract initialization

Traineddata download:https://github.com/tesseract-ocr/tessdata

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
//Traineddata directory must / end
$tesseract->setVariable('save_blob_choices','T')->init(__DIR__.'/traineddata/tessdata-fast/','eng');
//Multiple languages
$tesseract->setVariable('save_blob_choices','T')->init(__DIR__.'/traineddata/tessdata-fast/','eng+chi_sim');
//Setting Engine Mode
$tesseract->setVariable('save_blob_choices','T')->init(__DIR__.'/traineddata/tessdata-raw/','eng','OEM_TESSERACT_LSTM_COMBINED');
```

Engine Mode Options:
- OEM_DEFAULT(Default, based on what is available.)
- OEM_LSTM_ONLY(Neural nets LSTM engine only.)
- OEM_TESSERACT_LSTM_COMBINED(Legacy + LSTM engines.)
- OEM_TESSERACT_ONLY(Legacy engine only.)

### setPageSegMode($name)

Setting Paging Mode

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$tesseract->setVariable('save_blob_choices','T')
->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setPageSegMode('PSM_AUTO');
```
PageSegMode Options Reference:https://rmtheis.github.io/tess-two/javadoc/com/googlecode/tesseract/android/TessBaseAPI.PageSegMode.html
### setImage($path)

Setting Recognition Pictures

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
//Support png, jpg, jpeg, tif, webp format
$tesseract->setVariable('save_blob_choices','T')
->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setPageSegMode('PSM_AUTO')
->setImage(__DIR__.'/img/1.png');
```

### setRectangle($left,$top,$width,$height)

Setting image recognition area

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$tesseract->->setVariable('save_blob_choices','T')
->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setPageSegMode('PSM_AUTO')
->setImage(__DIR__.'/img/1.png')
->setRectangle(100,100,100,100);
```

### recognize($monitor)

After Recognize, the output is kept internally until the next SetImage

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$tesseract->setVariable('save_blob_choices','T')
->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setPageSegMode('PSM_AUTO')
->setImage(__DIR__.'/img/1.png')
->setRectangle(100,100,100,100)
//For the time being, only 0 or null is supported.
->recognize(0);
```

### analyseLayout()

Application Paging Layout

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$tesseract->setVariable('save_blob_choices','T')
->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setPageSegMode('PSM_AUTO')
->setImage(__DIR__.'/img/1.png')
->setRectangle(100,100,100,100)
->recognize(0)
->analyseLayout();
```

### orientation(&$orientation,&$writingDirection,&$textlineOrder,&$deskewAngle)

Get page layout analysis

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$tesseract->setVariable('save_blob_choices','T')
->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setPageSegMode('PSM_AUTO')
->setImage(__DIR__.'/img/1.png')
->setRectangle(100,100,100,100)
->recognize(0)
->analyseLayout()
->orientation($orientation,$writingDirection,$textlineOrder,$deskewAngle);
```

### getComponentImages($level,$callable)

Search for text blocks

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$tesseract->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setImage(__DIR__.'/img/1.png');
$tesseract->getComponentImages('RIL_WORD',function ($x,$y,$w,$h,$text){
    echo "Result:{$text}X:{$x}Y:{$y}Width:{$w}Height:{$h}";
    echo '<br>';
});
```

PageIteratorLevel Options:
- RIL_BLOCK(Block of text/image/separator line.)
- RIL_PARA(Paragraph within a block.)
- RIL_TEXTLINE(Line within a paragraph.)
- RIL_WORD(Word within a textline.)
- RIL_SYMBOL(Symbol/character within a word.)

### getIterator($level,$callable)

Get result iterator

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$tesseract->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setImage(__DIR__.'/img/1.png')->recognize(0);
$tesseract->getIterator('RIL_TEXTLINE',function ($text,$x1,$y1,$x2,$y2){
    echo "Text:{$text}X1:{$x1}Y1:{$y1}X2:{$x2}Y2:{$y2}";
    echo '<br>';
});
```
See getComponentImages for parameters

### getUTF8Text()

Get UTF8 characters

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$text=$tesseract->init(__DIR__.'/traineddata/tessdata-fast/','eng')
->setImage(__DIR__.'/img/1.png')
->getUTF8Text();
echo $text;
```

### clear()

Free up recognition results and any stored image data

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
$tesseract->init(__DIR__.'/traineddata/tessdata-fast/','eng')
//Three images were recognized normally.
for($i=1;$i<=3;$i++){
    $tesseract->setImage(__DIR__.'/img/'.$i.'.png')
    echo $tesseract->getUTF8Text();
}
//Only one can be identified.
for($i=1;$i<=3;$i++){
   $tesseract->setImage(__DIR__.'/img/'.$i.'.png')
   echo $tesseract->getUTF8Text();
   $tesseract->clear();
}
```

### version()

Get php tesseract version

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
echo $tesseract->version();
```

### tesseract()

Get tesseract version

```php
use tesseract_ocr\Tesseract;
$tesseract=new Tesseract();
echo $tesseract->tesseract();
```

## License

Apache License Version 2.0 see http://www.apache.org/licenses/LICENSE-2.0.html