// cpa-operator is a standard-library-only companion to native CLIProxyAPI.
// It has no Manager database, collector, auto-disable policy, or OAuth store.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/operator"
)

func required(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatal("missing ", name)
	}
	return v
}
func secret(path string) string {
	b, e := os.ReadFile(path)
	if e != nil {
		log.Fatal("cannot read secret file")
	}
	v := strings.TrimSpace(string(b))
	if len(v) < 10 {
		log.Fatal("secret too short")
	}
	return v
}
func loopback(addr string) {
	host, _, e := net.SplitHostPort(addr)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("listeners and upstream must use loopback IPs")
	}
}
func main() {
	upstream := required("CPA_URL")
	u, e := url.Parse(upstream)
	if e != nil || u.Scheme != "http" || u.User != nil {
		log.Fatal("invalid CPA URL")
	}
	loopback(u.Host)
	adminAddr, publicAddr := required("CPA_ADMIN_ADDR"), required("CPA_OPERATOR_ADDR")
	loopback(adminAddr)
	loopback(publicAddr)
	key := secret(required("CPA_KEY_FILE"))
	password := secret(required("CPA_OPERATOR_PASSWORD_FILE"))
	s, e := operator.New(func(context.Context) (operator.Connection, error) {
		return operator.Connection{URL: upstream, Key: key}, nil
	}, required("CPA_ACTIVATION_FILE"))
	if e != nil {
		log.Fatal(e)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	s.Start(ctx)
	admin := http.NewServeMux()
	admin.Handle("/activation/", s.AdminHandler(func(_ context.Context, auth string) (bool, error) {
		got := sha256.Sum256([]byte(strings.TrimPrefix(auth, "Bearer ")))
		want := sha256.Sum256([]byte(key))
		return strings.HasPrefix(auth, "Bearer ") && subtle.ConstantTimeCompare(got[:], want[:]) == 1, nil
	}))
	proxy := httputil.NewSingleHostReverseProxy(u)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Header["X-Forwarded-For"] = nil
		r.Header.Del("Forwarded")
		r.Header.Del("X-Real-IP")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) { http.Error(w, "CPA unavailable", 502) }
	admin.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/management.html", 302)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	servers := []*http.Server{{Addr: adminAddr, Handler: admin, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}, {Addr: publicAddr, Handler: s.PublicHandler(password), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}}
	for _, server := range servers {
		go func(server *http.Server) {
			if e := server.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
				log.Print("listener stopped")
				cancel()
			}
		}(server)
	}
	<-ctx.Done()
	shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	for _, server := range servers {
		_ = server.Shutdown(shutdown)
	}
}
