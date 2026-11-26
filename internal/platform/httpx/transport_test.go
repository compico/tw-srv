package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewTransportDisablesHTTP2(t *testing.T) {
	tr := NewTransport()
	if tr.ForceAttemptHTTP2 || tr.TLSNextProto == nil || len(tr.TLSNextProto) != 0 {
		t.Fatalf("HTTP/2 должен быть выключен: force=%v nextProto=%v", tr.ForceAttemptHTTP2, tr.TLSNextProto)
	}
	if tr.IdleConnTimeout <= 0 || tr.IdleConnTimeout > time.Minute {
		t.Fatalf("IdleConnTimeout = %v", tr.IdleConnTimeout)
	}
	if NewTransport() == tr {
		t.Fatal("каждый вызов должен возвращать свой транспорт")
	}
}

// Даже сервер с включённым HTTP/2 должен получить запрос по HTTP/1.1.
func TestTransportSpeaksHTTP11(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	tr := NewTransport()
	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig не задан")
	}

	if len(tr.TLSClientConfig.NextProtos) != 1 ||
		tr.TLSClientConfig.NextProtos[0] != "http/1.1" {
		t.Fatalf("ожидался только http/1.1, получено: %v",
			tr.TLSClientConfig.NextProtos)
	}

	tr.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "HTTP/1.1" {
		t.Fatalf("протокол %q, ожидали HTTP/1.1", body)
	}
}
