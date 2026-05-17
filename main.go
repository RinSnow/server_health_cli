package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Constants & Config ---

const (
	fetchInterval = 60 * time.Second
	tickInterval  = 1 * time.Second
)

// --- Config ---

type Config struct {
	HealthCheckPort int      `json:"healthCheckPort"`
	SSLCheckPorts   []int    `json:"sslCheckPorts"`
	Testing         []string `json:"testing"`
	Production      []string `json:"production"`
}

func loadConfig() (Config, error) {
	config := Config{
		HealthCheckPort: 442,
		SSLCheckPorts:   []int{443},
	}

	baseConfig, err := readConfigFile("config.json")
	if err != nil {
		return Config{}, err
	}
	mergeConfig(&config, baseConfig)

	privateConfig, err := readConfigFile("config.private.json")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	if err == nil {
		mergeConfig(&config, privateConfig)
	}

	if err := applyEnvOverrides(&config); err != nil {
		return Config{}, err
	}

	if err := validateConfig(config); err != nil {
		return Config{}, err
	}

	return config, nil
}

func readConfigFile(path string) (Config, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var config Config
	err = json.Unmarshal(file, &config)
	if err != nil {
		return Config{}, fmt.Errorf("invalid %s: %w", path, err)
	}

	return config, nil
}

func mergeConfig(base *Config, override Config) {
	if override.HealthCheckPort != 0 {
		base.HealthCheckPort = override.HealthCheckPort
	}
	if len(override.SSLCheckPorts) > 0 {
		base.SSLCheckPorts = append([]int(nil), override.SSLCheckPorts...)
	}
	if len(override.Testing) > 0 {
		base.Testing = append([]string(nil), override.Testing...)
	}
	if len(override.Production) > 0 {
		base.Production = append([]string(nil), override.Production...)
	}
}

func applyEnvOverrides(config *Config) error {
	healthPortRaw := strings.TrimSpace(os.Getenv("HEALTH_CHECK_PORT"))
	if healthPortRaw != "" {
		healthPort, err := parsePort(healthPortRaw)
		if err != nil {
			return fmt.Errorf("invalid HEALTH_CHECK_PORT: %w", err)
		}
		config.HealthCheckPort = healthPort
	}

	sslPortsRaw := strings.TrimSpace(os.Getenv("SSL_CHECK_PORTS"))
	if sslPortsRaw != "" {
		ports, err := parsePortList(sslPortsRaw)
		if err != nil {
			return fmt.Errorf("invalid SSL_CHECK_PORTS: %w", err)
		}
		config.SSLCheckPorts = ports
	}

	testingRaw := strings.TrimSpace(os.Getenv("TESTING_SERVERS"))
	if testingRaw != "" {
		config.Testing = parseHostList(testingRaw)
	}

	productionRaw := strings.TrimSpace(os.Getenv("PROD_SERVERS"))
	if productionRaw != "" {
		config.Production = parseHostList(productionRaw)
	}

	return nil
}

func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("must be a number")
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("must be between 1 and 65535")
	}
	return port, nil
}

func parsePortList(raw string) ([]int, error) {
	parts := strings.Split(raw, ",")
	ports := make([]int, 0, len(parts))

	for _, part := range parts {
		portRaw := strings.TrimSpace(part)
		if portRaw == "" {
			continue
		}
		port, err := parsePort(portRaw)
		if err != nil {
			return nil, err
		}
		ports = append(ports, port)
	}

	if len(ports) == 0 {
		return nil, fmt.Errorf("must contain at least one port")
	}

	return ports, nil
}

