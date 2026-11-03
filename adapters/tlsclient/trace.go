package tlsclient

import (
	"context"
	"crypto/tls"
	"net/http/httptrace"
	"net/textproto"

	ftrace "github.com/bogdanfinn/fhttp/httptrace"
	utls "github.com/bogdanfinn/utls"
)

// fhttp 与标准库使用不同的 Trace context key，必须逐项桥接。
func bridgeTrace(ctx context.Context) context.Context {
	t := httptrace.ContextClientTrace(ctx)
	if t == nil {
		return ctx
	}
	f := &ftrace.ClientTrace{
		GetConn: t.GetConn, PutIdleConn: t.PutIdleConn, GotFirstResponseByte: t.GotFirstResponseByte,
		Got100Continue: t.Got100Continue, Got1xxResponse: t.Got1xxResponse,
		ConnectStart: t.ConnectStart, ConnectDone: t.ConnectDone,
		TLSHandshakeStart: t.TLSHandshakeStart, WroteHeaderField: t.WroteHeaderField,
		WroteHeaders: t.WroteHeaders, Wait100Continue: t.Wait100Continue,
	}
	if t.GotConn != nil {
		f.GotConn = func(i ftrace.GotConnInfo) {
			t.GotConn(httptrace.GotConnInfo{Conn: i.Conn, Reused: i.Reused, WasIdle: i.WasIdle, IdleTime: i.IdleTime})
		}
	}
	if t.DNSStart != nil {
		f.DNSStart = func(i ftrace.DNSStartInfo) { t.DNSStart(httptrace.DNSStartInfo{Host: i.Host}) }
	}
	if t.DNSDone != nil {
		f.DNSDone = func(i ftrace.DNSDoneInfo) {
			t.DNSDone(httptrace.DNSDoneInfo{Addrs: i.Addrs, Err: i.Err, Coalesced: i.Coalesced})
		}
	}
	if t.TLSHandshakeDone != nil {
		f.TLSHandshakeDone = func(state utls.ConnectionState, err error) { t.TLSHandshakeDone(standardTLS(state), err) }
	}
	if t.WroteRequest != nil {
		f.WroteRequest = func(i ftrace.WroteRequestInfo) { t.WroteRequest(httptrace.WroteRequestInfo{Err: i.Err}) }
	}
	old := ftrace.ContextClientTrace(ctx)
	ctx = ftrace.WithClientTrace(ctx, f)
	// 上游的自动组合会丢弃第一个 Got1xxResponse 返回值，显式保留双方错误。
	if old != nil && old.Got1xxResponse != nil && t.Got1xxResponse != nil {
		f.Got1xxResponse = func(code int, header textproto.MIMEHeader) error {
			if err := t.Got1xxResponse(code, header); err != nil {
				return err
			}
			return old.Got1xxResponse(code, header)
		}
	}
	return ctx
}

// 仅映射两种 TLS 状态共有的公开信息；不伪造 ExportKeyingMaterial 的内部状态。
func standardTLS(s utls.ConnectionState) tls.ConnectionState {
	return tls.ConnectionState{
		Version: s.Version, HandshakeComplete: s.HandshakeComplete, DidResume: s.DidResume,
		CipherSuite: s.CipherSuite, CurveID: tls.CurveID(s.CurveID), NegotiatedProtocol: s.NegotiatedProtocol,
		NegotiatedProtocolIsMutual: s.NegotiatedProtocolIsMutual, ServerName: s.ServerName,
		PeerCertificates: s.PeerCertificates, VerifiedChains: s.VerifiedChains,
		SignedCertificateTimestamps: s.SignedCertificateTimestamps, OCSPResponse: s.OCSPResponse,
		TLSUnique: s.TLSUnique, ECHAccepted: s.ECHAccepted,
	}
}
