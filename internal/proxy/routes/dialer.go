package routes

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"

	"telegram-companion/internal/domain"
)

const connectHandshakeTimeout = 10 * time.Second

type DialFunc func(ctx context.Context, network, target string) (net.Conn, error)

func NewDialer(route domain.ProxyRoute) (DialFunc, error) {
	protocol := strings.ToLower(strings.TrimSpace(route.Protocol))
	host := strings.TrimSpace(route.Host)
	if !route.Enabled {
		return nil, errors.New("proxy route is disabled")
	}
	if host == "" || route.Port < 1 || route.Port > 65535 {
		return nil, errors.New("valid proxy host and port are required")
	}
	address := net.JoinHostPort(host, strconv.Itoa(route.Port))
	switch protocol {
	case "socks5":
		var auth *proxy.Auth
		if route.Username != "" || route.Password != "" {
			auth = &proxy.Auth{User: route.Username, Password: route.Password}
		}
		dialer, err := proxy.SOCKS5("tcp", address, auth, &net.Dialer{})
		if err != nil {
			return nil, errors.New("create SOCKS5 proxy dialer")
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("SOCKS5 proxy does not support context cancellation")
		}
		return func(ctx context.Context, network, target string) (net.Conn, error) {
			conn, err := contextDialer.DialContext(ctx, network, target)
			if err != nil {
				if ctxErr := contextFailure(ctx); ctxErr != nil {
					return nil, ctxErr
				}
			}
			return conn, err
		}, nil
	case "http":
		dialer := httpConnectDialer{address: address, username: route.Username, password: route.Password}
		return dialer.DialContext, nil
	default:
		return nil, errors.New("proxy protocol must be socks5 or http")
	}
}

type httpConnectDialer struct {
	address       string
	username      string
	password      string
	afterResponse func()
}

func (d httpConnectDialer) DialContext(ctx context.Context, network, target string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.ContainsAny(target, "\r\n") {
		return nil, errors.New("invalid proxy target")
	}
	var base net.Dialer
	conn, err := base.DialContext(ctx, network, d.address)
	if err != nil {
		return nil, fmt.Errorf("connect to HTTP proxy: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = conn.Close()
		}
	}()

	deadline := time.Now().Add(connectHandshakeTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, errors.New("configure HTTP proxy handshake deadline")
	}
	watcherStop := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-watcherStop:
		}
	}()
	watcherStopped := false
	stopWatcher := func() {
		if watcherStopped {
			return
		}
		close(watcherStop)
		<-watcherDone
		watcherStopped = true
	}
	defer stopWatcher()

	request := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if d.username != "" || d.password != "" {
		token := base64.StdEncoding.EncodeToString([]byte(d.username + ":" + d.password))
		request += "Proxy-Authorization: Basic " + token + "\r\n"
	}
	request += "Proxy-Connection: Keep-Alive\r\n\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		if ctxErr := contextFailure(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New("write HTTP CONNECT request")
	}

	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		if ctxErr := contextFailure(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New("read HTTP CONNECT response")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP CONNECT rejected with status %d", response.StatusCode)
	}
	if d.afterResponse != nil {
		d.afterResponse()
	}
	stopWatcher()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, errors.New("clear HTTP proxy handshake deadline")
	}
	succeeded = true
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

func contextFailure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(buffer []byte) (int, error) {
	return c.reader.Read(buffer)
}
