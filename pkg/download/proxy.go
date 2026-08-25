package download

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// Config controls the package-level download behavior. Zero value is usable
// (Configure fills in defaults for unset fields).
type Config struct {
	// GithubProxy is a prefix-rewrite proxy, e.g. "https://ghproxy.example.com/".
	// Requests to a known GitHub asset host get the original URL appended after
	// this prefix. Empty means download from GitHub directly.
	GithubProxy string
	// HTTPProxy is a standard HTTP(S)_PROXY, applied to every request
	// (GitHub-proxied or not) as a fallback for users behind a corporate/campus
	// gateway with no dedicated GitHub accelerator. Empty means no proxy.
	HTTPProxy string
	// Timeout bounds connection establishment and the wait for response
	// headers — not the body transfer, so it does not need to scale with file
	// size. Zero uses the default.
	Timeout time.Duration
	// Retries is the number of attempts per Fetch call. Zero uses the default.
	Retries int
}

const (
	defaultTimeout = 30 * time.Second
	defaultRetries = 3
)

// githubHosts are the hosts a GitHub Release download can end up on: the
// page itself, and the two hosts release assets redirect to. Missing the
// asset hosts means only the first redirect hop is proxied while the actual
// (large) transfer still goes direct.
var githubHosts = map[string]bool{
	"github.com":                    true,
	"raw.githubusercontent.com":     true,
	"objects.githubusercontent.com": true,
}

var (
	current    atomic.Pointer[Config]
	httpClient atomic.Pointer[http.Client]
)

func init() {
	cfg := Config{Timeout: defaultTimeout, Retries: defaultRetries}
	current.Store(&cfg)
	httpClient.Store(buildClient(&cfg))
}

// Configure sets the global proxy/timeout/retry behavior. Call once at
// startup on both platforms — Syncthing downloads happen on Windows too, not
// just in the Linux runtime-bootstrap path.
func Configure(cfg Config) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.Retries <= 0 {
		cfg.Retries = defaultRetries
	}
	current.Store(&cfg)
	httpClient.Store(buildClient(&cfg))
}

func buildClient(cfg *Config) *http.Client {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: cfg.Timeout}).DialContext,
		ResponseHeaderTimeout: cfg.Timeout,
	}
	if cfg.HTTPProxy != "" {
		if proxyURL, err := url.Parse(cfg.HTTPProxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	// No client.Timeout: it would bound the whole exchange including the
	// response body, which breaks large transfers (GE-Proton is ~450MB).
	return &http.Client{Transport: transport}
}

// rewriteGithubURL prepends the configured GitHub proxy in front of rawURL
// when its host is a known GitHub asset host. Non-GitHub URLs (e.g. the
// Steam CDN) pass through untouched even when a proxy is configured.
func rewriteGithubURL(rawURL, proxy string) string {
	if proxy == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || !githubHosts[u.Hostname()] {
		return rawURL
	}
	return strings.TrimSuffix(proxy, "/") + "/" + rawURL
}
