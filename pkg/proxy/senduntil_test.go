package proxy

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func TestSendPacketUntilCloseUnblocksFullQueue(t *testing.T) {
	p := &Proxy{ctx: context.Background(), sendCh: make(chan sendItem)}
	closed := make(chan struct{})
	result := make(chan error, 1)
	go func() { result <- p.SendPacketUntil(closed, []byte("packet")) }()
	select {
	case err := <-result:
		t.Fatalf("write returned before close: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(closed)
	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("closed bind leaves send blocked")
	}
	if p.ctx.Err() != nil {
		t.Fatal("bind close stopped shared transport")
	}
}

func TestSendPacketUntilClosedNeverEnqueues(t *testing.T) {
	p := &Proxy{ctx: context.Background(), sendCh: make(chan sendItem, 1)}
	done := make(chan struct{})
	close(done)
	for i := 0; i < 100; i++ {
		if err := p.SendPacketUntil(done, []byte("packet")); !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
	}
	if len(p.sendCh) != 0 {
		t.Fatal("closed sender enqueued a packet")
	}
}

func TestSendPacketUntilBoundsLiveBindWait(t *testing.T) {
	p := &Proxy{ctx: context.Background(), sendCh: make(chan sendItem)}
	result := make(chan error, 1)
	go func() { result <- p.SendPacketUntil(make(chan struct{}), []byte("packet")) }()
	select {
	case err := <-result:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("rebind cannot acquire WireGuard network lock while enqueue waits forever")
	}
}
