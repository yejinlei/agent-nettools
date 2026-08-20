package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"github.com/spf13/cobra"
)

func corsproxyCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use: "corsproxy",
		Short: "CORS proxy",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCORSProxy(cmd.Context(), port)
		},
	}
	cmd.Flags().IntVar(&port, "port", 8080, "listen port")
	return cmd
}

func runCORSProxy(ctx context.Context, port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil { return err }
	fmt.Printf("corsproxy: listening on %s\n", ln.Addr())
	srv := &http.Server{Handler: corsMux()}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case <-ctx.Done(): srv.Close(); return nil
	case err := <-errCh:
		if err == http.ErrServerClosed { return nil }
		return err
	}
}

func corsMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy", handleCorsProxy)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<h1>CORS Proxy</h1><p>GET /proxy?url=TARGET</p>")
	})
	return mux
}

func handleCorsProxy(w http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("url")
	if targetURL == "" && r.Method == "POST" {
		var req struct{ URL string `json:"url"` }
		if json.NewDecoder(r.Body).Decode(&req) == nil { targetURL = req.URL }
	}
	if targetURL == "" { http.Error(w, "Missing url", 400); return }
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		http.Error(w, "Invalid URL", 400); return
	}
	target, err := url.Parse(targetURL)
	if err != nil { http.Error(w, "Invalid URL", 400); return }
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(req *http.Request) {
		req.URL = target; req.Host = target.Host
		req.Header.Set("X-Forwarded-For", r.RemoteAddr)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("Access-Control-Allow-Origin")
		resp.Header.Set("Access-Control-Allow-Origin", "*")
		resp.Header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		resp.Header.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		return nil
	}
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(204); return
	}
	proxy.ServeHTTP(w, r)
}
