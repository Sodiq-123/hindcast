package market

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"sync"
	"time"
)

const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"

// ErrRateLimited is returned when a provider keeps answering 429.
var ErrRateLimited = errors.New("rate limited by provider")

// ErrNotFound means the provider does not know the symbol.
var ErrNotFound = errors.New("symbol not found")

// PoliteClient is the exported form for other packages that call free
// public APIs (earnings calendars) and need the same politeness.
type PoliteClient = politeClient

// NewPoliteClient returns a client that spaces requests to each host by minGap.
//
// It speaks HTTP/1.1 so concurrent workers get separate connections: over
// HTTP/2 every worker shares one connection, and a single stalled connection
// (seen with Nasdaq after sustained use) freezes them all.
func NewPoliteClient(minGap time.Duration) *PoliteClient {
	c := newPoliteClient(minGap)
	c.http.Transport = &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return c
}

// Get fetches url with retries and backoff.
func (p *politeClient) Get(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	return p.get(ctx, url, headers)
}

// politeClient wraps http.Client with a per-host minimum spacing between
// requests and exponential backoff on 429/5xx.
type politeClient struct {
	http     *http.Client
	minGap   time.Duration
	mu       sync.Mutex
	lastCall map[string]time.Time
}

func newPoliteClient(minGap time.Duration) *politeClient {
	jar, _ := cookiejar.New(nil)
	return &politeClient{
		http:     &http.Client{Timeout: 30 * time.Second, Jar: jar},
		minGap:   minGap,
		lastCall: map[string]time.Time{},
	}
}

func (p *politeClient) wait(ctx context.Context, host string) error {
	p.mu.Lock()
	next := p.lastCall[host].Add(p.minGap)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	p.lastCall[host] = next
	p.mu.Unlock()
	select {
	case <-time.After(time.Until(next)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *politeClient) get(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		if err := p.wait(ctx, req.URL.Host); err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", browserUA)
		req.Header.Set("Accept", "application/json, text/xml, */*")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := p.http.Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, rerr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
			resp.Body.Close()
			switch {
			case rerr != nil:
				lastErr = rerr
			case resp.StatusCode == http.StatusOK:
				return body, nil
			case resp.StatusCode == http.StatusNotFound:
				return nil, ErrNotFound
			case resp.StatusCode == http.StatusTooManyRequests:
				lastErr = ErrRateLimited
				if ra, _ := strconv.Atoi(resp.Header.Get("Retry-After")); ra > 0 && ra < 60 {
					sleepCtx(ctx, time.Duration(ra)*time.Second)
					continue
				}
			case resp.StatusCode >= 500:
				lastErr = fmt.Errorf("provider error %d", resp.StatusCode)
			default:
				return nil, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(body), 200))
			}
		}
		// Retry on a fresh connection: a wedged keep-alive connection would
		// otherwise fail every retry the same way.
		p.http.CloseIdleConnections()
		backoff := time.Duration(1<<attempt)*time.Second + time.Duration(rand.IntN(700))*time.Millisecond
		if err := sleepCtx(ctx, backoff); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
