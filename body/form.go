package body

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"os"
	"path/filepath"
)

type Form struct {
	dataBuf *bytes.Buffer
	dataStr string
	data *multipart.Writer
}

func NewFormData() *Form {
	bodyBuf:=&bytes.Buffer{}
	bodyWriter:=multipart.NewWriter(bodyBuf)
	return &Form{
		dataBuf: bodyBuf,
		dataStr: "",
		data: bodyWriter,
	}
}

func (this *Form) SetBoundary(boundary string) *Form {
	_=this.data.SetBoundary(boundary)
	return this
}

func (this *Form) SetData(name,value string) *Form {
	_=this.data.WriteField(name,value)
	return this
}

func (this *Form) SetFile(name,file string) *Form  {
	fd,err:=os.Open(file)
	defer fd.Close()
	if err!=nil {
		panic("file does not exist")
	}
	fileWriter,_:=this.data.CreateFormFile(name,filepath.Base(file))
	_,_=io.Copy(fileWriter,fd)
	return this
}

func (this *Form) GetData() string {
	return this.dataStr
}

func (this *Form) GetContentType() string {
	return this.data.FormDataContentType()
}

func (this *Form) Encode() io.Reader {
	_=this.data.Close()

	r:=multipart.NewReader(this.dataBuf,this.data.Boundary())
	f,err:=r.ReadForm(0)
	if err==nil {
		header:="--"+this.data.Boundary()+"\n"
		dataStr:=header
		foot:="--"+this.data.Boundary()+"--"
		for k,v:=range f.Value {
			dataStr+=fmt.Sprintf(`Content-Disposition: form-data; name="%s"`,k)+"\n"
			dataStr+=v[0]+"\n"
		}
		for fk,fv:=range f.File{
			dataStr+=fmt.Sprintf(`Content-Disposition: form-data; name="%s"; filename="%s" Content-Type: application/octet-stream`,fk,fv[0].Filename)+"\n"
			dataStr+=fmt.Sprintf("%v",fv)+"\n"
		}
		dataStr+=foot
		this.dataStr=dataStr
	}
	return ioutil.NopCloser(this.dataBuf)
}
