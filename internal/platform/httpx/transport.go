package httpx

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// NewTransport возвращает транспорт для исходящих запросов к внешним API (Twitch, MemeAlerts).
//
// HTTP/2 отключён намеренно. Он держит ОДНО соединение на хост для всех запросов сразу. Если путь до сервера
// «залипает» (соединение установлено, ответы не приходят), то зависают все запросы разом, а таймаут клиента
// соединение не закрывает: следующие запросы снова уходят в то же мёртвое соединение
// («awaiting headers» у всех стримеров одновременно). По HTTP/1.1 каждый запрос идёт в своём соединении,
// а при таймауте соединение закрывается, и следующий запрос открывает новое.
// Нагрузка у сервиса небольшая, так что мультиплексирование не нужно.
func NewTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 15 * time.Second}).DialContext
	t.ForceAttemptHTTP2 = false
	t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{} // пустая карта: ALPN h2 не предлагается
	t.IdleConnTimeout = 30 * time.Second
	t.TLSHandshakeTimeout = 10 * time.Second
	return t
}
