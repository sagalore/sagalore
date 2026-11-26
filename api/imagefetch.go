package main

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sagalore/sagalore/internal/screenshot"
)

const maxCoverBytes = 8 << 20

// fetchCover is optional. Nil uses fetchCoverURL. Tests point it at a stub.
func (s *Server) fetchCover(ctx context.Context, raw string) ([]byte, string, error) {
	if s != nil && s.fetchCoverFn != nil {
		return s.fetchCoverFn(ctx, raw)
	}
	return fetchCoverURL(ctx, raw)
}

// takeCover turns an optional image_url into bytes. Uploaded bytes are used
// when image_url is empty. Sending both is rejected so a client cannot hide
// one source behind the other.
func (s *Server) takeCover(ctx context.Context, image []byte, contentType, imageURL string) ([]byte, string, error) {
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return image, contentType, nil
	}
	if len(image) > 0 {
		return nil, "", errors.New("send image bytes or image_url, not both")
	}
	return s.fetchCover(ctx, imageURL)
}

func fetchCoverURL(ctx context.Context, raw string) ([]byte, string, error) {
	return fetchCover(ctx, raw, coverFetchOptions{})
}

type coverFetchOptions struct {
	lookup    func(ctx context.Context, host string) ([]net.IP, error)
	dial      func(ctx context.Context, network, addr string) (net.Conn, error)
	tlsConfig *tls.Config
}

func fetchCover(ctx context.Context, raw string, opts coverFetchOptions) ([]byte, string, error) {
	if _, err := validateCoverURL(raw); err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	lookup := opts.lookup
	if lookup == nil {
		lookup = lookupPublicHost
	}
	transport := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       opts.tlsConfig,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil || hostBlocked(host) {
				return nil, errCoverURL
			}
			ips, err := lookup(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, errCoverUnreachable
			}
			for _, ip := range ips {
				if err := publicIP(ip); err != nil {
					return nil, errCoverURL
				}
			}
			if opts.dial != nil {
				return opts.dial(ctx, network, addr)
			}
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "tcp", net.JoinHostPort(ips[0].String(), port))
		},
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 4 {
				return errCoverUnreachable
			}
			if _, err := validateCoverURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(raw), nil)
	if err != nil {
		return nil, "", errCoverURL
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp")
	req.Header.Set("User-Agent", "Sagalore cover fetch")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, errCoverURL) {
			return nil, "", errCoverURL
		}
		return nil, "", errCoverUnreachable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", errCoverUnreachable
	}
	if !coverContentTypeOK(resp.Header.Get("Content-Type")) {
		return nil, "", errCoverType
	}
	if resp.ContentLength > maxCoverBytes {
		return nil, "", errCoverSize
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
	if err != nil {
		return nil, "", errCoverUnreachable
	}
	if len(body) > maxCoverBytes {
		return nil, "", errCoverSize
	}
	out, contentType, err := screenshot.NormalizeUpload(body)
	if err != nil {
		return nil, "", errCoverType
	}
	return out, contentType, nil
}

var (
	errCoverURL         = errors.New("image_url must be an https URL on a public host")
	errCoverUnreachable = errors.New("image_url could not be fetched")
	errCoverType        = errors.New("image_url must be a png, jpeg, gif, or webp")
	errCoverSize        = errors.New("image larger than 8 MiB")
)

func validateCoverURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil {
		return nil, errCoverURL
	}
	if u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Host == "" {
		return nil, errCoverURL
	}
	host := strings.TrimSuffix(u.Hostname(), ".")
	if host == "" || hostBlocked(host) {
		return nil, errCoverURL
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := publicIP(ip); err != nil {
			return nil, errCoverURL
		}
	}
	if port := u.Port(); port != "" && port != "443" {
		return nil, errCoverURL
	}
	return u, nil
}

func hostBlocked(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	switch h {
	case "localhost", "metadata", "metadata.google.internal", "metadata.google.com":
		return true
	}
	return strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal")
}

func lookupPublicHost(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

func publicIP(ip net.IP) error {
	if ip == nil {
		return errCoverURL
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		return errCoverURL
	}
	if v4 := ip.To4(); v4 != nil && (v4[0] == 0 || (v4[0] == 100 && v4[1]&0xc0 == 64)) {
		return errCoverURL
	}
	return nil
}

func coverContentTypeOK(header string) bool {
	ct := strings.ToLower(strings.TrimSpace(header))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "", "application/octet-stream", "binary/octet-stream",
		"image/png", "image/jpeg", "image/jpg", "image/pjpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