func parseHostList(raw string) []string {
	parts := strings.Split(raw, ",")
	hosts := make([]string, 0, len(parts))
	for _, part := range parts {
		host := strings.TrimSpace(part)
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func validateConfig(config Config) error {
	if _, err := parsePort(strconv.Itoa(config.HealthCheckPort)); err != nil {
		return fmt.Errorf("invalid healthCheckPort: %w", err)
	}

	if len(config.SSLCheckPorts) == 0 {
		return fmt.Errorf("sslCheckPorts must contain at least one port")
	}

	for _, port := range config.SSLCheckPorts {
		if _, err := parsePort(strconv.Itoa(port)); err != nil {
			return fmt.Errorf("invalid sslCheckPorts entry %d: %w", port, err)
		}
	}

	return nil
}

var (
	healthCheckPort int
	sslCheckPorts   []int
	testingServers  []string
	prodServers     []string
)

// --- Styles ---

var (
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	danger    = lipgloss.AdaptiveColor{Light: "#F25D94", Dark: "#F55086"}
	warning   = lipgloss.AdaptiveColor{Light: "#F2B05D", Dark: "#F5A050"}

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(highlight).
			Padding(0, 1).
			MarginBottom(1)

	styleCard = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(subtle).
			Padding(0, 1).
			MarginRight(1).
			MarginBottom(1)

	styleStatusOk = lipgloss.NewStyle().
			Foreground(special).
			Bold(true)

	styleStatusErr = lipgloss.NewStyle().
			Foreground(danger).
			Bold(true)

	styleStatusWarn = lipgloss.NewStyle().
			Foreground(warning).
			Bold(true)

	styleLabel = lipgloss.NewStyle().
			Foreground(subtle)

	// Column widths
	minWidthStatus  = 8
	minWidthName    = 20
	minWidthVersion = 10
	minWidthUptime  = 15
	minWidthDisk    = 10
	minWidthSSL     = 20
	minWidthLatency = 10
)

// --- Model ---

type ServerHealth struct {
	Status      string  `json:"status"`
	Timestamp   string  `json:"timestamp"`
	Uptime      float64 `json:"uptime"`
	Environment string  `json:"environment"`
	Version     string  `json:"version"`
}

type ServerState struct {
	Name        string
	URL         string
	Loading     bool
	Data        *ServerHealth
	Error       error
	LastUpdated time.Time
	DiskUsage   string
	SSLExpiry   map[int]time.Time // Map of port -> SSL expiry time
	SSLInfo     string
	Latency     time.Duration
}

type model struct {
	Servers map[string]*ServerState
	Width   int
	Height  int
	Scroll  int
}

// --- Messages ---

type tickMsg time.Time

type fetchMsg struct {
	Name      string
	Data      *ServerHealth
	Err       error
	SSLExpiry time.Time // SSL expiry from health check port
	Latency   time.Duration
}

type sslCheckMsg struct {
	Name      string
	Port      int
	SSLExpiry time.Time
	Err       error
}

type diskCheckMsg struct {
	Name  string
	Usage string
	Err   error
}

// --- Init ---

func initialModel() model {
	config, err := loadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	healthCheckPort = config.HealthCheckPort
	sslCheckPorts = config.SSLCheckPorts

	testingServers = config.Testing
	prodServers = config.Production

	m := model{
		Servers: make(map[string]*ServerState),
	}

	allServers := append(testingServers, prodServers...)
	for _, host := range allServers {
		m.Servers[host] = &ServerState{
			Name:      host,
			URL:       fmt.Sprintf("https://%s:%d/health", host, healthCheckPort),
			Loading:   true,
			SSLExpiry: make(map[int]time.Time),
		}
	}
	return m
}

func (m model) Init() tea.Cmd {
	// Initial fetch for all servers + start tick + disk check + SSL checks
	var cmds []tea.Cmd
	cmds = append(cmds, tickCmd())
	for _, s := range m.Servers {
		cmds = append(cmds, fetchCmd(s.Name, s.URL))
		cmds = append(cmds, checkDiskSpaceCmd(s.Name)) // One-time check
		// Check SSL for each configured port
		for _, port := range sslCheckPorts {
			cmds = append(cmds, checkSSLCmd(s.Name, port))
		}
	}
	return tea.Batch(cmds...)
}

// --- Update ---

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.Scroll > 0 {
				m.Scroll--
			}
		case "down", "j":
			m.Scroll++
		case "pgup", "b":
			if m.Height > 0 {
				m.Scroll -= m.Height / 2
				if m.Scroll < 0 {
					m.Scroll = 0
				}
			}
		case "pgdown", "f":
			if m.Height > 0 {
				m.Scroll += m.Height / 2
			}
		}

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height

	case tickMsg:
		// Increment uptime for healthy servers
		for _, s := range m.Servers {
			if s.Data != nil && s.Data.Status == "OK" {
				s.Data.Uptime += 1.0
			}
		}

		// Check if we need to re-fetch (every 60s)
		var cmds []tea.Cmd
		now := time.Now()
		for _, s := range m.Servers {
			if !s.Loading && now.Sub(s.LastUpdated) >= fetchInterval {
				// s.Loading = true // Optional: don't show loading on refresh if you want smooth UI
				cmds = append(cmds, fetchCmd(s.Name, s.URL))
			}
		}
		cmds = append(cmds, tickCmd())
		return m, tea.Batch(cmds...)

	case fetchMsg:
		if s, ok := m.Servers[msg.Name]; ok {
			s.Loading = false
			if msg.Err != nil {
				s.Error = msg.Err
			} else {
				s.Data = msg.Data
				s.Error = nil
				s.LastUpdated = time.Now()
				// Store SSL expiry from health check port
				if !msg.SSLExpiry.IsZero() {
					s.SSLExpiry[healthCheckPort] = msg.SSLExpiry
				}
				s.Latency = msg.Latency
			}
		}

	case sslCheckMsg:
		if s, ok := m.Servers[msg.Name]; ok {
			if msg.Err == nil && !msg.SSLExpiry.IsZero() {
				s.SSLExpiry[msg.Port] = msg.SSLExpiry
			}
		}

	case diskCheckMsg:
		if s, ok := m.Servers[msg.Name]; ok {
			if msg.Err == nil {
				s.DiskUsage = msg.Usage
			} else {
				// s.DiskUsage = "Err" // Optional: show error or leave empty
			}
		}
	}

	return m, nil
}

