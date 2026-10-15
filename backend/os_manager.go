package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// OSTemplate represents a bootable OS configuration
type OSTemplate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	BootMethod  string            `json:"boot_method"` // "chain", "template", "local"
	NextPath    string            `json:"next_path,omitempty"`
	BootTemplate string           `json:"boot_template,omitempty"`
	Variables   map[string]string `json:"variables,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	IsBuiltIn   bool              `json:"is_built_in"`
}

// ServerOSAssignment represents the OS assigned to a server
type ServerOSAssignment struct {
	ServerID   string    `json:"server_id"`
	TemplateID string    `json:"template_id"`
	AssignedAt time.Time `json:"assigned_at"`
	AssignedBy string    `json:"assigned_by,omitempty"`
}

// OSManager manages OS templates and server assignments
type OSManager struct {
	templates   map[string]OSTemplate         // template_id -> template
	assignments map[string]ServerOSAssignment // server_id -> assignment
	mu          sync.RWMutex
}

// NewOSManager creates a new OS manager with built-in templates
func NewOSManager() *OSManager {
	manager := &OSManager{
		templates:   make(map[string]OSTemplate),
		assignments: make(map[string]ServerOSAssignment),
	}
	
	// Load built-in templates
	manager.loadBuiltInTemplates()
	
	return manager
}

// loadBuiltInTemplates adds default OS templates
func (m *OSManager) loadBuiltInTemplates() {
	builtInTemplates := []OSTemplate{
		{
			ID:          "local-disk",
			Name:        "Boot from Local Disk",
			Description: "Standard local disk boot",
			BootMethod:  "local",
			CreatedAt:   time.Now(),
			IsBuiltIn:   true,
		},
		{
			ID:          "nixos-stable",
			Name:        "NixOS 24.05 Stable",
			Description: "Latest stable NixOS with flake support",
			BootMethod:  "chain",
			NextPath:    "https://netboot.nixos.org/ipxe?hostname={{.Hostname}}&mac={{.MAC}}&role={{.Role}}",
			CreatedAt:   time.Now(),
			IsBuiltIn:   true,
		},
		{
			ID:          "ubuntu-22.04-autoinstall",
			Name:        "Ubuntu 22.04 LTS Autoinstall",
			Description: "Ubuntu 22.04 with automated installation",
			BootMethod:  "template",
			BootTemplate: `#!ipxe

echo Lab Manager - Ubuntu 22.04 Autoinstall for {{.Hostname}}
echo Role: {{.Role}}
echo MAC: {{.MAC}}

# Download kernel and initrd
kernel http://archive.ubuntu.com/ubuntu/dists/jammy/main/installer-amd64/current/legacy-images/netboot/ubuntu-installer/amd64/linux
initrd http://archive.ubuntu.com/ubuntu/dists/jammy/main/installer-amd64/current/legacy-images/netboot/ubuntu-installer/amd64/initrd.gz

# Boot with autoinstall parameters
imgargs linux auto=true priority=critical
imgargs linux hostname={{.Hostname}}
imgargs linux preseed/url=http://lab-manager:8098/preseed/{{.Role}}.cfg

