package wireguard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type Manager struct {
	dataDir    string
	iface      string
	mu         sync.Mutex
	privateKey string
	publicKey  string
	configPath string
	keyPath    string
}

func NewManager(dataDir, iface string) *Manager {
	return &Manager{
		dataDir:    dataDir,
		iface:      iface,
		configPath: filepath.Join(dataDir, "wg.conf"),
		keyPath:    filepath.Join(dataDir, "key"),
	}
}

func (m *Manager) loadKeyFromDisk() bool {
	data, err := os.ReadFile(m.keyPath)
	if err != nil {
		return false
	}
	priv := strings.TrimSpace(string(data))
	if priv == "" {
		return false
	}
	pub, err := pipeToWg("pubkey", priv)
	if err != nil {
		return false
	}
	m.privateKey = priv
	m.publicKey = strings.TrimSpace(pub)
	return true
}

func (m *Manager) loadKeyFromConfig() bool {
	configData, err := os.ReadFile(m.configPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(configData), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "PrivateKey") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				priv := strings.TrimSpace(parts[1])
				if priv == "" || priv == "<inserta-tu-clave-privada-aqui>" {
					return false
				}
				pub, err := pipeToWg("pubkey", priv)
				if err == nil {
					m.privateKey = priv
					m.publicKey = strings.TrimSpace(pub)
					m.saveKeyToDisk()
					return true
				}
			}
		}
	}
	return false
}

func (m *Manager) generateKey() string {
	priv, err := wgCommand("genkey")
	if err != nil {
		return ""
	}
	priv = strings.TrimSpace(priv)
	pub, err := pipeToWg("pubkey", priv)
	if err != nil {
		return ""
	}
	m.privateKey = priv
	m.publicKey = strings.TrimSpace(pub)
	m.saveKeyToDisk()
	return m.publicKey
}

func (m *Manager) saveKeyToDisk() {
	os.MkdirAll(m.dataDir, 0700)
	os.WriteFile(m.keyPath, []byte(m.privateKey), 0600)
}

func (m *Manager) GetPublicKey() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.publicKey != "" {
		return m.publicKey
	}

	if m.loadKeyFromDisk() {
		return m.publicKey
	}

	if m.loadKeyFromConfig() {
		return m.publicKey
	}

	return m.generateKey()
}

func (m *Manager) GetPrivateKey() string {
	m.GetPublicKey()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.privateKey
}

func (m *Manager) ApplyConfig(configContent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.WriteFile(m.configPath, []byte(configContent), 0600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	exec.Command("wg-quick", "down", m.configPath).Run()

	cmd := exec.Command("wg-quick", "up", m.configPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wg-quick up: %s: %w", string(out), err)
	}

	return nil
}

func (m *Manager) Disconnect() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	exec.Command("wg-quick", "down", m.configPath).Run()
	return nil
}

func (m *Manager) IsUp() bool {
	if err := exec.Command("wg", "show", m.iface).Run(); err == nil {
		return true
	}
	iface := m.detectInterface()
	if iface != "" {
		return exec.Command("wg", "show", iface).Run() == nil
	}
	return false
}

func (m *Manager) detectInterface() string {
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Interface") || line == "[Interface]" {
			continue
		}
	}
	out, err := exec.Command("wg", "show", "interfaces").Output()
	if err != nil {
		return ""
	}
	for _, iface := range strings.Fields(string(out)) {
		if iface != "" {
			return iface
		}
	}
	return ""
}

func (m *Manager) Status() string {
	if !m.IsUp() {
		return "disconnected"
	}
	iface := m.iface
	if exec.Command("wg", "show", iface).Run() != nil {
		iface = m.detectInterface()
	}
	out, _ := exec.Command("wg", "show", iface).Output()
	return string(out)
}

func (m *Manager) GetConfigContent() string {
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return ""
	}
	return string(data)
}

func (m *Manager) GetConfigPath() string {
	return m.configPath
}

func (m *Manager) GetClientIPFromConfig() string {
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Address") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				addr := strings.TrimSpace(parts[1])
				addr = strings.Split(addr, ",")[0]
				addr = strings.TrimSpace(addr)
				if strings.Contains(addr, "/") {
					return strings.Split(addr, "/")[0]
				}
				return addr
			}
		}
	}
	return ""
}

func wgCommand(subcmd string) (string, error) {
	out, err := exec.Command("wg", subcmd).Output()
	if err != nil {
		return "", fmt.Errorf("wg %s: %w", subcmd, err)
	}
	return string(out), nil
}

func pipeToWg(subcmd, input string) (string, error) {
	cmd := exec.Command("wg", subcmd)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("wg %s: %w", subcmd, err)
	}
	return string(out), nil
}
