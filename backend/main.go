package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/gin-gonic/gin"
)

var configPath string
var listenAddr string
var debugMode bool
var osManager *OSManager

func main() {
	flag.StringVar(&configPath, "config", "config/servers.yaml", "Path to configuration file")
	flag.StringVar(&listenAddr, "listen", "0.0.0.0:8098", "Address to listen on")
	flag.BoolVar(&debugMode, "debug", false, "Enable debug logging")
	flag.Parse()
	
	// Initialize OS manager
	osManager = NewOSManager()
	
	// Load persistent OS assignments if file exists
	if err := osManager.LoadFromFile(osDataPath()); err != nil {
		log.Printf("Warning: Failed to load OS assignments from %s: %v", osDataPath(), err)
	}
	
	r := gin.Default()

	// Serve embedded static files
	r.GET("/static/*filepath", func(c *gin.Context) {
		serveEmbeddedFile(c)
	})

	api := r.Group("/api/v1")
	{
		api.GET("/servers", getServers)
		api.GET("/servers/:id", getServer)
		api.POST("/servers/:id/power", serverPower)
		api.POST("/servers/:id/reboot", serverReboot)
		api.GET("/servers/:id/status", serverStatus)
		api.POST("/servers/:id/ipmi", serverIPMI)
		api.GET("/status", getAllServerStatus)
		api.POST("/pdu/:outlet/power", pduPower)
		api.GET("/boot/:mac", getBootScript)
		api.GET("/autoexec.ipxe", getAutoExecScript)
		
		// Job queue endpoints
		api.GET("/jobs", getAllJobs)
		api.GET("/jobs/:id", getJob)
		api.GET("/servers/:id/jobs", getServerJobs)
		api.DELETE("/jobs/:id", cancelJob)
		api.POST("/jobs/:id/run-now", runJobNow)
		api.POST("/jobs/:id/retry", retryJob)
		api.POST("/jobs/schedule", scheduleJob)
		
		// OS Management endpoints
		api.GET("/os/templates", getOSTemplates)
		api.GET("/os/templates/:id", getOSTemplate)
		api.POST("/os/templates", createOSTemplate)
		api.PUT("/os/templates/:id", updateOSTemplate)
		api.DELETE("/os/templates/:id", deleteOSTemplate)
		api.GET("/servers/:id/os", getServerOSAssignment)
		api.PUT("/servers/:id/os", assignServerOS)
		api.DELETE("/servers/:id/os", removeServerOSAssignment)
		api.POST("/os/bulk-assign", bulkAssignOS)
		api.GET("/os/assignments", getAllOSAssignments)
	}

	// Serve index.html for root and any non-API routes (SPA routing)
	r.NoRoute(func(c *gin.Context) {
		// Don't serve index.html for API routes
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "API endpoint not found"})
			return
		}
		serveEmbeddedFile(c)
	})
	
	r.GET("/", func(c *gin.Context) {
		serveEmbeddedFile(c)
	})

	// Start background services
	startStatusPoller()
	startJobProcessor()
	
	log.Printf("Lab Manager starting on %s", listenAddr)
	if err := r.Run(listenAddr); err != nil {
		log.Fatal(err)
	}
}

func getServers(c *gin.Context) {
	config, err := loadConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, config.Public())
}

func getServer(c *gin.Context) {
	id := c.Param("id")
	servers, err := loadConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	for _, server := range servers.Public().Servers {
		if server.ID == id {
			c.JSON(http.StatusOK, server)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
}

func serverPower(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	var jobs []*Job
	var err error
	
	switch req.Action {
	case "on":
		jobs, err = createPowerOnSequence(id)
	case "off":
		jobs, err = createPowerOffSequence(id)
	default:
		// For other actions, create a simple IPMI job
		config, configErr := loadConfig()
		if configErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": configErr.Error()})
			return
		}
		
		var server *Server
		for _, s := range config.Servers {
			if s.ID == id {
				server = &s
				break
			}
		}
		
		if server == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
			return
		}
		
		if !validIPMIActions[req.Action] {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid action %q", req.Action)})
			return
		}
		
		description := fmt.Sprintf("IPMI %s %s", req.Action, server.Name)
		job := jobQueue.AddJob(JobIPMI, id, req.Action, "", description)
		jobs = []*Job{job}
	}
	
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	
	// Show transitional status only once the sequence is actually queued
	cache.SetTransitionalStatus(id, req.Action)
	
	c.JSON(http.StatusOK, gin.H{
		"status": "queued",
		"jobs": jobs,
		"message": fmt.Sprintf("Queued %d job(s)", len(jobs)),
	})
	
	// Schedule post-action polling for immediate feedback
	go schedulePostActionPolling(id)
}

