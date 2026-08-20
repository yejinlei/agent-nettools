package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/quic-go/quic-go/http3"
)

type HTTP3Proxy struct {
	cfg Config
}

func NewHTTP3(cfg Config) Proxy { return &HTTP3Proxy{cfg: cfg} }

func (h *HTTP3Proxy) Name() string { return h.cfg.Name }

func (h *HTTP3Proxy) Connect(ctx context.Context, addr string) (net.Conn, error) {
	sni := h.cfg.SNI
	if sni == "" {
		sni = h.cfg.Server
	}

	tlsCfg := &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: sni == "",
	}

	rt := &http3.RoundTripper{
		TLSClientConfig: tlsCfg,
	}

	req, err := http.NewRequest(http.MethodConnect, "http://"+addr, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)

	resp, err := rt.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("http3 connect %s:443: %w", h.cfg.Server, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("http3 connect %s: %d %s", h.cfg.Server, resp.StatusCode, resp.Status)
	}
	if rw, ok := resp.Body.(io.ReadWriteCloser); ok {
		return &http3Conn{rw: rw}, nil
	}
	resp.Body.Close()
	return nil, fmt.Errorf("http3: response body not a stream")
}

type http3Conn struct {
	rw io.ReadWriteCloser
}

func (c *http3Conn) Read(b []byte) (int, error)        { return c.rw.Read(b) }
func (c *http3Conn) Write(b []byte) (int, error)       { return c.rw.Write(b) }
func (c *http3Conn) Close() error                      { return c.rw.Close() }
func (c *http3Conn) LocalAddr() net.Addr               { return nil }
func (c *http3Conn) RemoteAddr() net.Addr              { return nil }
func (c *http3Conn) SetDeadline(t time.Time) error     { return nil }
func (c *http3Conn) SetReadDeadline(t time.Time) error { return nil }
func (c *http3Conn) SetWriteDeadline(t time.Time) error { return nil }

func (h *HTTP3Proxy) Latency(url string) (time.Duration, error) {
	host := url
	if !strings.Contains(host, ":") {
		host = host + ":443"
	}
	start := time.Now()
	conn, err := h.Connect(context.Background(), host)
	if err != nil {
		return 0, err
	}
	conn.Close()
	return time.Since(start), nil
}

func (h *HTTP3Proxy) Close() error    { return nil }
func (h *HTTP3Proxy) ServerAddr() string { return net.JoinHostPort(h.cfg.Server, strconv.Itoa(h.cfg.Port)) }