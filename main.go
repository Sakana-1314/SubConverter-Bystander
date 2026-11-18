// SubConverter-Bystander 把订阅转换请求原样转给转换器，同时并发读取订阅源的
// Subscription-Userinfo 响应头，和转换后的文件一起返回。
//
// 本项目不解析、不生成任何订阅内容。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type config struct {
	listen        string
	converter     *url.URL
	converterCmd  string
	headerTimeout time.Duration
	cacheTTL      time.Duration
	allowPrivate  bool
	fallbackUA    string
	fetchLimit    int
}

func loadConfig() (config, error) {
	raw := env("CONVERTER_URL", "http://127.0.0.1:25500")
	converter, err := url.Parse(raw)
	if err != nil || converter.Host == "" ||
		(converter.Scheme != "http" && converter.Scheme != "https") {
		return config{}, errors.New("CONVERTER_URL 必须是 http(s) 地址")
	}
	cfg := config{
		listen:        env("LISTEN", ":8080"),
		converter:     converter,
		converterCmd:  os.Getenv("CONVERTER_CMD"),
		headerTimeout: envDuration("HEADER_TIMEOUT", 3*time.Second),
		cacheTTL:      envCacheTTL("HEADER_CACHE_TTL", 60*time.Second),
		allowPrivate:  envBool("ALLOW_PRIVATE_TARGETS", false),
		fallbackUA:    env("FALLBACK_UA", "clash.meta"),
		fetchLimit:    envInt("MAX_CONCURRENT_FETCHES", 32),
	}
	if cfg.fetchLimit < 1 {
		cfg.fetchLimit = 1
	}
	return cfg, nil
}

type userinfoKey struct{}

type service struct {
	converter  *url.URL
	proxy      *httputil.ReverseProxy
	client     *http.Client
	fallbackUA string
	cache      *ttlCache
	sem        chan struct{}
}

func newService(cfg config) *service {
	s := &service{
		converter:  cfg.converter,
		fallbackUA: cfg.fallbackUA,
		cache:      newTTLCache(cfg.cacheTTL),
		sem:        make(chan struct{}, cfg.fetchLimit),
		client: &http.Client{
			Timeout: cfg.headerTimeout,
			Transport: &http.Transport{
				Proxy:               nil,
				DialContext:         guardedDialer(cfg.allowPrivate),
				MaxIdleConns:        64,
				MaxIdleConnsPerHost: 4,
				MaxConnsPerHost:     8,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 5 * time.Second,
				ForceAttemptHTTP2:   true,
			},
		},
	}
	s.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(cfg.converter)
			pr.SetXForwarded()
		},
		ModifyResponse: s.modifyResponse,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("转换器不可用：%v", err)
			http.Error(w, "converter unavailable", http.StatusBadGateway)
		},
	}
	return s
}

func (s *service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if target, ua := pickSubscription(r.URL.Query().Get("url")); target != "" {
			ctx := context.WithValue(r.Context(), userinfoKey{}, s.fetchAsync(target, ua))
			r = r.WithContext(ctx)
		}
	}
	s.proxy.ServeHTTP(w, r)
}

func (s *service) modifyResponse(resp *http.Response) error {
	if resp.Request == nil {
		return nil
	}
	ch, _ := resp.Request.Context().Value(userinfoKey{}).(<-chan string)
	if ch == nil || resp.Header.Get("Subscription-Userinfo") != "" {
		return nil
	}
	select {
	case value := <-ch:
		if value != "" {
			resp.Header.Set("Subscription-Userinfo", value)
		}
	case <-resp.Request.Context().Done():
	}
	return nil
}

func (s *service) fetchAsync(target, ua string) <-chan string {
	ch := make(chan string, 1)
	go func() { ch <- s.userinfo(target, ua) }()
	return ch
}

func (s *service) userinfo(target, ua string) string {
	if ua == "" {
		ua = s.fallbackUA
	}
	key := target + "\n" + ua
	if value, ok := s.cache.get(key); ok {
		return value
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.client.Timeout)
	defer cancel()
	value, contacted := s.readHeader(ctx, target, ua, http.MethodHead)
	if value == "" {
		second, secondContacted := s.readHeader(ctx, target, ua, http.MethodGet)
		contacted = contacted || secondContacted
		if second != "" {
			value = second
		}
	}
	if contacted {
		s.cache.set(key, value)
	}
	return value
}