func serverReboot(c *gin.Context) {
	id := c.Param("id")
	
	jobs, err := createRebootSequence(id)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	
	cache.SetTransitionalStatus(id, "reboot")
	
	c.JSON(http.StatusOK, gin.H{
		"status": "queued",
		"jobs": jobs,
		"message": fmt.Sprintf("Queued %d job(s) for reboot sequence", len(jobs)),
	})
	
	// Schedule post-action polling for immediate feedback
	go schedulePostActionPolling(id)
}

func serverIPMI(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Validate action
	if !validIPMIActions[req.Action] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid action. Must be one of on, off, reboot, graceful_shutdown, graceful_restart"})
		return
	}
	
	// Create a single IPMI job directly (bypassing complex power sequences)
	job := jobQueue.AddJob(JobIPMI, id, req.Action, "", fmt.Sprintf("Direct IPMI %s for %s", req.Action, id))
	
	c.JSON(http.StatusOK, gin.H{
		"status": "queued",
		"job": job,
		"message": fmt.Sprintf("Queued direct IPMI %s job for %s", req.Action, id),
	})
	
	// Schedule post-action polling for immediate feedback
	go schedulePostActionPolling(id)
}

func serverStatus(c *gin.Context) {
	id := c.Param("id")
	
	// Check if this is a forced refresh (bypass cache)
	forceRefresh := c.Query("force") == "true"
	
	var status string
	var err error
	
	if forceRefresh {
		// Use fresh polling with cache bypass
		go pollServerFresh(id)
		
		// Get the fresh status (might take a moment to update cache)
		time.Sleep(100 * time.Millisecond) // Brief wait for fresh poll
		if cachedStatus, exists := cache.Get(id); exists {
			status = cachedStatus.Status
		} else {
			// Fallback to direct status check
			status, err = getDeviceStatusDirect(id)
		}
	} else {
		// Use cached status, but fall back to device router if not cached
		if cachedStatus, exists := cache.Get(id); exists {
			// If it's transitional and not expired, return transitional status
			if cachedStatus.IsTransitional {
				// Keep transitional status for up to 2 minutes
				if time.Since(cachedStatus.TransitionalSince) < 2*time.Minute {
					status = cachedStatus.Status
				} else {
					// Transitional expired, get fresh status
					status, err = getDeviceStatusDirect(id)
					if err == nil {
						cache.UpdateStatus(id, status, true, nil)
					}
				}
			} else {
				// Normal cached status
				if cachedStatus.Error != "" {
					err = fmt.Errorf(cachedStatus.Error)
				} else {
					status = cachedStatus.Status
				}
			}
		} else {
			// Not in cache, get status using device router
			status, err = getDeviceStatusDirect(id)
			if err == nil {
				cache.UpdateStatus(id, status, true, nil)
			}
		}
	}
	
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

func pduPower(c *gin.Context) {
	outlet := c.Param("outlet")
	var req struct {
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	err := performPDUAction(outlet, req.Action)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

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
	err = tmpl.Execute(&result, vars)
	if err != nil {
		return "", err
	}
	
	return result.String(), nil
}

// getClientIP extracts the real client IP from the request
func getClientIP(c *gin.Context) string {
	clientIP := c.ClientIP()
	if clientIP != "" {
		return clientIP
	}
	
	// Fallback to remote address
	if ip, _, err := net.SplitHostPort(c.Request.RemoteAddr); err == nil {
		return ip
	}
	
	return "unknown"
}

// createTemplateVars creates template variables from server config and request context
func createTemplateVars(server *Server, mac string, c *gin.Context) iPXETemplateVars {
	hostname := server.Metadata["hostname"]
	if hostname == "" {
		hostname = server.Name
	}
	
	return iPXETemplateVars{
		MAC:              mac,
		Hostname:         hostname,
		Role:             server.Metadata["role"],
		ServerName:       server.Name,
		ServerID:         server.ID,
		Description:      server.Description,
		IncomingServerIP: getClientIP(c),
		Metadata:         server.Metadata,
	}
}

// getAutoExecScript handles the standard /autoexec.ipxe endpoint
func getAutoExecScript(c *gin.Context) {
	mac := c.Query("mac")
	if mac == "" {
		// Try to get MAC from other common parameters
		mac = c.Query("uuid")
		if mac == "" {
			c.String(http.StatusBadRequest, "#!ipxe\necho MAC address required (?mac=xx:xx:xx:xx:xx:xx)\nshell")
			return
		}
	}
	
	// Redirect to the main boot script handler
	getBootScriptWithMAC(c, mac)
}

func getBootScript(c *gin.Context) {
	mac := c.Param("mac")
	getBootScriptWithMAC(c, mac)
}

func getBootScriptWithMAC(c *gin.Context, mac string) {
	servers, err := loadConfig()
	if err != nil {
		c.String(http.StatusInternalServerError, "#!ipxe\necho Error loading config\nshell")
		return
	}
	
	var server *Server
	for _, s := range servers.Servers {
		if s.Metadata["mac"] == mac {
			server = &s
			break
		}
	}
	
	if server == nil {
		// Return a fallback script for unknown servers
		fallbackScript := fmt.Sprintf(`#!ipxe

echo Lab Manager - Unknown server
echo MAC: %s
echo No configuration found, booting from local disk

# Boot from local disk
sanboot --no-describe --drive 0x80 ||

# Final fallback
echo Boot failed
shell
`, mac)
		c.Header("Content-Type", "text/plain")
		c.String(http.StatusOK, fallbackScript)
		return
	}
	
	// Create template variables
	vars := createTemplateVars(server, mac, c)
	
	// 1. PRIORITY: Check for dynamic OS assignment first
	if assignment, exists := osManager.GetServerOSAssignment(server.ID); exists {
		if template, templateExists := osManager.GetTemplate(assignment.TemplateID); templateExists {
			log.Printf("Using OS assignment %s for server %s (%s)", template.Name, server.ID, mac)
			
			bootScript := generateBootScriptFromTemplate(template, vars, server, mac)
			c.Header("Content-Type", "text/plain")
			c.String(http.StatusOK, bootScript)
			return
		} else {
			log.Printf("Warning: OS template %s not found for server %s, falling back", assignment.TemplateID, server.ID)
		}
	}
	
	// 2. FALLBACK: Check static metadata (next-path)
	nextPath := server.Metadata["next-path"]
	if nextPath != "" {
		// Process the next-path template
		processedPath, err := processTemplate(nextPath, vars)
		if err != nil {
			log.Printf("Error processing next-path template for %s: %v", server.ID, err)
			processedPath = nextPath // Use unprocessed version as fallback
		}
		
		// Generate chain loading script
		bootScript := fmt.Sprintf(`#!ipxe

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
		
		c.Header("Content-Type", "text/plain")
		c.String(http.StatusOK, bootScript)
		return
	}
	
	// 3. FALLBACK: Check static metadata (boot-template)
	bootTemplate := server.Metadata["boot-template"]
	if bootTemplate != "" {
		// Process custom boot template
		bootScript, err := processTemplate(bootTemplate, vars)
		if err != nil {
			log.Printf("Error processing boot-template for %s: %v", server.ID, err)
			// Fall back to default template
		} else {
			c.Header("Content-Type", "text/plain")
			c.String(http.StatusOK, bootScript)
			return
		}
	}
	
	// 4. DEFAULT: Local boot script
	defaultTemplate := `#!ipxe

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
`
	
	bootScript, err := processTemplate(defaultTemplate, vars)
	if err != nil {
		log.Printf("Error processing default boot template: %v", err)
		// Fallback to static script
		bootScript = fmt.Sprintf(`#!ipxe

echo Lab Manager - Booting %s
echo MAC: %s
echo Role: %s

sanboot --no-describe --drive 0x80 ||
echo Boot failed
shell
`, server.Name, mac, server.Metadata["role"])
	}
	
	c.Header("Content-Type", "text/plain")
	c.String(http.StatusOK, bootScript)
}

// generateBootScriptFromTemplate generates an iPXE boot script from an OS template
func generateBootScriptFromTemplate(template OSTemplate, vars iPXETemplateVars, server *Server, mac string) string {
	switch template.BootMethod {
	case "local":
		// Local disk boot
		return fmt.Sprintf(`#!ipxe

echo Lab Manager - OS Template: %s
echo Server: %s
echo MAC: %s
echo Booting from local disk

# Boot from local disk
sanboot --no-describe --drive 0x80 ||

echo Boot failed
shell
`, template.Name, server.Name, mac)

	case "chain":
		// Chain loading to external URL
		if template.NextPath == "" {
			log.Printf("Chain template %s has no next-path", template.ID)
			return generateLocalBootScript(server, mac, "Chain template misconfigured")
		}
		
		// Process the next-path template
		processedPath, err := processTemplate(template.NextPath, vars)
		if err != nil {
			log.Printf("Error processing chain template %s next-path: %v", template.ID, err)
			processedPath = template.NextPath // Use unprocessed version as fallback
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
`, template.Name, server.Name, mac, server.Metadata["role"], vars.Hostname, processedPath, processedPath)

	case "template":
		// Custom iPXE template
		if template.BootTemplate == "" {
			log.Printf("Template %s has no boot-template", template.ID)
			return generateLocalBootScript(server, mac, "Boot template empty")
		}
		
		// Add template header
		bootScript := fmt.Sprintf(`#!ipxe

echo Lab Manager - OS Template: %s
echo Server: %s
echo MAC: %s
echo Role: %s
echo Hostname: %s

# Custom template begins
`, template.Name, server.Name, mac, server.Metadata["role"], vars.Hostname)
		
		// Process custom boot template
		processedTemplate, err := processTemplate(template.BootTemplate, vars)
		if err != nil {
			log.Printf("Error processing boot template %s: %v", template.ID, err)
			return generateLocalBootScript(server, mac, "Template processing failed")
		}
		
		return bootScript + processedTemplate

	default:
		log.Printf("Unknown boot method %s for template %s", template.BootMethod, template.ID)
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

func getAllServerStatus(c *gin.Context) {
	// Check if this is a forced refresh (bypass cache)
	forceRefresh := c.Query("force") == "true"
	
	if forceRefresh {
		// Use fresh polling for all servers
		go refreshAllServersFresh()
		// Brief wait for fresh polling to start
		time.Sleep(200 * time.Millisecond)
	}
	
	statuses := cache.GetAll()
	c.JSON(http.StatusOK, statuses)
}

func getAllJobs(c *gin.Context) {
	jobs := jobQueue.GetAllJobs()
	c.JSON(http.StatusOK, jobs)
}

func getJob(c *gin.Context) {
	id := c.Param("id")
	job, exists := jobQueue.GetJob(id) 
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func getServerJobs(c *gin.Context) {
	serverID := c.Param("id")
	jobs := jobQueue.GetJobsForServer(serverID)
	c.JSON(http.StatusOK, jobs)
}

func cancelJob(c *gin.Context) {
	jobID := c.Param("id")
	
	err := jobQueue.CancelJob(jobID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}

func runJobNow(c *gin.Context) {
	jobID := c.Param("id")
	
	err := jobQueue.RunJobNow(jobID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{"status": "marked for immediate execution"})
}

func retryJob(c *gin.Context) {
	jobID := c.Param("id")
	
	err := jobQueue.RetryJob(jobID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{"status": "job retry initiated"})
}

func scheduleJob(c *gin.Context) {
	var req struct {
		ServerID      string `json:"server_id"`
		Action        string `json:"action"`
		ScheduledTime string `json:"scheduled_time"`
		Description   string `json:"description"`
	}
	
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Parse the scheduled time
	scheduledTime, err := time.Parse(time.RFC3339, req.ScheduledTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid scheduled_time format"})
		return
	}
	
	// Validate server exists
	config, err := loadConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	var server *Server
	for _, s := range config.Servers {
		if s.ID == req.ServerID {
			server = &s
			break
		}
	}
	
	if server == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}
	
	// Create the scheduled job
	var jobs []*Job
	
	switch req.Action {
	case "off":
		// For scheduled power off, create power off sequence scheduled for the specified time
		jobs, err = createPowerOffSequenceScheduled(req.ServerID, scheduledTime)
	case "on":
		// For scheduled power on
		jobs, err = createPowerOnSequenceScheduled(req.ServerID, scheduledTime)
	default:
		// For other actions, create a simple scheduled IPMI job
		if !validIPMIActions[req.Action] {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid action %q", req.Action)})
			return
		}
		job := jobQueue.AddScheduledJob(JobIPMI, req.ServerID, req.Action, "", req.Description, scheduledTime)
		jobs = []*Job{job}
	}
	
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"status": "scheduled",
		"jobs": jobs,
		"message": fmt.Sprintf("Scheduled %d job(s) for %s", len(jobs), scheduledTime.Format("2006-01-02 15:04:05")),
	})
}

// OS Management API Handlers

// getOSTemplates returns all OS templates
func getOSTemplates(c *gin.Context) {
	templates := osManager.GetAllTemplates()
	c.JSON(http.StatusOK, gin.H{"templates": templates})
}

// getOSTemplate returns a specific OS template
func getOSTemplate(c *gin.Context) {
	templateID := c.Param("id")
	template, exists := osManager.GetTemplate(templateID)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Template not found"})
		return
	}
	c.JSON(http.StatusOK, template)
}

// createOSTemplate creates a new OS template
func createOSTemplate(c *gin.Context) {
	var template OSTemplate
	if err := c.ShouldBindJSON(&template); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	created, err := osManager.AddTemplate(template)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Save to persistent storage
	saveOSData()
	
	c.JSON(http.StatusCreated, created)
}

// updateOSTemplate updates an existing OS template
func updateOSTemplate(c *gin.Context) {
	templateID := c.Param("id")
	
	var template OSTemplate
	if err := c.ShouldBindJSON(&template); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	template.ID = templateID
	if err := osManager.UpdateTemplate(template); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Save to persistent storage
	saveOSData()
	
	c.JSON(http.StatusOK, template)
}

// deleteOSTemplate deletes an OS template
func deleteOSTemplate(c *gin.Context) {
	templateID := c.Param("id")
	
	if err := osManager.DeleteTemplate(templateID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Save to persistent storage
	saveOSData()
	
	c.JSON(http.StatusOK, gin.H{"message": "Template deleted"})
}

// getServerOSAssignment returns the OS assignment for a server
func getServerOSAssignment(c *gin.Context) {
	serverID := c.Param("id")
	
	assignment, exists := osManager.GetServerOSAssignment(serverID)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "No OS assignment found"})
		return
	}
	
	// Also include template details
	template, _ := osManager.GetTemplate(assignment.TemplateID)
	c.JSON(http.StatusOK, gin.H{
		"assignment": assignment,
		"template": template,
	})
}