boot`,
			CreatedAt: time.Now(),
			IsBuiltIn: true,
		},
		{
			ID:          "rescue-system",
			Name:        "System Rescue CD",
			Description: "Network-based rescue environment",
			BootMethod:  "chain",
			NextPath:    "http://rescue.lab.local/sysrescue?target={{.Hostname}}&client={{.IncomingServerIP}}",
			CreatedAt:   time.Now(),
			IsBuiltIn:   true,
		},
	}
	
	for _, template := range builtInTemplates {
		m.templates[template.ID] = template
	}
}

// GetAllTemplates returns all OS templates
func (m *OSManager) GetAllTemplates() []OSTemplate {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	templates := make([]OSTemplate, 0, len(m.templates))
	for _, template := range m.templates {
		templates = append(templates, template)
	}
	
	return templates
}

// GetTemplate returns a specific template by ID
func (m *OSManager) GetTemplate(id string) (OSTemplate, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	template, exists := m.templates[id]
	return template, exists
}

// AddTemplate adds a new custom template
func (m *OSManager) AddTemplate(template OSTemplate) (OSTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if template.ID == "" {
		template.ID = m.uniqueTemplateID(template.Name)
	}
	if template.ID == "" {
		return OSTemplate{}, fmt.Errorf("template needs an ID or a name")
	}
	
	if _, exists := m.templates[template.ID]; exists {
		return OSTemplate{}, fmt.Errorf("template with ID %s already exists", template.ID)
	}
	
	template.CreatedAt = time.Now()
	template.IsBuiltIn = false
	m.templates[template.ID] = template
	
	return template, nil
}

// uniqueTemplateID derives an unused template ID from a name, e.g. "NixOS Installer" -> "nixos-installer".
// Caller must hold m.mu.
func (m *OSManager) uniqueTemplateID(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	base := strings.TrimSuffix(b.String(), "-")
	if base == "" {
		return ""
	}
	id := base
	for i := 2; ; i++ {
		if _, exists := m.templates[id]; !exists {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

// UpdateTemplate updates an existing custom template
func (m *OSManager) UpdateTemplate(template OSTemplate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	existing, exists := m.templates[template.ID]
	if !exists {
		return fmt.Errorf("template with ID %s not found", template.ID)
	}
	
	if existing.IsBuiltIn {
		return fmt.Errorf("cannot modify built-in template %s", template.ID)
	}
	
	// Preserve creation time and built-in status
	template.CreatedAt = existing.CreatedAt
	template.IsBuiltIn = false
	m.templates[template.ID] = template
	
	return nil
}

// DeleteTemplate removes a custom template
func (m *OSManager) DeleteTemplate(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	template, exists := m.templates[id]
	if !exists {
		return fmt.Errorf("template with ID %s not found", id)
	}
	
	if template.IsBuiltIn {
		return fmt.Errorf("cannot delete built-in template %s", id)
	}
	
	// Check if any servers are using this template
	for _, assignment := range m.assignments {
		if assignment.TemplateID == id {
			return fmt.Errorf("cannot delete template %s: still assigned to servers", id)
		}
	}
	
	delete(m.templates, id)
	return nil
}

// AssignOSToServer assigns an OS template to a server
func (m *OSManager) AssignOSToServer(serverID, templateID, assignedBy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Verify template exists
	if _, exists := m.templates[templateID]; !exists {
		return fmt.Errorf("template with ID %s not found", templateID)
	}
	
	assignment := ServerOSAssignment{
		ServerID:   serverID,
		TemplateID: templateID,
		AssignedAt: time.Now(),
		AssignedBy: assignedBy,
	}
	
	m.assignments[serverID] = assignment
	return nil
}

// GetServerOSAssignment returns the OS assignment for a server
func (m *OSManager) GetServerOSAssignment(serverID string) (ServerOSAssignment, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	assignment, exists := m.assignments[serverID]
	return assignment, exists
}

// RemoveServerOSAssignment removes OS assignment from a server (reverts to local boot)
func (m *OSManager) RemoveServerOSAssignment(serverID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	delete(m.assignments, serverID)
}

// GetAllAssignments returns all server OS assignments
func (m *OSManager) GetAllAssignments() []ServerOSAssignment {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	assignments := make([]ServerOSAssignment, 0, len(m.assignments))
	for _, assignment := range m.assignments {
		assignments = append(assignments, assignment)
	}
	
	return assignments
}

// BulkAssignOS assigns the same OS template to multiple servers
func (m *OSManager) BulkAssignOS(serverIDs []string, templateID, assignedBy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Verify template exists
	if _, exists := m.templates[templateID]; !exists {
		return fmt.Errorf("template with ID %s not found", templateID)
	}
	
	now := time.Now()
	for _, serverID := range serverIDs {
		assignment := ServerOSAssignment{
			ServerID:   serverID,
			TemplateID: templateID,
			AssignedAt: now,
			AssignedBy: assignedBy,
		}
		m.assignments[serverID] = assignment
	}
	
	return nil
}

// SaveToFile persists assignments to a JSON file
func (m *OSManager) SaveToFile(filepath string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	data := struct {
		Templates   map[string]OSTemplate         `json:"templates"`
		Assignments map[string]ServerOSAssignment `json:"assignments"`
		SavedAt     time.Time                     `json:"saved_at"`
	}{
		Templates:   make(map[string]OSTemplate),
		Assignments: m.assignments,
		SavedAt:     time.Now(),
	}
	
	// Only save custom templates (not built-ins)
	for id, template := range m.templates {
		if !template.IsBuiltIn {
			data.Templates[id] = template
		}
	}
	
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal OS data: %v", err)
	}
	
	return os.WriteFile(filepath, jsonData, 0644)
}

// LoadFromFile loads assignments from a JSON file
func (m *OSManager) LoadFromFile(filepath string) error {
	data, err := os.ReadFile(filepath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, that's fine
			return nil
		}
		return fmt.Errorf("failed to read OS data file: %v", err)
	}
	
	var fileData struct {
		Templates   map[string]OSTemplate         `json:"templates"`
		Assignments map[string]ServerOSAssignment `json:"assignments"`
		SavedAt     time.Time                     `json:"saved_at"`
	}
	
	if err := json.Unmarshal(data, &fileData); err != nil {
		return fmt.Errorf("failed to unmarshal OS data: %v", err)
	}
	
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Load custom templates
	for id, template := range fileData.Templates {
		if !template.IsBuiltIn {
			m.templates[id] = template
		}
	}
	
	// Load assignments
	m.assignments = fileData.Assignments
	if m.assignments == nil {
		m.assignments = make(map[string]ServerOSAssignment)
	}
	
	return nil
}