func (s *service) readHeader(ctx context.Context, target, ua, method string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")
	if method == http.MethodGet {
		// 只要响应头；支持 Range 的服务器不会回传正文。
		req.Header.Set("Range", "bytes=0-0")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", false
	}
	resp.Body.Close()
	return strings.TrimSpace(resp.Header.Get("Subscription-Userinfo")), true
}

// pickSubscription 从 url 参数中取第一个 HTTP(S) 订阅地址，并识别 ua: 前缀。
// 参数可能包含多个用 | 分隔的来源，以及 tag:/provider:/interval:/ua:/node: 前缀。
func pickSubscription(raw string) (target, ua string) {
	for _, segment := range strings.Split(raw, "|") {
		var candidate, candidateUA string
		for _, field := range strings.Split(segment, ",") {
			field = strings.TrimSpace(field)
			switch {
			case strings.HasPrefix(field, "ua:"):
				candidateUA = strings.TrimPrefix(field, "ua:")
			case strings.HasPrefix(field, "http://"), strings.HasPrefix(field, "https://"):
				if candidate == "" {
					candidate = field
				}
			}
		}
		if candidate != "" {
			return candidate, candidateUA
		}
	}
	return "", ""
}

func guardedDialer(allowPrivate bool) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if allowPrivate {
		return dialer.DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if blockedIP(ip.IP) {
				continue
			}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("拒绝访问私网或不可用地址：%s", host)
	}
}

func blockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// 100.64.0.0/10：运营商级 NAT，公开部署时同样不应可达。
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return true
	}
	return false
}

type ttlEntry struct {
	value   string
	expires time.Time
}

type ttlCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]ttlEntry
}

func newTTLCache(ttl time.Duration) *ttlCache {
	return &ttlCache{ttl: ttl, m: make(map[string]ttlEntry)}
}

func (c *ttlCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.m[key]
	if !ok || time.Now().After(entry.expires) {
		if ok {
			delete(c.m, key)
		}
		return "", false
	}
	return entry.value, true
}

func (c *ttlCache) set(key, value string) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if len(c.m) >= 4096 {
		for k, entry := range c.m {
			if now.After(entry.expires) {
				delete(c.m, k)
			}
		}
		if len(c.m) >= 4096 {
			c.m = make(map[string]ttlEntry)
		}
	}
	c.m[key] = ttlEntry{value: value, expires: now.Add(c.ttl)}
}

var (
	converterProc *exec.Cmd
	stopping      atomic.Bool
)

func startConverter(cmdline string) {
	cmd := exec.Command("/bin/sh", "-c", cmdline)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatalf("启动内置转换器失败：%v", err)
	}
	converterProc = cmd
	go func() {
		err := cmd.Wait()
		if stopping.Load() {
			return
		}
		log.Printf("内置转换器已退出：%v", err)
		os.Exit(1)
	}()
}

func stopConverter() {
	if converterProc == nil {
		return
	}
	stopping.Store(true)
	_ = converterProc.Process.Kill()
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// 只记录路径，不记录查询串，避免把订阅 token 写进日志。
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func main() {
	log.SetFlags(log.LstdFlags)
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if cfg.converterCmd != "" {
		startConverter(cfg.converterCmd)
	}

	server := &http.Server{
		Addr:              cfg.listen,
		Handler:           logRequests(newService(cfg)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Printf("SubConverter-Bystander 监听 %s，转换器 %s", cfg.listen, cfg.converter.Redacted())
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Print("正在退出")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
	stopConverter()
}

func env(key, def string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			return parsed
		}
		log.Printf("忽略无效的 %s=%q，使用默认值 %s", key, value, def)
	}
	return def
}

// envCacheTTL 允许显式设为 0，表示关闭缓存。
func envCacheTTL(key string, def time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed >= 0 {
			return parsed
		}
		log.Printf("忽略无效的 %s=%q，使用默认值 %s", key, value, def)
	}
	return def
}

func envBool(key string, def bool) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(key string, def int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
		log.Printf("忽略无效的 %s=%q，使用默认值 %d", key, value, def)
	}
	return def
}
