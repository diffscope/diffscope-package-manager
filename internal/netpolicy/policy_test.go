package netpolicy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestProxyRouteDelegatesFinalAddress(t *testing.T) {
	if os.Getenv("DSPM_PROXY_TEST_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProxyRouteDelegatesFinalAddress$")
		cmd.Env = append(os.Environ(), "DSPM_PROXY_TEST_CHILD=1")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", b, err)
		}
		return
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "verified") }))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			http.Error(w, "CONNECT required", 400)
			return
		}
		upstream, err := net.Dial("tcp", origin.Listener.Addr().String())
		if err != nil {
			http.Error(w, "dial failed", 502)
			return
		}
		downstream, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		io.WriteString(downstream, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { io.Copy(upstream, downstream); upstream.Close() }()
		go func() { io.Copy(downstream, upstream); downstream.Close() }()
	}))
	defer proxy.Close()
	for _, key := range []string{"NO_PROXY", "no_proxy", "HTTP_PROXY", "http_proxy", "https_proxy", "ALL_PROXY"} {
		t.Setenv(key, "")
	}
	t.Setenv("HTTPS_PROXY", proxy.URL)
	policy := Policy{}
	client := policy.Client(false)
	defer client.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	name := origin.Certificate().DNSNames[0]
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: name}
	raw := "https://archive.example/archive"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := policy.Request(ctx, client, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "verified" || policy.AddressValidation(raw) != "proxy-delegated" {
		t.Fatal(string(body), policy.AddressValidation(raw))
	}
}

func TestRestrictedDialRejectsPrivateOrigin(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "169.254.169.254", "10.0.0.1", "100.64.1.1", "2001:db8::1"} {
		if publicIP(net.ParseIP(raw)) {
			t.Fatal("accepted", raw)
		}
	}
	client := Policy{DisableProxy: true}.Client(false)
	defer client.CloseIdleConnections()
	_, err := client.Get("https://127.0.0.1:1/archive")
	if err == nil || !strings.Contains(err.Error(), "DOWNLOAD_POLICY_VIOLATION") {
		t.Fatal(err)
	}
}