// --- Commands ---

func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func fetchCmd(name, url string) tea.Cmd {
	return func() tea.Msg {
		client := http.Client{
			Timeout: 10 * time.Second,
		}

		start := time.Now()
		resp, err := client.Get(url)
		latency := time.Since(start)

		if err != nil {
			return fetchMsg{Name: name, Err: err, Latency: latency}
		}
		defer resp.Body.Close()

		var sslExpiry time.Time
		if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
			sslExpiry = resp.TLS.PeerCertificates[0].NotAfter
		}

		if resp.StatusCode != http.StatusOK {
			return fetchMsg{Name: name, Err: fmt.Errorf("status %d", resp.StatusCode), Latency: latency}
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fetchMsg{Name: name, Err: err, Latency: latency}
		}

		var health ServerHealth
		if err := json.Unmarshal(body, &health); err != nil {
			// Check if it looks like HTML
			if len(body) > 0 && body[0] == '<' {
				return fetchMsg{Name: name, Err: fmt.Errorf("received HTML"), Latency: latency}
			}
			return fetchMsg{Name: name, Err: fmt.Errorf("invalid JSON"), Latency: latency}
		}

		return fetchMsg{Name: name, Data: &health, SSLExpiry: sslExpiry, Latency: latency}
	}
}

func checkSSLCmd(host string, port int) tea.Cmd {
	return func() tea.Msg {
		client := http.Client{
			Timeout: 10 * time.Second,
		}

		url := fmt.Sprintf("https://%s:%d/", host, port)
		resp, err := client.Get(url)
		if err != nil {
			return sslCheckMsg{Name: host, Port: port, Err: err}
		}
		defer resp.Body.Close()

		var sslExpiry time.Time
		if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
			sslExpiry = resp.TLS.PeerCertificates[0].NotAfter
		}

		return sslCheckMsg{Name: host, Port: port, SSLExpiry: sslExpiry}
	}
}

