package gotd

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/gotd/td/telegram/dcs"
	"golang.org/x/net/proxy"

	"telegram-companion/internal/domain"
	proxyroutes "telegram-companion/internal/proxy/routes"
)

func NewProxyResolver(route domain.ProxyRoute) (dcs.Resolver, error) {
	dial, err := proxyroutes.NewDialer(route)
	if err != nil {
		return nil, err
	}
	return dcs.Plain(dcs.PlainOptions{Dial: dcs.DialFunc(dial)}), nil
}

type ProxyRouteRegistry interface {
	RouteFor(context.Context, domain.Account) (domain.ProxyRoute, error)
}

type RegistryResolver struct {
	routes ProxyRouteRegistry
}

func NewRegistryResolver(routes ProxyRouteRegistry) *RegistryResolver {
	return &RegistryResolver{routes: routes}
}

func (r *RegistryResolver) ResolverFor(account domain.Account) (dcs.Resolver, error) {
	if r == nil || r.routes == nil {
		return nil, errors.New("proxy route registry is not configured")
	}
	route, err := r.routes.RouteFor(context.Background(), account)
	if err != nil {
		return nil, err
	}
	return NewProxyResolver(route)
}

func NewTorSOCKSResolverFactory(address string) DCResolverFactory {
	address = strings.TrimSpace(address)
	return func() dcs.Resolver {
		return dcs.Plain(dcs.PlainOptions{Dial: func(ctx context.Context, network, target string) (net.Conn, error) {
			if address == "" {
				return nil, errors.New("SOCKS5 address is required")
			}
			dialer, err := proxy.SOCKS5("tcp", address, nil, &net.Dialer{})
			if err != nil {
				return nil, err
			}
			contextDialer, ok := dialer.(proxy.ContextDialer)
			if !ok {
				return nil, errors.New("SOCKS5 dialer does not support context cancellation")
			}
			return contextDialer.DialContext(ctx, network, target)
		}})
	}
}
