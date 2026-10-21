package proxy

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestProtectedDialRejectsFailedProtection(t *testing.T) {
	denied := errors.New("protect denied")
	called := false
	SetPhysicalNetwork(func(int32) error { called = true; return denied }, func(string) ([]string, error) { return []string{"127.0.0.1"}, nil })
	defer SetPhysicalNetwork(nil, nil)
	conn, err := dialPhysical(context.Background(), "tcp", "private-test.invalid:9", time.Second)
	if conn != nil || !errors.Is(err, denied) || !called {
		t.Fatalf("conn=%v err=%v called=%v", conn, err, called)
	}
}

func TestPhysicalResolverAndSocketShutdown(t *testing.T) {
	server, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := server.Accept()
		if e == nil {
			accepted <- c
		}
	}()
	resolved := false
	SetPhysicalNetwork(func(int32) error { return nil }, func(host string) ([]string, error) {
		resolved = host == "physical.invalid"
		return []string{"127.0.0.1"}, nil
	})
	defer SetPhysicalNetwork(nil, nil)
	_, port, _ := net.SplitHostPort(server.Addr().String())
	conn, err := dialPhysical(context.Background(), "tcp", "physical.invalid:"+port, time.Second)
	if err != nil || !resolved {
		t.Fatalf("resolved=%v err=%v", resolved, err)
	}
	peer := <-accepted
	defer peer.Close()
	ClosePhysicalSockets()
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err == nil {
		t.Fatal("stop did not close transport socket")
	}
	_ = conn.Close()
}

func TestInconsistentProvisionCancelsProxy(t *testing.T) {
	p := NewProxy(Config{NumConns: 1, UseWrapA: true, WrapAPassword: "test", DeviceID: "test"})
	defer p.Stop()
	first := &WrapAProvision{Address: "10.66.66.2/32", PrivateKeyHex: "a", PeerPublicKeyHex: "b", MTU: 1280, KeepaliveSec: 25}
	if err := p.storeWrapAProvision(first); err != nil {
		t.Fatal(err)
	}
	changed := *first
	changed.Address = "10.66.66.3/32"
	if err := p.storeWrapAProvision(&changed); err == nil || !p.Stopped() {
		t.Fatal("inconsistent provision was accepted")
	}
}

func TestStopWaitsForWorkersAndRejectsLateWorkers(t *testing.T) {
	p := NewProxy(Config{NumConns: 1})
	release := make(chan struct{})
	started := make(chan struct{})
	p.startWorker(func() { close(started); <-release })
	<-started
	stopped := make(chan struct{})
	go func() { p.Stop(); close(stopped) }()
	<-p.ctx.Done()
	select {
	case <-stopped:
		t.Fatal("stop completed while worker was alive")
	default:
	}
	var late atomic.Bool
	p.startWorker(func() { late.Store(true) })
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not finish after worker release")
	}
	if late.Load() {
		t.Fatal("late worker started after stop")
	}
}
