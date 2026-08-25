package download

import "testing"

func TestRewriteGithubURL(t *testing.T) {
	const proxy = "https://ghproxy.example.com/"

	cases := []struct {
		name  string
		url   string
		proxy string
		want  string
	}{
		{
			name:  "empty proxy passes through",
			url:   "https://github.com/foo/bar/releases/download/v1/asset.tar.gz",
			proxy: "",
			want:  "https://github.com/foo/bar/releases/download/v1/asset.tar.gz",
		},
		{
			name:  "github.com is rewritten",
			url:   "https://github.com/foo/bar/releases/download/v1/asset.tar.gz",
			proxy: proxy,
			want:  proxy + "https://github.com/foo/bar/releases/download/v1/asset.tar.gz",
		},
		{
			name:  "release asset redirect host is rewritten",
			url:   "https://objects.githubusercontent.com/abc123",
			proxy: proxy,
			want:  proxy + "https://objects.githubusercontent.com/abc123",
		},
		{
			name:  "raw content host is rewritten",
			url:   "https://raw.githubusercontent.com/foo/bar/main/file.txt",
			proxy: proxy,
			want:  proxy + "https://raw.githubusercontent.com/foo/bar/main/file.txt",
		},
		{
			name:  "non-github host passes through even with proxy configured",
			url:   "https://steamcdn-a.akamaihd.net/client/installer/steamcmd.zip",
			proxy: proxy,
			want:  "https://steamcdn-a.akamaihd.net/client/installer/steamcmd.zip",
		},
		{
			name:  "proxy without trailing slash is normalized",
			url:   "https://github.com/foo/bar",
			proxy: "https://ghproxy.example.com",
			want:  "https://ghproxy.example.com/https://github.com/foo/bar",
		},
		{
			name:  "unparseable url passes through unchanged",
			url:   "http://%zz",
			proxy: proxy,
			want:  "http://%zz",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rewriteGithubURL(tc.url, tc.proxy)
			if got != tc.want {
				t.Errorf("rewriteGithubURL(%q, %q) = %q, want %q", tc.url, tc.proxy, got, tc.want)
			}
		})
	}
}

func TestConfigureAppliesDefaults(t *testing.T) {
	t.Cleanup(func() { Configure(Config{}) })

	Configure(Config{})
	cfg := current.Load()
	if cfg.Timeout != defaultTimeout {
		t.Errorf("Timeout = %v, want default %v", cfg.Timeout, defaultTimeout)
	}
	if cfg.Retries != defaultRetries {
		t.Errorf("Retries = %d, want default %d", cfg.Retries, defaultRetries)
	}
}
