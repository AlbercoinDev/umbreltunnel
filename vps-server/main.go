package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	ListenAddr string
	Domain     string
	WGSubnet   string
	CaddyAdmin string
	DataDir    string
	APIKey     string
}

type Client struct {
	ID        string    `json:"id"`
	PublicKey string    `json:"publicKey"`
	IP        string    `json:"ip"`
	APIKey    string    `json:"apiKey"`
	CreatedAt time.Time `json:"createdAt"`
}

type Tunnel struct {
	ID        string    `json:"id"`
	ClientID  string    `json:"clientId"`
	Name      string    `json:"name"`
	LocalPort int       `json:"localPort"`
	ClientIP  string    `json:"clientIP"`
	Subdomain string    `json:"subdomain"`
	PublicURL string    `json:"publicUrl"`
	CreatedAt time.Time `json:"createdAt"`
}

type Server struct {
	cfg     Config
	clients map[string]*Client
	tunnels map[string]*Tunnel
	mu      sync.RWMutex
	nextIP  int
}

func loadConfig() Config {
	return Config{
		ListenAddr: getEnv("LISTEN_ADDR", ":8081"),
		Domain:     getEnv("DOMAIN", "localhost"),
		WGSubnet:   getEnv("WG_SUBNET", "10.10.10.0/24"),
		CaddyAdmin: getEnv("CADDY_ADMIN", "http://caddy:2019"),
		DataDir:    getEnv("DATA_DIR", "/data"),
		APIKey:     getEnv("API_KEY", ""),
	}
}

