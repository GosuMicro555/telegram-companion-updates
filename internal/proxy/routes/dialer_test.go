package routes

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/domain"
)

func TestNewDialerSOCKS5Authenticates(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	result := make(chan string, 1)
	go serveSOCKS5(t, listener, result)

	dial, err := NewDialer(domain.ProxyRoute{
		Protocol: "socks5", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port,
		Username: "proxy-user", Password: "proxy-secret", Enabled: true,
	})
	require.NoError(t, err)

	conn, err := dial(context.Background(), "tcp", "149.154.167.50:443")
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, "proxy-user|proxy-secret|149.154.167.50:443", <-result)
}

func TestNewDialerHTTPConnectAuthenticates(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	result := make(chan string, 1)
	go serveHTTPConnect(t, listener, result, false)

	dial, err := NewDialer(domain.ProxyRoute{
		Protocol: "http", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port,
		Username: "proxy-user", Password: "proxy-secret", Enabled: true,
	})
	require.NoError(t, err)

	conn, err := dial(context.Background(), "tcp", "149.154.167.50:443")
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t,
		"149.154.167.50:443|Basic "+base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-secret")),
		<-result,
	)
}

func TestSOCKS5HonorsContextCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()

	dial, err := NewDialer(domain.ProxyRoute{
		Protocol: "socks5", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Enabled: true,
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = dial(ctx, "tcp", "149.154.167.50:443")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestHTTPConnectHonorsContextCancellationWithoutLeakingCredentials(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	go serveHTTPConnect(t, listener, make(chan string, 1), true)
	dial, err := NewDialer(domain.ProxyRoute{
		Protocol: "http", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port,
		Username: "proxy-user", Password: "never-print-this", Enabled: true,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = dial(ctx, "tcp", "149.154.167.50:443")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotContains(t, err.Error(), "never-print-this")
}

func TestNewDialerRejectsInvalidRoutes(t *testing.T) {
	tests := []domain.ProxyRoute{
		{Protocol: "ftp", Host: "127.0.0.1", Port: 1, Enabled: true},
		{Protocol: "http", Port: 1, Enabled: true},
		{Protocol: "socks5", Host: "127.0.0.1", Port: 0, Enabled: true},
		{Protocol: "socks5", Host: "127.0.0.1", Port: 65536, Enabled: true},
	}
	for _, route := range tests {
		_, err := NewDialer(route)
		require.Error(t, err)
	}
}

func serveSOCKS5(t *testing.T, listener net.Listener, result chan<- string) {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)

	version, _ := reader.ReadByte()
	methodsCount, _ := reader.ReadByte()
	methods := make([]byte, int(methodsCount))
	_, _ = io.ReadFull(reader, methods)
	require.Equal(t, byte(5), version)
	_, _ = conn.Write([]byte{5, 2})

	_, _ = reader.ReadByte()
	usernameLength, _ := reader.ReadByte()
	username := make([]byte, int(usernameLength))
	_, _ = io.ReadFull(reader, username)
	passwordLength, _ := reader.ReadByte()
	password := make([]byte, int(passwordLength))
	_, _ = io.ReadFull(reader, password)
	_, _ = conn.Write([]byte{1, 0})

	_, _ = io.ReadFull(reader, make([]byte, 3))
	addressType, _ := reader.ReadByte()
	require.Equal(t, byte(1), addressType)
	ip := make([]byte, 4)
	_, _ = io.ReadFull(reader, ip)
	portBytes := make([]byte, 2)
	_, _ = io.ReadFull(reader, portBytes)
	target := fmt.Sprintf("%s:%d", net.IP(ip).String(), int(portBytes[0])<<8|int(portBytes[1]))
	_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1})
	result <- string(username) + "|" + string(password) + "|" + target
}

func serveHTTPConnect(t *testing.T, listener net.Listener, result chan<- string, hang bool) {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	request, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	if hang {
		_, _ = io.Copy(io.Discard, conn)
		return
	}
	result <- request.Host + "|" + request.Header.Get("Proxy-Authorization")
	_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
}

func TestHTTPConnectRejectsNon200WithoutLeakingCredentials(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = http.ReadRequest(bufio.NewReader(conn))
		_, _ = io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n")
	}()

	dial, err := NewDialer(domain.ProxyRoute{Protocol: "http", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: "u", Password: "secret-value", Enabled: true})
	require.NoError(t, err)
	_, err = dial(context.Background(), "tcp", "149.154.167.50:443")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "407"))
	require.NotContains(t, err.Error(), "secret-value")
}

func TestHTTPConnectDoesNotReturnConnectionCanceledAfterResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go serveHTTPConnect(t, listener, make(chan string, 1), false)

	ctx, cancel := context.WithCancel(context.Background())
	dialer := httpConnectDialer{
		address:       listener.Addr().String(),
		afterResponse: cancel,
	}
	conn, err := dialer.DialContext(ctx, "tcp", "149.154.167.50:443")
	require.Nil(t, conn)
	require.ErrorIs(t, err, context.Canceled)
}
