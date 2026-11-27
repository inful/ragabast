// Package webfetch downloads a page and converts it to markdown,
// under a guard that keeps the fetch from reaching anything the
// operator did not ask for.
//
// The guard exists because the URL list that drives this feature is
// a file an operator edits — but a typo, a redirect, or a hostile
// DNS answer should never turn a documentation server into a
// request proxy for the private network it sits in.
//
// Three rules:
//
//   - Only http and https.
//   - Only public addresses. The host is resolved before the
//     request and every resolved address is checked; the connection
//     is then made to the checked address, so a second DNS answer
//     (rebinding) cannot swap in a private one.
//   - Bounded work: a response size cap, a timeout, and a redirect
//     cap.
//
// The fetch is never recursive: the links inside a fetched page are
// converted to markdown, never fetched.
package webfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/PuerkitoBio/goquery"
)

// Policy bounds what a single fetch may do. The zero value is
// usable: it means "no timeout beyond the context's", a 4 MiB
// response cap, and 3 redirects.
type Policy struct {
	// Timeout bounds the whole fetch (connect + read). Zero means
	// the caller's context deadline is the only bound.
	Timeout time.Duration
	// MaxBytes caps the response body. Zero means DefaultMaxBytes.
	MaxBytes int64
	// MaxRedirects caps the redirect chain. Zero means
	// DefaultMaxRedirects.
	MaxRedirects int

	// dialContext overrides the connection. It exists because the
	// guard refuses loopback addresses, so a test server on
	// 127.0.0.1 is unreachable without it. Production code leaves
	// it nil and gets the guarded dialer.
	dialContext func(ctx context.Context, network, addr string) (net.Conn, error)
}

// dialer returns the dialer the fetch uses: the test override
// when one is set, the guarded one otherwise.
func (p Policy) dialer(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if p.dialContext != nil {
		return p.dialContext
	}
	return guardedDialer(timeout)
}

// Defaults for the zero-value policy.
const (
	DefaultMaxBytes     int64 = 4 << 20 // 4 MiB
	DefaultMaxRedirects       = 3
)

// userAgent identifies the fetcher to the servers it reads. A
// descriptive agent is better than a blank one: operators reading
// the other side's logs can tell what hit them.
const userAgent = "ragabast-web-mirror/1.0 (+https://github.com/inful/ragabast)"

// Result is what a successful fetch produced.
type Result struct {
	// Markdown is the page's main content, converted.
	Markdown string
	// Title is the page's <title>, when it has one.
	Title string
	// FinalURL is the URL the content actually came from, after
	// redirects.
	FinalURL string
	// StatusCode is the status of the final response.
	StatusCode int
}

// ErrNotHTML is returned when the server answers with something
// that is not a web page (a PDF, a JSON API, a plain-text file).
var ErrNotHTML = errors.New("webfetch: response is not HTML")

// Fetch downloads rawURL and converts the page to markdown.
//
// The returned error is safe to show an operator: it names the URL
// and the reason (blocked address, wrong content type, too large,
// timed out) without any of the response body.
func Fetch(ctx context.Context, rawURL string, policy Policy) (*Result, error) {
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("webfetch: invalid URL %q: %w", rawURL, err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("webfetch: unsupported scheme %q in %q (only http and https)", target.Scheme, rawURL)
	}
	if target.Host == "" {
		return nil, fmt.Errorf("webfetch: URL %q has no host", rawURL)
	}

	if policy.MaxBytes <= 0 {
		policy.MaxBytes = DefaultMaxBytes
	}
	if policy.MaxRedirects <= 0 {
		policy.MaxRedirects = DefaultMaxRedirects
	}

	if policy.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, policy.Timeout)
		defer cancel()
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: policy.dialer(policy.Timeout),
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= policy.MaxRedirects {
				return fmt.Errorf("webfetch: stopped after %d redirects", policy.MaxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("webfetch: redirect to unsupported scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("webfetch: cannot build request for %q: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webfetch: %q: %w", rawURL, err)
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("webfetch: %q returned status %d", rawURL, resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" && !isHTMLContentType(ct) {
		return nil, fmt.Errorf("webfetch: %q returned content type %q: %w", rawURL, ct, ErrNotHTML)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, policy.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("webfetch: cannot read %q: %w", rawURL, err)
	}
	if int64(len(body)) > policy.MaxBytes {
		return nil, fmt.Errorf("webfetch: %q is larger than the %d byte cap", rawURL, policy.MaxBytes)
	}

	markdown, title, err := convert(body)
	if err != nil {
		return nil, fmt.Errorf("webfetch: cannot convert %q: %w", rawURL, err)
	}

	return &Result{
		Markdown:   markdown,
		Title:      title,
		FinalURL:   resp.Request.URL.String(),
		StatusCode: resp.StatusCode,
	}, nil
}

// convert turns an HTML page into markdown, keeping the main
// content and dropping the furniture (navigation, footers, scripts).
func convert(body []byte) (markdown string, title string, err error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return "", "", fmt.Errorf("parse HTML: %w", err)
	}

	title = strings.TrimSpace(doc.Find("title").First().Text())

	// The main content, when the page marks it up. Sites that
	// do not are served the whole body minus the furniture.
	selection := doc.Find("main").First()
	if selection.Length() == 0 {
		selection = doc.Find("article").First()
	}
	if selection.Length() == 0 {
		selection = doc.Find("body").First()
	}
	if selection.Length() == 0 {
		selection = doc.Selection
	}

	// Drop the parts that are never content.
	selection.Find("script, style, noscript, nav, footer, aside, form, iframe, svg").Remove()

	converter := md.NewConverter("", true, &md.Options{
		HeadingStyle:   "atx",
		CodeBlockStyle: "fenced",
		LinkStyle:      "inlined",
	})
	out := converter.Convert(selection)

	return strings.TrimSpace(out), title, nil
}

// isHTMLContentType reports whether a Content-Type header names a
// web page. An empty header is accepted — some servers omit it.
func isHTMLContentType(contentType string) bool {
	media := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch media {
	case "", "text/html", "application/xhtml+xml":
		return true
	}
	return false
}

// guardedDialer returns a dialer that only connects to public
// addresses.
//
// The host is resolved here, every answer is checked, and the
// connection is made to the checked address. Pinning the address
// matters: resolving twice (once to check, once to connect) leaves
// a window where the second answer can be a private one.
func guardedDialer(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("webfetch: cannot parse address %q: %w", addr, err)
		}

		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("webfetch: cannot resolve %q: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("webfetch: %q resolved to no addresses", host)
		}
		for _, ip := range ips {
			if !isPublicIP(ip.IP) {
				return nil, fmt.Errorf("webfetch: refusing to connect to %q: %s is a private or reserved address", host, ip.IP)
			}
		}

		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

// isPublicIP reports whether an address is routable on the public
// internet. Everything else — loopback, private ranges, link-local
// (including the cloud metadata address), unique-local, multicast,
// unspecified — is refused.
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	// Unique-local IPv6 (fc00::/7) is not covered by IsPrivate
	// on every Go version, so check it explicitly.
	if len(ip) == net.IPv6len && ip.To4() == nil && (ip[0]&0xfe) == 0xfc {
		return false
	}
	return true
}
