package api

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/albit/umbreltunnel-app/internal/config"
	"github.com/albit/umbreltunnel-app/internal/vps"
	"github.com/albit/umbreltunnel-app/internal/wireguard"
)

type Server struct {
	cfg       *config.Config
	wgMgr     *wireguard.Manager
	vpsClient *vps.Client
	webFS     embed.FS
	prefix    string
	tunnels   []TunnelEntry
	statePath string
	vpsURL    string
	vpsAPIKey string

	fwMu   sync.Mutex
	fwd    map[string]*forwarder
}

type TunnelEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	LocalPort int    `json:"localPort"`
	LocalHost string `json:"localHost"`
	PublicURL string `json:"publicUrl"`
	CreatedAt string `json:"createdAt"`
}

type appState struct {
	Tunnels []TunnelEntry `json:"tunnels"`
	VpsURL  string        `json:"vpsUrl"`
	VpsKey  string        `json:"vpsApiKey"`
}

type forwarder struct {
	listener net.Listener
	port     int
	done     chan struct{}
}

func NewServer(cfg *config.Config, wgMgr *wireguard.Manager) *Server {
	s := &Server{
		cfg:       cfg,
		wgMgr:     wgMgr,
		statePath: filepath.Join(cfg.DataDir, "app-state.json"),
		fwd:       make(map[string]*forwarder),
	}
	s.loadState()
	return s
}

func (s *Server) SetVPSClient(c *vps.Client) {
	s.vpsClient = c
}

func (s *Server) SetWebFS(fs embed.FS, prefix string) {
	s.webFS = fs
	s.prefix = prefix
}

func (s *Server) saveState() {
	st := appState{
		Tunnels: s.tunnels,
		VpsURL:  s.vpsURL,
		VpsKey:  s.vpsAPIKey,
	}
	data, _ := json.MarshalIndent(st, "", "  ")
	os.WriteFile(s.statePath, data, 0600)
}

func (s *Server) loadState() {
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return
	}
	var st appState
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}
	if st.Tunnels != nil {
		s.tunnels = st.Tunnels
	}
	s.vpsURL = st.VpsURL
	s.vpsAPIKey = st.VpsKey
	if s.vpsURL != "" {
		s.vpsClient = vps.NewClient(s.vpsURL, s.vpsAPIKey)
	}
}

func (s *Server) RestoreForwarders() {
	wgIP := s.wgMgr.GetClientIPFromConfig()
	if wgIP == "" {
		return
	}
	for _, t := range s.tunnels {
		if err := s.startForwarder(wgIP, t.LocalPort); err != nil {
			fmt.Fprintf(os.Stderr, "restore forwarder %d: %v\n", t.LocalPort, err)
		}
	}
}

func (s *Server) getTargetHost() string {
	if addrs, err := net.LookupHost("umbrel.local"); err == nil && len(addrs) > 0 {
		return addrs[0]
	}
	return "172.17.0.1"
}

func (s *Server) startForwarder(wgIP string, localPort int) error {
	key := net.JoinHostPort(wgIP, strconv.Itoa(localPort))

	s.fwMu.Lock()
	if _, exists := s.fwd[key]; exists {
		s.fwMu.Unlock()
		return nil
	}
	s.fwMu.Unlock()

	targetHost := s.getTargetHost()
	target := net.JoinHostPort(targetHost, strconv.Itoa(localPort))

	l, err := net.Listen("tcp", ":"+strconv.Itoa(localPort))
	if err != nil {
		return fmt.Errorf("listen :%d: %w", localPort, err)
	}

	f := &forwarder{
		listener: l,
		port:     localPort,
		done:     make(chan struct{}),
	}

	s.fwMu.Lock()
	s.fwd[key] = f
	s.fwMu.Unlock()

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				select {
				case <-f.done:
					return
				default:
					continue
				}
			}
			go func() {
				defer conn.Close()
				dst, err := net.DialTimeout("tcp", target, 10*time.Second)
				if err != nil {
					return
				}
				defer dst.Close()
				var wg sync.WaitGroup
				wg.Add(2)
				go func() { ioCopy(dst, conn); wg.Done() }()
				go func() { ioCopy(conn, dst); wg.Done() }()
				wg.Wait()
			}()
		}
	}()

	return nil
}

func (s *Server) stopForwarder(wgIP string, localPort int) {
	key := net.JoinHostPort(wgIP, strconv.Itoa(localPort))
	s.fwMu.Lock()
	f, ok := s.fwd[key]
	delete(s.fwd, key)
	s.fwMu.Unlock()
	if ok {
		close(f.done)
		f.listener.Close()
	}
}

func ioCopy(dst io.Writer, src io.Reader) {
	buf := make([]byte, 32768)
	io.CopyBuffer(dst, src, buf)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/status", s.basicAuth(s.handleStatus))
	mux.HandleFunc("/api/setup", s.basicAuth(s.handleSetup))
	mux.HandleFunc("/api/wg/key", s.basicAuth(s.handleWGKey))
	mux.HandleFunc("/api/wg/config", s.basicAuth(s.handleWGConfig))
	mux.HandleFunc("/api/wg/connect", s.basicAuth(s.handleWGConnect))
	mux.HandleFunc("/api/wg/status", s.basicAuth(s.handleWGStatus))
	mux.HandleFunc("/api/vps/register", s.basicAuth(s.handleVPSRegister))
	mux.HandleFunc("/api/vps/check", s.basicAuth(s.handleVPSCheck))
	mux.HandleFunc("/api/tunnels", s.basicAuth(s.handleTunnels))
	mux.HandleFunc("/api/tunnels/", s.basicAuth(s.handleTunnelByID))

	if s.webFS != (embed.FS{}) {
		webRoot, _ := fs.Sub(s.webFS, s.prefix)
		mux.Handle("/", s.basicAuth(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" || r.URL.Path == "" {
				r.URL.Path = "/index.html"
			}
			name := strings.TrimPrefix(r.URL.Path, "/")
			data, err := fs.ReadFile(webRoot, name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			contentType := "text/plain"
			if strings.HasSuffix(name, ".html") {
				contentType = "text/html; charset=utf-8"
			} else if strings.HasSuffix(name, ".svg") {
				contentType = "image/svg+xml"
			} else if strings.HasSuffix(name, ".css") {
				contentType = "text/css"
			} else if strings.HasSuffix(name, ".js") {
				contentType = "application/javascript"
			} else if strings.HasSuffix(name, ".json") {
				contentType = "application/json"
			} else if strings.HasSuffix(name, ".png") {
				contentType = "image/png"
			}
			w.Header().Set("Content-Type", contentType)
			w.Write(data)
		}))
	}

	return mux
}

func (s *Server) basicAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.UIAuthUser == "" {
			next(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.cfg.UIAuthUser || pass != s.cfg.UIAuthPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="umbreltunnel"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) json(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) error(w http.ResponseWriter, status int, msg string) {
	s.json(w, status, map[string]string{"error": msg})
}
