package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCoverURLRejectsPrivateTargets(t *testing.T) {
	for _, raw := range []string{
		"http://cdn.example/a.png",
		"https://user:pass@cdn.example/a.png",
		"https://localhost/a.png",
		"https://127.0.0.1/a.png",
		"https://[::1]/a.png",
		"https://10.1.2.3/a.png",
		"https://169.254.169.254/latest/meta-data",
		"https://metadata.google.internal/a.png",
		"https://cdn.example:8443/a.png",
		"file:///etc/passwd",
	} {
		if _, err := validateCoverURL(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := validateCoverURL("https://cdn.example/a.png"); err != nil {
		t.Fatal(err)
	}
	if err := publicIP(net.ParseIP("8.8.8.8")); err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"10.0.0.1", "192.168.0.1", "172.16.0.1", "100.64.1.1", "0.1.2.3", "169.254.1.1"} {
		if err := publicIP(net.ParseIP(ip)); err == nil {
			t.Fatalf("accepted %s", ip)
		}
	}
}

func TestFetchCoverChecksTypeSizeAndAddress(t *testing.T) {
	pngBytes := tinyPNG(t)
	var dialed []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes)
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>no</html>"))
		case "/big":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", "8388609")
			w.WriteHeader(http.StatusOK)
		case "/go":
			http.Redirect(w, r, "https://evil.example/secret.png", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	tr := srv.Client().Transport.(*http.Transport)
	tlsCfg := tr.TLSClientConfig.Clone()
	tlsCfg.ServerName = "127.0.0.1"

	lookup := func(_ context.Context, host string) ([]net.IP, error) {
		switch host {
		case "cdn.example":
			return []net.IP{net.ParseIP("8.8.8.8")}, nil
		case "evil.example":
			return []net.IP{net.ParseIP("10.1.1.1")}, nil
		case "rebind.example":
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		default:
			return nil, errors.New("no such host")
		}
	}
	opts := coverFetchOptions{
		lookup: lookup,
		dial: func(_ context.Context, network, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			return net.Dial(network, srv.Listener.Addr().String())
		},
		tlsConfig: tlsCfg,
	}
	body, ct, err := fetchCover(context.Background(), "https://cdn.example/ok.png", opts)
	if err != nil || ct != "image/png" || len(body) == 0 {
		t.Fatalf("fetch %s %d %v", ct, len(body), err)
	}
	if _, _, err := fetchCover(context.Background(), "https://cdn.example/page", opts); !errors.Is(err, errCoverType) {
		t.Fatalf("html %v", err)
	}
	if _, _, err := fetchCover(context.Background(), "https://cdn.example/big", opts); !errors.Is(err, errCoverSize) {
		t.Fatalf("size %v", err)
	}
	before := len(dialed)
	if _, _, err := fetchCover(context.Background(), "https://rebind.example/ok.png", opts); !errors.Is(err, errCoverURL) {
		t.Fatalf("rebind %v", err)
	}
	if len(dialed) != before {
		t.Fatalf("dialed private address %v", dialed[before:])
	}
	if _, _, err := fetchCover(context.Background(), "https://cdn.example/go", opts); !errors.Is(err, errCoverURL) {
		t.Fatalf("redirect %v", err)
	}
	for _, addr := range dialed {
		if strings.Contains(addr, "evil.example") {
			t.Fatalf("dialed blocked redirect %s", addr)
		}
	}
}