func main() {
	cfg := loadConfig()
	os.MkdirAll(cfg.DataDir, 0700)

	srv := &Server{
		cfg:     cfg,
		clients: make(map[string]*Client),
		tunnels: make(map[string]*Tunnel),
		nextIP:  2,
	}
	srv.loadState()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", srv.handleHealth)
	mux.HandleFunc("/api/register", srv.handleRegister)
	mux.HandleFunc("/api/tunnels", srv.auth(srv.handleTunnels))
	mux.HandleFunc("/api/tunnels/", srv.auth(srv.handleTunnelByID))

	log.Printf("VPS Server listening on %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.json(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		PublicKey string `json:"publicKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.PublicKey == "" {
		s.error(w, http.StatusBadRequest, "publicKey required")
		return
	}

	s.mu.Lock()
	clientIP := fmt.Sprintf("10.10.10.%d", s.nextIP)
	s.nextIP++
	apiKey := generateAPIKey()
	id := generateID()

	client := &Client{
		ID:        id,
		PublicKey: req.PublicKey,
		IP:        clientIP,
		APIKey:    apiKey,
		CreatedAt: time.Now(),
	}
	s.clients[id] = client
	s.saveState()
	s.mu.Unlock()

	s.addWGPeer(client)

	serverPubKey := s.getServerPublicKey()
	endpoint := s.cfg.Domain + ":51820"

	configContent := fmt.Sprintf(`[Interface]
PrivateKey = <inserta-tu-clave-privada-aqui>
Address = %s/32
DNS = 1.1.1.1

[Peer]
PublicKey = %s
Endpoint = %s
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 25
`, clientIP, serverPubKey, endpoint)

	s.json(w, http.StatusCreated, map[string]interface{}{
		"id":            id,
		"clientIP":      clientIP,
		"clientAddress": clientIP + "/32",
		"endpoint":      endpoint,
		"dns":           "1.1.1.1",
		"allowedIPs":    "0.0.0.0/0",
		"keepalive":     25,
		"configContent": configContent,
		"apiKey":        apiKey,
	})
}

func (s *Server) handleTunnels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		result := make([]Tunnel, 0, len(s.tunnels))
		for _, t := range s.tunnels {
			result = append(result, *t)
		}
		s.mu.RUnlock()
		s.json(w, http.StatusOK, result)

	case http.MethodPost:
		var req struct {
			Name      string `json:"name"`
			LocalPort int    `json:"localPort"`
			ClientIP  string `json:"clientIp"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.error(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name == "" || req.ClientIP == "" {
			s.error(w, http.StatusBadRequest, "name and clientIp required")
			return
		}
		if req.LocalPort < 1 || req.LocalPort > 65535 {
			s.error(w, http.StatusBadRequest, "port must be 1-65535")
			return
		}

		subdomain := strings.ToLower(strings.ReplaceAll(req.Name, " ", "-"))
		publicURL := fmt.Sprintf("https://%s.%s", subdomain, s.cfg.Domain)
		id := generateID()

		tunnel := &Tunnel{
			ID:        id,
			ClientID:  r.Header.Get("X-Client-ID"),
			Name:      req.Name,
			LocalPort: req.LocalPort,
			ClientIP:  req.ClientIP,
			Subdomain: subdomain,
			PublicURL: publicURL,
			CreatedAt: time.Now(),
		}

		s.mu.Lock()
		s.tunnels[id] = tunnel
		s.saveState()
		s.mu.Unlock()

		s.configureCaddy(tunnel)

		s.json(w, http.StatusCreated, tunnel)

	default:
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleTunnelByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")

	switch r.Method {
	case http.MethodDelete:
		s.mu.Lock()
		tunnel, ok := s.tunnels[id]
		if !ok {
			s.mu.Unlock()
			s.error(w, http.StatusNotFound, "tunnel not found")
			return
		}
		delete(s.tunnels, id)
		s.saveState()
		s.mu.Unlock()

		s.removeCaddy(tunnel)
		s.json(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// Check global master key
		if s.cfg.APIKey != "" && key == s.cfg.APIKey {
			next(w, r)
			return
		}

		// Check per-client API keys
		s.mu.RLock()
		for _, client := range s.clients {
			if client.APIKey == key {
				r.Header.Set("X-Authenticated-Client-ID", client.ID)
				s.mu.RUnlock()
				next(w, r)
				return
			}
		}
		s.mu.RUnlock()

		http.Error(w, "invalid api key", http.StatusForbidden)
	}
}

func (s *Server) addWGPeer(client *Client) {
	log.Printf("Adding WireGuard peer: %s -> IP %s", client.PublicKey[:16], client.IP)

	args := []string{"exec", "umbreltunnel-wg", "wg", "set", "wg0", "peer", client.PublicKey, "allowed-ips", client.IP + "/32"}
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ERROR adding WireGuard peer (trying direct wg): %v", err)
		cmd := exec.Command("wg", "set", "wg0", "peer", client.PublicKey, "allowed-ips", client.IP+"/32")
		out, err = cmd.CombinedOutput()
		if err != nil {
			log.Printf("ERROR adding WireGuard peer directly: %v\nOutput: %s", err, string(out))
			return
		}
	}

	exec.Command("docker", "exec", "umbreltunnel-wg", "wg-quick", "save", "wg0").Run()
	log.Printf("WireGuard peer added: %s -> %s/32", client.PublicKey[:16], client.IP)
}

func (s *Server) getServerPublicKey() string {
	paths := []string{"/config/server/publickey", "/etc/wireguard/server.public", "/data/server.public"}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	key, _ := generateKey()
	log.Printf("WARNING: server public key not found, generated temporary: %s", key)
	return key
}

func (s *Server) configureCaddy(tunnel *Tunnel) {
	if s.cfg.CaddyAdmin == "" {
		return
	}

	host := fmt.Sprintf("%s.%s", tunnel.Subdomain, s.cfg.Domain)
	dial := fmt.Sprintf("%s:%d", tunnel.ClientIP, tunnel.LocalPort)

	config := map[string]interface{}{
		"@id": tunnel.ID,
		"match": []map[string]interface{}{
			{"host": []string{host}},
		},
		"handle": []map[string]interface{}{
			{
				"handler": "reverse_proxy",
				"upstreams": []map[string]interface{}{
					{"dial": dial},
				},
			},
		},
		"terminal": true,
	}

	body, _ := json.Marshal(config)
	url := fmt.Sprintf("%s/config/apps/http/servers/srv0/routes/", s.cfg.CaddyAdmin)

	resp, err := http.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		log.Printf("Caddy API error: %v", err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 300 {
		log.Printf("Caddy API status: %d", resp.StatusCode)
		return
	}

	log.Printf("Caddy configured: %s -> %s", host, dial)
}

func (s *Server) removeCaddy(tunnel *Tunnel) {
	if s.cfg.CaddyAdmin == "" {
		return
	}
	url := fmt.Sprintf("%s/id/%s", s.cfg.CaddyAdmin, tunnel.ID)
	req, _ := http.NewRequest("DELETE", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Caddy remove error: %v", err)
		return
	}
	resp.Body.Close()
	log.Printf("Caddy route removed: %s", tunnel.Subdomain+"."+s.cfg.Domain)
}

func (s *Server) saveState() {
	type state struct {
		Clients map[string]*Client `json:"clients"`
		Tunnels map[string]*Tunnel `json:"tunnels"`
		NextIP  int                `json:"nextIp"`
	}
	data, _ := json.MarshalIndent(&state{
		Clients: s.clients,
		Tunnels: s.tunnels,
		NextIP:  s.nextIP,
	}, "", "  ")
	os.WriteFile(filepath.Join(s.cfg.DataDir, "state.json"), data, 0600)
}

func (s *Server) loadState() {
	data, err := os.ReadFile(filepath.Join(s.cfg.DataDir, "state.json"))
	if err != nil {
		return
	}
	var st struct {
		Clients map[string]*Client `json:"clients"`
		Tunnels map[string]*Tunnel `json:"tunnels"`
		NextIP  int                `json:"nextIp"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}
	if st.Clients != nil {
		s.clients = st.Clients
	}
	if st.Tunnels != nil {
		s.tunnels = st.Tunnels
	}
	s.nextIP = st.NextIP
	if s.nextIP < 2 {
		s.nextIP = 2
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

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func generateAPIKey() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func generateKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
