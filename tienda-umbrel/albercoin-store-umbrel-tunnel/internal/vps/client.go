package vps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type RegisterResponse struct {
	ID            string `json:"id"`
	ClientIP      string `json:"clientIp"`
	ClientAddress string `json:"clientAddress"`
	Endpoint      string `json:"endpoint"`
	DNS           string `json:"dns"`
	AllowedIPs    string `json:"allowedIPs"`
	Keepalive     int    `json:"keepalive"`
	ConfigContent string `json:"configContent"`
	APIKey        string `json:"apiKey"`
}

type TunnelResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Subdomain string `json:"subdomain"`
	PublicURL string `json:"publicUrl"`
	LocalPort int    `json:"localPort"`
	CreatedAt string `json:"createdAt"`
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) Register(publicKey string) (*RegisterResponse, error) {
	body, _ := json.Marshal(map[string]string{"publicKey": publicKey})

	resp, err := c.httpClient.Post(c.baseURL+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("connecting to VPS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("VPS error (%d): %s", resp.StatusCode, string(data))
	}

	var result RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	c.apiKey = result.APIKey
	return &result, nil
}

func (c *Client) CreateTunnel(name string, localPort int, clientIP string) (*TunnelResponse, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"name":      name,
		"localPort": localPort,
		"clientIp":  clientIP,
	})

	req, _ := http.NewRequest("POST", c.baseURL+"/api/tunnels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling VPS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("VPS error (%d): %s", resp.StatusCode, string(data))
	}

	var result TunnelResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

func (c *Client) DeleteTunnel(id string) error {
	req, _ := http.NewRequest("DELETE", c.baseURL+"/api/tunnels/"+id, nil)
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling VPS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("VPS error (%d): %s", resp.StatusCode, string(data))
	}
	return nil
}

func (c *Client) ListTunnels() ([]TunnelResponse, error) {
	req, _ := http.NewRequest("GET", c.baseURL+"/api/tunnels", nil)
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling VPS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("VPS error: %d", resp.StatusCode)
	}

	var result []TunnelResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return result, nil
}

func (c *Client) Check() error {
	resp, err := c.httpClient.Get(c.baseURL + "/api/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("VPS returned status %d", resp.StatusCode)
	}
	return nil
}