func checkDiskSpaceCmd(host string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command(
			"ssh",
			"-o", "BatchMode=yes",
			"-o", "StrictHostKeyChecking=accept-new",
			host,
			"df -h",
		)
		output, err := cmd.Output()
		if err != nil {
			return diskCheckMsg{Name: host, Err: err}
		}

		// Parse output
		// Filesystem      Size  Used Avail Use% Mounted on
		// /dev/root        49G   23G   27G  46% /
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) >= 6 && fields[5] == "/" {
				// Return "Avail" which is usually index 3 (Size=1, Used=2, Avail=3)
				return diskCheckMsg{Name: host, Usage: fields[3]}
			}
		}

		return diskCheckMsg{Name: host, Err: fmt.Errorf("root partition not found")}
	}
}

// --- View ---

func formatUptime(seconds float64) string {
	d := time.Duration(seconds) * time.Second
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dh %dm %ds", h, m, s)
}

func (m model) View() string {
	if m.Width == 0 {
		return "Loading..."
	}

	// Calculate dynamic widths
	// Total fixed/minimum width required
	minTotalWidth := minWidthStatus + minWidthName + minWidthVersion + minWidthUptime + minWidthDisk + minWidthSSL + minWidthLatency

	// Available extra width
	extraWidth := m.Width - minTotalWidth - 4 // -4 for some margins/borders
	if extraWidth < 0 {
		extraWidth = 0
	}

	// Distribute extra width
	// Give 40% to Name, 20% to SSL, 10% to others
	wName := minWidthName + int(float64(extraWidth)*0.4)
	wSSL := minWidthSSL + int(float64(extraWidth)*0.2)
	wStatus := minWidthStatus + int(float64(extraWidth)*0.1)
	wVersion := minWidthVersion + int(float64(extraWidth)*0.05)
	wUptime := minWidthUptime + int(float64(extraWidth)*0.1)
	wDisk := minWidthDisk + int(float64(extraWidth)*0.05)
	wLatency := minWidthLatency + int(float64(extraWidth)*0.1)

	// Summary Stats
	total := len(m.Servers)
	healthy := 0
	errors := 0
	var totalUptime float64
	uptimeCount := 0

	for _, s := range m.Servers {
		if s.Error != nil {
			errors++
		} else if s.Data != nil && s.Data.Status == "OK" {
			healthy++
			totalUptime += s.Data.Uptime
			uptimeCount++
		}
	}

	avgUptime := 0.0
	if uptimeCount > 0 {
		avgUptime = totalUptime / float64(uptimeCount)
	}

	// Render Header
	header := styleTitle.Render("Server Health Monitor CLI")

	// Calculate available height for spacing
	// Estimate height usage: Header (2) + Summary (4) + Lists (Header(2) + Rows(N)) + Footer (2)
	// We want to expand vertical space if possible.
	verticalMargin := 0
	if m.Height > 40 {
		verticalMargin = 1
	}
	if m.Height > 60 {
		verticalMargin = 2
	}

	summary := lipgloss.JoinHorizontal(lipgloss.Top,
		styleCard.Width(wName).MarginBottom(verticalMargin).Render(fmt.Sprintf("Total\n%d", total)),
		styleCard.Width(wName).MarginBottom(verticalMargin).Render(fmt.Sprintf("Healthy\n%s", styleStatusOk.Render(fmt.Sprintf("%d", healthy)))),
		styleCard.Width(wName).MarginBottom(verticalMargin).Render(fmt.Sprintf("Issues\n%s", styleStatusErr.Render(fmt.Sprintf("%d", errors)))),
		styleCard.Width(wName).MarginBottom(verticalMargin).Render(fmt.Sprintf("Avg Uptime\n%s", formatUptime(avgUptime))),
	)

	// Render Lists
	renderList := func(title string, servers []string) string {
		var rows []string

		// Header row
		headerRow := lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Width(wStatus).Bold(true).Render("Status"),
			lipgloss.NewStyle().Width(wName).Bold(true).Render("Server"),
			lipgloss.NewStyle().Width(wVersion).Bold(true).Render("Version"),
			lipgloss.NewStyle().Width(wUptime).Bold(true).Render("Uptime"),
			lipgloss.NewStyle().Width(wDisk).Bold(true).Render("Disk"),
			lipgloss.NewStyle().Width(wSSL).Bold(true).Render("SSL Expiry"),
			lipgloss.NewStyle().Width(wLatency).Bold(true).Render("Latency"),
		)
		rows = append(rows, headerRow)
		rows = append(rows, lipgloss.NewStyle().Foreground(subtle).Render(strings.Repeat("-", m.Width-4)))

		for _, host := range servers {
			s := m.Servers[host]

			statusText := "OK"
			statusStyle := styleStatusOk
			version := "-"
			uptime := "-"
			disk := "-"
			ssl := "-"
			latency := "-"
			sslStyle := lipgloss.NewStyle()

			if s.Loading && s.Data == nil {
				statusText = "..."
				statusStyle = lipgloss.NewStyle().Foreground(subtle)
			} else if s.Error != nil {
				statusText = "ERR"
				statusStyle = styleStatusErr
				// details = fmt.Sprintf("%v", s.Error) // Maybe show error in a tooltip or separate area? For now, just ERR
			} else if s.Data != nil {
				version = s.Data.Version
				uptime = formatUptime(s.Data.Uptime)
			}

			if s.DiskUsage != "" {
				disk = s.DiskUsage
			}

			// Build SSL string for all configured ports
			if len(s.SSLExpiry) > 0 {
				var sslParts []string
				minDays := 999999 // Track minimum days for overall styling

				for _, port := range sslCheckPorts {
					if expiry, ok := s.SSLExpiry[port]; ok && !expiry.IsZero() {
						days := int(time.Until(expiry).Hours() / 24)
						if days < minDays {
							minDays = days
						}
						sslParts = append(sslParts, fmt.Sprintf("%d:%dd", port, days))
					}
				}

				if len(sslParts) > 0 {
					ssl = strings.Join(sslParts, " | ")
					// Style based on minimum days
					if minDays < 7 {
						sslStyle = styleStatusErr
					} else if minDays < 30 {
						sslStyle = styleStatusWarn
					} else {
						sslStyle = styleStatusOk
					}
				}
			}

			if s.Latency > 0 {
				latency = fmt.Sprintf("%dms", s.Latency.Milliseconds())
			}

			row := lipgloss.JoinHorizontal(lipgloss.Left,
				statusStyle.Width(wStatus).Render(statusText),
				lipgloss.NewStyle().Width(wName).Render(host),
				lipgloss.NewStyle().Width(wVersion).Render(version),
				lipgloss.NewStyle().Width(wUptime).Render(uptime),
				lipgloss.NewStyle().Width(wDisk).Render(disk),
				sslStyle.Width(wSSL).Render(ssl),
				lipgloss.NewStyle().Width(wLatency).Render(latency),
			)
			rows = append(rows, row)
		}

		return styleCard.Width(m.Width - 2).MarginBottom(verticalMargin).Render(lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Bold(true).MarginBottom(1).Render(title),
			lipgloss.JoinVertical(lipgloss.Left, rows...),
		))
	}

	lists := lipgloss.JoinVertical(lipgloss.Left,
		renderList("Testing", testingServers),
		renderList("Production", prodServers),
	)

	fullView := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		summary,
		lists,
		styleLabel.Render("\nPress q to quit | Up/Down or j/k to scroll"),
	)

	return renderScrollable(fullView, m.Height, m.Scroll)
}

func renderScrollable(content string, height int, scroll int) string {
	if height <= 0 {
		return content
	}

	lines := strings.Split(content, "\n")
	if len(lines) <= height {
		return content
	}

	maxScroll := len(lines) - height
	if scroll < 0 {
		scroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}

	return strings.Join(lines[scroll:scroll+height], "\n")
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
	}
}
