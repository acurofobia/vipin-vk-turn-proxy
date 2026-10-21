package proxy

import (
	"encoding/base64"
	"net"
	"strings"
	"testing"
)

func TestGetconfDenialsAndSecretRedaction(t *testing.T) {
	for _, response := range []string{"DENIED:wrong_password", "DENIED:expired", "DENIED:device_mismatch", "PrivateKey=SHOULD_NEVER_APPEAR_IN_LOGS"} {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			buf := make([]byte, 4096)
			_, _ = server.Read(buf)
			_, _ = server.Write([]byte(response))
		}()
		_, err := doGetconf(client, "installation-test", "password-test")
		_ = client.Close()
		if err == nil {
			t.Fatalf("accepted %q", response)
		}
		if strings.Contains(err.Error(), "SHOULD_NEVER_APPEAR") {
			t.Fatal("provision response leaked into error")
		}
	}
}

func TestParseWGINIValidatesCompleteProvision(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	valid := "[Interface]\nPrivateKey=" + key + "\nAddress=10.66.66.2/32\nMTU=1280\n[Peer]\nPublicKey=" + key + "\nPersistentKeepalive=25\nEndpoint=127.0.0.1:51820"
	if _, err := parseWGINIToProvision(valid); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.Replace(valid, "10.66.66.2/32", "bad-cidr", 1),
		strings.Replace(valid, "10.66.66.2/32", "2001:db8::1/128", 1),
		strings.Replace(valid, "MTU=1280", "MTU=1501", 1),
		strings.Replace(valid, "MTU=1280", "MTU=invalid", 1),
		strings.Replace(valid, "PersistentKeepalive=25", "PersistentKeepalive=301", 1),
		strings.Replace(valid, "PrivateKey="+key, "PrivateKey=invalid", 1),
	} {
		if _, err := parseWGINIToProvision(invalid); err == nil {
			t.Fatalf("accepted invalid provision %q", invalid)
		}
	}
}
