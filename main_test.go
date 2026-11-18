package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

const providerUserinfo = "upload=1048576; download=5242880; total=107374182400; expire=1900000000"

func TestPickSubscription(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantURL string
		wantUA  string
	}{
		{"plain", "https://airport.example/sub?token=1", "https://airport.example/sub?token=1", ""},
		{"tag", "tag:主订阅,https://airport.example/sub", "https://airport.example/sub", ""},
		{"ua", "ua:clash-verge/v2.5.8,https://airport.example/sub", "https://airport.example/sub", "clash-verge/v2.5.8"},
		{"skip node link", "node:ss://YWJj@1.2.3.4:443,http://airport.example/sub", "http://airport.example/sub", ""},
		{"second source", "ss://node|tag:x,provider:p,interval:3600,https://airport.example/sub", "https://airport.example/sub", ""},
		{"no http source", "ss://node|node:trojan://x", "", ""},
		{"empty", "", "", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotURL, gotUA := pickSubscription(test.raw)
			if gotURL != test.wantURL || gotUA != test.wantUA {
				t.Fatalf("pickSubscription(%q) = (%q, %q)，期望 (%q, %q)",
					test.raw, gotURL, gotUA, test.wantURL, test.wantUA)
			}
		})
	}
}

func TestBlockedIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.1.1", "100.64.0.1", "::1", "fd00::1"}
	allowed := []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"}
	for _, raw := range blocked {
		if !blockedIP(mustIP(t, raw)) {
			t.Errorf("%s 应被拦截", raw)
		}
	}
	for _, raw := range allowed {
		if blockedIP(mustIP(t, raw)) {
			t.Errorf("%s 不应被拦截", raw)
		}
	}
}

func TestServiceInjectsUserinfo(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", providerUserinfo)
		_, _ = w.Write([]byte("proxies: []\n"))
	}))
	defer provider.Close()

	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sub" {
			t.Errorf("转发路径 = %q，期望 /sub", r.URL.Path)
		}
		if got := r.URL.Query().Get("target"); got != "clash" {
			t.Errorf("target = %q，期望 clash", got)
		}
		if got := r.URL.Query().Get("url"); got != provider.URL {
			t.Errorf("url = %q，期望 %q", got, provider.URL)
		}
		w.Header().Set("profile-update-interval", "24")
		_, _ = w.Write([]byte("proxy-providers:\n"))
	}))
	defer converter.Close()

	svc := newService(testConfig(t, converter.URL))
	rec := serve(t, svc, "/sub?target=clash&url="+url.QueryEscape(provider.URL))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d，期望 200", rec.Code)
	}
	if got := rec.Header().Get("Subscription-Userinfo"); got != providerUserinfo {
		t.Errorf("Subscription-Userinfo = %q，期望 %q", got, providerUserinfo)
	}
	if got := rec.Header().Get("profile-update-interval"); got != "24" {
		t.Errorf("profile-update-interval = %q，期望 24", got)
	}
	if got := rec.Body.String(); got != "proxy-providers:\n" {
		t.Errorf("body = %q", got)
	}
}

func TestServiceKeepsConverterUserinfo(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "from=provider")
	}))
	defer provider.Close()

	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "from=converter")
	}))
	defer converter.Close()

	svc := newService(testConfig(t, converter.URL))
	rec := serve(t, svc, "/sub?target=ss&url="+url.QueryEscape(provider.URL))

	if got := rec.Header().Get("Subscription-Userinfo"); got != "from=converter" {
		t.Errorf("Subscription-Userinfo = %q，转换器已返回时不应被覆盖", got)
	}
}

func TestServiceProviderFailureIsSilent(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer provider.Close()

	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("converted\n"))
	}))
	defer converter.Close()

	svc := newService(testConfig(t, converter.URL))
	rec := serve(t, svc, "/sub?target=clash&url="+url.QueryEscape(provider.URL))

	if rec.Code != http.StatusOK || rec.Body.String() != "converted\n" {
		t.Fatalf("转换结果被影响：status=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Subscription-Userinfo"); got != "" {
		t.Errorf("不应注入空头，得到 %q", got)
	}
}

func TestServiceCachesUserinfo(t *testing.T) {
	var hits atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Subscription-Userinfo", providerUserinfo)
	}))
	defer provider.Close()

	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer converter.Close()

	cfg := testConfig(t, converter.URL)
	cfg.cacheTTL = time.Minute
	svc := newService(cfg)
	target := "/sub?target=clash&url=" + url.QueryEscape(provider.URL)

	serve(t, svc, target)
	serve(t, svc, target)
	if hits.Load() != 1 {
		t.Errorf("订阅源被请求 %d 次，期望缓存后只请求 1 次", hits.Load())
	}
}

func TestHealthzDoesNotNeedConverter(t *testing.T) {
	cfg := testConfig(t, "http://127.0.0.1:1")
	svc := newService(cfg)
	rec := serve(t, svc, "/healthz")
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}

func testConfig(t *testing.T, converter string) config {
	t.Helper()
	parsed, err := url.Parse(converter)
	if err != nil {
		t.Fatal(err)
	}
	return config{
		converter:     parsed,
		headerTimeout: 2 * time.Second,
		cacheTTL:      0,
		allowPrivate:  true,
		fallbackUA:    "clash.meta",
		fetchLimit:    4,
	}
}

func serve(t *testing.T, svc *service, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func mustIP(t *testing.T, raw string) net.IP {
	t.Helper()
	parsed := net.ParseIP(raw)
	if parsed == nil {
		t.Fatalf("无效 IP：%s", raw)
	}
	return parsed
}