// assignServerOS assigns an OS template to a server
func assignServerOS(c *gin.Context) {
	serverID := c.Param("id")
	
	var req struct {
		TemplateID string `json:"template_id" binding:"required"`
		AssignedBy string `json:"assigned_by,omitempty"`
	}
	
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Verify server exists
	config, err := loadConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	var serverExists bool
	for _, server := range config.Servers {
		if server.ID == serverID {
			serverExists = true
			break
		}
	}
	
	if !serverExists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}
	
	if err := osManager.AssignOSToServer(serverID, req.TemplateID, req.AssignedBy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Save to persistent storage
	saveOSData()
	
	c.JSON(http.StatusOK, gin.H{"message": "OS assigned to server"})
}

// removeServerOSAssignment removes OS assignment from a server
func removeServerOSAssignment(c *gin.Context) {
	serverID := c.Param("id")
	osManager.RemoveServerOSAssignment(serverID)
	
	// Save to persistent storage
	saveOSData()
	
	c.JSON(http.StatusOK, gin.H{"message": "OS assignment removed"})
}

// bulkAssignOS assigns OS to multiple servers
func bulkAssignOS(c *gin.Context) {
	var req struct {
		ServerIDs  []string `json:"server_ids" binding:"required"`
		TemplateID string   `json:"template_id" binding:"required"`
		AssignedBy string   `json:"assigned_by,omitempty"`
	}
	
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	if err := osManager.BulkAssignOS(req.ServerIDs, req.TemplateID, req.AssignedBy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Save to persistent storage
	saveOSData()
	
	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("OS assigned to %d servers", len(req.ServerIDs)),
		"assigned_servers": len(req.ServerIDs),
	})
}

// getAllOSAssignments returns all server OS assignments
func getAllOSAssignments(c *gin.Context) {
	assignments := osManager.GetAllAssignments()
	
	// Enrich with template details
	enrichedAssignments := make([]gin.H, len(assignments))
	for i, assignment := range assignments {
		template, _ := osManager.GetTemplate(assignment.TemplateID)
		enrichedAssignments[i] = gin.H{
			"assignment": assignment,
			"template": template,
		}
	}
	
	c.JSON(http.StatusOK, gin.H{"assignments": enrichedAssignments})
}

// osDataPath returns where OS templates/assignments persist: next to the config, e.g. servers.os.json
func osDataPath() string {
	return strings.TrimSuffix(configPath, filepath.Ext(configPath)) + ".os.json"
}

// saveOSData saves OS assignments to persistent storage
func saveOSData() {
	if err := osManager.SaveToFile(osDataPath()); err != nil {
		log.Printf("Warning: Failed to save OS assignments to %s: %v", osDataPath(), err)
	}
}