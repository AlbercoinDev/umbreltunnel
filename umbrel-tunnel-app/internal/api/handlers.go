package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/albit/umbreltunnel-app/internal/vps"
)

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	vpsConnected := s.vpsClient != nil && s.vpsClient.Check() == nil
	s.json(w, http.StatusOK, map[string]interface{}{
		"wgConnected":  s.wgMgr.IsUp(),
		"vpsConnected": vpsConnected,
		"tunnelCount":  len(s.tunnels),
		"hasConfig":    s.wgMgr.GetConfigContent() != "",
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	wgConfig := s.wgMgr.GetConfigContent()
	vpsRegistered := s.vpsClient != nil
	vpsConnected := false
	if vpsRegistered {
		vpsConnected = s.vpsClient.Check() == nil
	}
	s.json(w, http.StatusOK, map[string]interface{}{
		"wireguardConfigured": wgConfig != "",
		"wireguardConnected":  s.wgMgr.IsUp(),
		"vpsRegistered":       vpsRegistered,
		"vpsConnected":        vpsConnected,
	})
}

func (s *Server) handleWGKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.json(w, http.StatusOK, map[string]string{
		"publicKey": s.wgMgr.GetPublicKey(),
	})
}

func (s *Server) handleWGConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.json(w, http.StatusOK, map[string]string{
			"config": s.wgMgr.GetConfigContent(),
		})
	case http.MethodPost:
		var req struct {
			Config string `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.error(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Config == "" {
			s.error(w, http.StatusBadRequest, "config is required")
			return
		}
		if err := s.wgMgr.ApplyConfig(req.Config); err != nil {
			s.error(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.json(w, http.StatusOK, map[string]string{"status": "connected"})
	default:
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleWGConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Config string `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	configToApply := req.Config
	if configToApply == "" {
		configToApply = s.wgMgr.GetConfigContent()
	}
	if configToApply == "" {
		s.error(w, http.StatusBadRequest, "no config provided")
		return
	}

	configToApply = s.injectPrivateKey(configToApply)

	if err := s.wgMgr.ApplyConfig(configToApply); err != nil {
		s.error(w, http.StatusInternalServerError, fmt.Sprintf("wireguard: %v", err))
		return
	}

	go s.RestoreForwarders()

	s.json(w, http.StatusOK, map[string]interface{}{
		"status":    "connected",
		"publicKey": s.wgMgr.GetPublicKey(),
	})
}

func (s *Server) handleWGStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	isUp := s.wgMgr.IsUp()
	output := ""
	status := "disconnected"
	if isUp {
		status = "connected"
		output = s.wgMgr.Status()
	}

	s.json(w, http.StatusOK, map[string]interface{}{
		"status":    status,
		"connected": isUp,
		"wgOutput":  output,
	})
}

func (s *Server) injectPrivateKey(config string) string {
	if !strings.Contains(config, "<inserta-tu-clave-privada-aqui>") {
		return config
	}
	priv := s.wgMgr.GetPrivateKey()
	if priv == "" {
		return config
	}
	return strings.Replace(config, "<inserta-tu-clave-privada-aqui>", priv, 1)
}

func (s *Server) handleVPSRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		ServerURL string `json:"serverUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.error(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.ServerURL == "" {
		s.error(w, http.StatusBadRequest, "serverUrl is required")
		return
	}

	pubKey := s.wgMgr.GetPublicKey()

	client := vps.NewClient(req.ServerURL, "")
	resp, err := client.Register(pubKey)
	if err != nil {
		s.error(w, http.StatusBadGateway, fmt.Sprintf("VPS registration failed: %v", err))
		return
	}

	configContent := resp.ConfigContent
	if configContent != "" {
		configContent = s.injectPrivateKey(configContent)
		if err := s.wgMgr.ApplyConfig(configContent); err != nil {
			s.error(w, http.StatusInternalServerError, fmt.Sprintf("applying WireGuard config: %v", err))
			return
		}
	}

	s.vpsClient = client
	s.vpsURL = req.ServerURL
	s.vpsAPIKey = resp.APIKey
	s.saveState()

	go s.RestoreForwarders()

	s.json(w, http.StatusOK, map[string]interface{}{
		"status":        "registered",
		"clientIP":      resp.ClientIP,
		"configContent": configContent,
	})
}

func (s *Server) handleVPSCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.vpsClient == nil {
		s.json(w, http.StatusOK, map[string]bool{"connected": false})
		return
	}
	err := s.vpsClient.Check()
	s.json(w, http.StatusOK, map[string]interface{}{
		"connected": err == nil,
		"error":     errToString(err),
	})
}

func (s *Server) handleTunnels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if s.tunnels == nil {
			s.json(w, http.StatusOK, []TunnelEntry{})
			return
		}
		s.json(w, http.StatusOK, s.tunnels)

	case http.MethodPost:
		var req struct {
			Name      string `json:"name"`
			LocalPort int    `json:"localPort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.error(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name == "" {
			s.error(w, http.StatusBadRequest, "name is required")
			return
		}
		if req.LocalPort < 1 || req.LocalPort > 65535 {
			s.error(w, http.StatusBadRequest, "port must be 1-65535")
			return
		}

		if s.vpsClient == nil {
			s.error(w, http.StatusBadGateway, "VPS not connected. Complete setup first.")
			return
		}

		clientIP := s.wgMgr.GetClientIPFromConfig()

		tunnel, err := s.vpsClient.CreateTunnel(req.Name, req.LocalPort, clientIP)
		if err != nil {
			s.error(w, http.StatusBadGateway, fmt.Sprintf("VPS error: %v", err))
			return
		}

		entry := TunnelEntry{
			ID:        tunnel.ID,
			Name:      tunnel.Name,
			LocalPort: req.LocalPort,
			LocalHost: "umbrel.local",
			PublicURL: tunnel.PublicURL,
			CreatedAt: time.Now().Format(time.RFC3339),
		}

		if err := s.startForwarder(clientIP, req.LocalPort); err != nil {
			s.error(w, http.StatusInternalServerError, fmt.Sprintf("starting forwarder: %v", err))
			return
		}

		s.tunnels = append(s.tunnels, entry)
		s.saveState()

		s.json(w, http.StatusCreated, entry)

	default:
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleTunnelByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")

	switch r.Method {
	case http.MethodDelete:
		idx := -1
		for i, t := range s.tunnels {
			if t.ID == id {
				idx = i
				break
			}
		}
		if idx == -1 {
			s.error(w, http.StatusNotFound, "tunnel not found")
			return
		}

		entry := s.tunnels[idx]

		if s.vpsClient != nil {
			if err := s.vpsClient.DeleteTunnel(id); err != nil {
				s.error(w, http.StatusBadGateway, fmt.Sprintf("VPS error: %v", err))
				return
			}
		}

		clientIP := s.wgMgr.GetClientIPFromConfig()
		s.stopForwarder(clientIP, entry.LocalPort)

		s.tunnels = append(s.tunnels[:idx], s.tunnels[idx+1:]...)
		s.saveState()
		s.json(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		s.error(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func errToString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
