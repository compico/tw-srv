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

	if tr.ForceAttemptHTTP2 {
		t.Fatal("ForceAttemptHTTP2 должен быть false")
	}
	if len(tr.TLSNextProto) != 0 {
		t.Fatalf("ожидался пустой TLSNextProto, получено: %v", tr.TLSNextProto)
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig не задан")
	}
	if len(tr.TLSClientConfig.NextProtos) != 1 ||
		tr.TLSClientConfig.NextProtos[0] != "http/1.1" {
		t.Fatalf("ожидался только http/1.1, получено: %v",
			tr.TLSClientConfig.NextProtos)
	}
	if tr.IdleConnTimeout <= 0 || tr.IdleConnTimeout > time.Minute {
		t.Fatalf("IdleConnTimeout = %v", tr.IdleConnTimeout)
	}
	if NewTransport() == tr {
		t.Fatal("каждый вызов должен возвращать свой транспорт")
	}
}

func TestTransportSpeaksHTTP11(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	tr := NewTransport()

	// Доверяем сертификату тестового HTTPS-сервера,
	// сохраняя NextProtos из NewTransport.
	serverTLS := srv.Client().Transport.(*http.Transport).TLSClientConfig
	tr.TLSClientConfig = serverTLS.Clone()
	tr.TLSClientConfig.NextProtos = []string{"http/1.1"}

	c := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "HTTP/1.1" {
		t.Fatalf("протокол %q, ожидали HTTP/1.1", body)
	}
}
