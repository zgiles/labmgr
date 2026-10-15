package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/gin-gonic/gin"
)

// Template variables available in iPXE scripts
type iPXETemplateVars struct {
	MAC              string
	Hostname         string
	Role             string
	ServerName       string
	ServerID         string
	Description      string
	IncomingServerIP string
	Metadata         map[string]string
}

// processTemplate processes an iPXE script template with the given variables
func processTemplate(templateStr string, vars iPXETemplateVars) (string, error) {
	tmpl, err := template.New("ipxe").Parse(templateStr)
	if err != nil {
		return "", err
	}
	var result strings.Builder
	if err := tmpl.Execute(&result, vars); err != nil {
		return "", err
	}
	return result.String(), nil
}

func templateVars(server *Server, mac, clientIP string) iPXETemplateVars {
	return iPXETemplateVars{
		MAC:              mac,
		Hostname:         server.Hostname(),
		Role:             server.Metadata["role"],
		ServerName:       server.Name,
		ServerID:         server.ID,
		Description:      server.Description,
		IncomingServerIP: clientIP,
		Metadata:         server.Metadata,
	}
}

// BootScript is what a server gets when it network boots
type BootScript struct {
	Script     string `json:"script"`
	Source     string `json:"source"` // template ID, "next-path", "boot-template", or "local"
	SourceName string `json:"source_name"`
	OneShot    bool   `json:"one_shot"`
}

// renderBootScript decides and renders a server's boot script without side effects.
// Priority: OS assignment, then static metadata next-path, then metadata boot-template, then local disk.
func renderBootScript(server *Server, mac, clientIP string) BootScript {
	vars := templateVars(server, mac, clientIP)

	if assignment, exists := osManager.GetServerOSAssignment(server.ID); exists {
		if tmpl, ok := osManager.GetTemplate(assignment.TemplateID); ok {
			return BootScript{
				Script:     generateBootScriptFromTemplate(tmpl, vars, server, mac),
				Source:     tmpl.ID,
				SourceName: tmpl.Name,
				OneShot:    assignment.OneShot,
			}
		}
		log.Printf("Warning: OS template %s not found for server %s, falling back", assignment.TemplateID, server.ID)
	}

	if nextPath := server.Metadata["next-path"]; nextPath != "" {
		processedPath, err := processTemplate(nextPath, vars)
		if err != nil {
			log.Printf("Error processing next-path template for %s: %v", server.ID, err)
			processedPath = nextPath
		}
		script := fmt.Sprintf(`#!ipxe

echo Lab Manager - Booting %s (Static Config)
echo MAC: %s
echo Role: %s
echo Hostname: %s
echo Incoming IP: %s

# Chain load to custom path
echo Chaining to: %s
chain %s ||

echo Chain load failed, booting from local disk
sanboot --no-describe --drive 0x80 ||

echo Boot failed
shell
`, server.Name, mac, server.Metadata["role"], vars.Hostname, vars.IncomingServerIP, processedPath, processedPath)
		return BootScript{Script: script, Source: "next-path", SourceName: "Static next-path"}
	}

	if bootTemplate := server.Metadata["boot-template"]; bootTemplate != "" {
		if script, err := processTemplate(bootTemplate, vars); err == nil {
			return BootScript{Script: script, Source: "boot-template", SourceName: "Static boot-template"}
		} else {
			log.Printf("Error processing boot-template for %s: %v", server.ID, err)
		}
	}

	script, err := processTemplate(`#!ipxe

echo Lab Manager - Booting {{.ServerName}} (Default)
echo MAC: {{.MAC}}
echo Role: {{.Role}}
echo Hostname: {{.Hostname}}
echo Incoming IP: {{.IncomingServerIP}}

# Set hostname
set hostname {{.Hostname}}

# Boot from local disk
sanboot --no-describe --drive 0x80 ||

echo Boot failed
shell
`, vars)
	if err != nil {
		script = generateLocalBootScript(server, mac, "Default template failed")
	}
	return BootScript{Script: script, Source: "local", SourceName: "Local disk"}
}

