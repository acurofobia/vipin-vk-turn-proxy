package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	xproxy "golang.org/x/net/proxy"
)

// Android hooks are installation scoped. The client serializes all VPN sessions.
// Host resolution stays on Android's selected physical Network, including while
// the system VPN is active; it never changes net.DefaultResolver.
var physicalNetwork struct {
	sync.RWMutex
	protect func(int32) error
	resolve func(string) ([]string, error)
	sockets map[net.Conn]struct{}
	epoch   uint64
	stopped bool
}

func SetPhysicalNetwork(protect func(int32) error, resolve func(string) ([]string, error)) {
	ClosePhysicalSockets()
	physicalNetwork.Lock()
	physicalNetwork.protect, physicalNetwork.resolve = protect, resolve
	physicalNetwork.stopped = false
	physicalNetwork.Unlock()
}

func socketControl(_ string, _ string, raw syscall.RawConn) error {
	physicalNetwork.RLock()
	protect := physicalNetwork.protect
	physicalNetwork.RUnlock()
	if protect == nil {
		if runtime.GOOS == "android" {
			return errors.New("Android physical network is not configured")
		}
		return nil
	}
	var protectErr error
	if err := raw.Control(func(fd uintptr) { protectErr = protect(int32(fd)) }); err != nil {
		return err
	}
	return protectErr
}

func resolvePhysical(ctx context.Context, host string) ([]string, error) {
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return []string{ip.String()}, nil
	}
	physicalNetwork.RLock()
	resolve := physicalNetwork.resolve
	physicalNetwork.RUnlock()
	if resolve == nil {
		if runtime.GOOS == "android" {
			return nil, errors.New("Android physical DNS is not configured")
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var ips []string
		for _, a := range addresses {
			if a.IP.To4() != nil {
				ips = append(ips, a.IP.String())
			}
		}
		return ips, nil
	}
	type result struct {
		ips []string
		err error
	}
	ch := make(chan result, 1)
	go func() { ips, err := resolve(host); ch <- result{ips, err} }()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		var ips []string
		for _, address := range r.ips {
			if ip := net.ParseIP(address); ip != nil && ip.To4() != nil {
				ips = append(ips, ip.String())
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("physical DNS returned no IPv4 address")
		}
		return ips, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func resolvePhysicalUDP(ctx context.Context, address string) (*net.UDPAddr, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := resolvePhysical(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("peer has no usable IPv4 address")
	}
	return net.ResolveUDPAddr("udp4", net.JoinHostPort(ips[0], port))
}

type physicalConn struct{ net.Conn }

func (c *physicalConn) Close() error {
	physicalNetwork.Lock()
	delete(physicalNetwork.sockets, c)
	physicalNetwork.Unlock()
	return c.Conn.Close()
}
func (c *physicalConn) SyscallConn() (syscall.RawConn, error) {
	if raw, ok := c.Conn.(syscall.Conn); ok {
		return raw.SyscallConn()
	}
	return nil, errors.New("socket does not expose syscall control")
}

func dialPhysical(ctx context.Context, network, address string, timeout time.Duration) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	physicalNetwork.RLock()
	epoch, stopped := physicalNetwork.epoch, physicalNetwork.stopped
	physicalNetwork.RUnlock()
	if stopped {
		return nil, net.ErrClosed
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := resolvePhysical(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("host has no usable IPv4 address")
	}
	if network == "tcp" {
		network = "tcp4"
	}
	if network == "udp" {
		network = "udp4"
	}
	dialer := net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second, Control: socketControl}
	for _, ip := range ips {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
		if dialErr != nil {
			err = dialErr
			continue
		}
		wrapped := &physicalConn{conn}
		physicalNetwork.Lock()
		if epoch != physicalNetwork.epoch || physicalNetwork.stopped || ctx.Err() != nil {
			physicalNetwork.Unlock()
			_ = conn.Close()
			return nil, net.ErrClosed
		}
		if physicalNetwork.sockets == nil {
			physicalNetwork.sockets = make(map[net.Conn]struct{})
		}
		physicalNetwork.sockets[wrapped] = struct{}{}
		physicalNetwork.Unlock()
		return wrapped, nil
	}
	return nil, fmt.Errorf("physical network dial failed: %w", err)
}

func ClosePhysicalSockets() {
	physicalNetwork.Lock()
	sockets := physicalNetwork.sockets
	physicalNetwork.sockets = nil
	physicalNetwork.epoch++
	physicalNetwork.stopped = true
	physicalNetwork.Unlock()
	for conn := range sockets {
		_ = conn.Close()
	}
	if client := sessionClientIfCreated(); client != nil {
		go client.CloseIdleConnections()
	}
}

type physicalTLSClientDialer struct{ timeout time.Duration }

func (d physicalTLSClientDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}
func (d physicalTLSClientDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dialPhysical(ctx, network, address, d.timeout)
}

func physicalTLSClientOption() tlsclient.HttpClientOption {
	return tlsclient.WithProxyDialerFactory(func(_ string, timeout time.Duration, _ *net.TCPAddr, _ fhttp.Header, _ tlsclient.Logger) (xproxy.ContextDialer, error) {
		return physicalTLSClientDialer{timeout}, nil
	})
}