// generateBootScriptFromTemplate generates an iPXE boot script from an OS template
func generateBootScriptFromTemplate(tmpl OSTemplate, vars iPXETemplateVars, server *Server, mac string) string {
	switch tmpl.BootMethod {
	case "local":
		return fmt.Sprintf(`#!ipxe

echo Lab Manager - OS Template: %s
echo Server: %s
echo MAC: %s
echo Booting from local disk

# Boot from local disk
sanboot --no-describe --drive 0x80 ||

echo Boot failed
shell
`, tmpl.Name, server.Name, mac)

	case "chain":
		if tmpl.NextPath == "" {
			log.Printf("Chain template %s has no next-path", tmpl.ID)
			return generateLocalBootScript(server, mac, "Chain template misconfigured")
		}
		processedPath, err := processTemplate(tmpl.NextPath, vars)
		if err != nil {
			log.Printf("Error processing chain template %s next-path: %v", tmpl.ID, err)
			processedPath = tmpl.NextPath
		}
		return fmt.Sprintf(`#!ipxe

echo Lab Manager - OS Template: %s
echo Server: %s
echo MAC: %s
echo Role: %s
echo Hostname: %s

# Chain load to template URL
echo Chaining to: %s
chain %s ||

echo Chain load failed, booting from local disk
sanboot --no-describe --drive 0x80 ||

echo Boot failed
shell
`, tmpl.Name, server.Name, mac, server.Metadata["role"], vars.Hostname, processedPath, processedPath)

	case "template":
		if tmpl.BootTemplate == "" {
			log.Printf("Template %s has no boot-template", tmpl.ID)
			return generateLocalBootScript(server, mac, "Boot template empty")
		}
		processed, err := processTemplate(tmpl.BootTemplate, vars)
		if err != nil {
			log.Printf("Error processing boot template %s: %v", tmpl.ID, err)
			return generateLocalBootScript(server, mac, "Template processing failed")
		}
		// The template carries its own #!ipxe line; put our banner after it
		processed = strings.TrimPrefix(strings.TrimLeft(processed, " \t\r\n"), "#!ipxe")
		return fmt.Sprintf(`#!ipxe

echo Lab Manager - OS Template: %s
echo Server: %s
echo MAC: %s
echo Role: %s
echo Hostname: %s

# Custom template begins
`, tmpl.Name, server.Name, mac, server.Metadata["role"], vars.Hostname) + processed

	default:
		log.Printf("Unknown boot method %s for template %s", tmpl.BootMethod, tmpl.ID)
		return generateLocalBootScript(server, mac, "Unknown boot method")
	}
}

// generateLocalBootScript generates a fallback local boot script
func generateLocalBootScript(server *Server, mac, reason string) string {
	return fmt.Sprintf(`#!ipxe

echo Lab Manager - Local Boot Fallback
echo Server: %s
echo MAC: %s
echo Reason: %s

# Boot from local disk
sanboot --no-describe --drive 0x80 ||

echo Boot failed
shell
`, server.Name, mac, reason)
}

func unknownServerScript(mac string) string {
	return fmt.Sprintf(`#!ipxe

echo Lab Manager - Unknown server
echo MAC: %s
echo No configuration found, booting from local disk

# Boot from local disk
sanboot --no-describe --drive 0x80 ||

# Final fallback
echo Boot failed
shell
`, mac)
}

// getAutoExecScript handles the standard /autoexec.ipxe endpoint
func (a *App) getAutoExecScript(c *gin.Context) {
	mac := c.Query("mac")
	if mac == "" {
		mac = c.Query("uuid")
	}
	if mac == "" {
		c.String(http.StatusBadRequest, "#!ipxe\necho MAC address required (?mac=xx:xx:xx:xx:xx:xx)\nshell")
		return
	}
	a.serveBootScript(c, mac)
}

func (a *App) getBootScript(c *gin.Context) {
	a.serveBootScript(c, c.Param("mac"))
}

// serveBootScript serves a server's iPXE script, records the fetch, and consumes one-shot assignments
func (a *App) serveBootScript(c *gin.Context, rawMAC string) {
	mac := normalizeMAC(rawMAC)
	config := a.config.Get()

	var server *Server
	for i := range config.Servers {
		if config.Servers[i].MAC() == mac {
			server = &config.Servers[i]
			break
		}
	}
	c.Header("Content-Type", "text/plain")
	if server == nil {
		c.String(http.StatusOK, unknownServerScript(mac))
		return
	}

	clientIP := c.ClientIP()
	boot := renderBootScript(server, mac, clientIP)
	c.String(http.StatusOK, boot.Script)

	a.state.RecordBoot(server.ID, BootFetch{At: time.Now(), ClientIP: clientIP, Source: boot.Source})
	message := fmt.Sprintf("Network booted %s from %s", boot.SourceName, clientIP)
	if boot.OneShot && osManager.ConsumeOneShot(server.ID) {
		saveOSData()
		a.hub.Publish("os", nil)
		message += "; one-time boot used, next boot reverts"
	}
	a.recordEvent(&Event{ServerID: server.ID, Kind: "boot", Message: message, Level: "info"})
}
