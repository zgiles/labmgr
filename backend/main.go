package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/gin-gonic/gin"
)

var configPath string
var debugMode bool

func main() {
	flag.StringVar(&configPath, "config", "config/servers.yaml", "Path to configuration file")
	flag.BoolVar(&debugMode, "debug", false, "Enable debug logging")
	flag.Parse()
	
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
	
	log.Println("Lab Manager starting on 0.0.0.0:8098")
	r.Run("0.0.0.0:8098")
}

func getServers(c *gin.Context) {
	servers, err := loadConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, servers)
}

func getServer(c *gin.Context) {
	id := c.Param("id")
	servers, err := loadConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	for _, server := range servers.Servers {
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
	
	// Set transitional status immediately
	cache.SetTransitionalStatus(id, req.Action)
	
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
		
		description := fmt.Sprintf("IPMI %s %s", req.Action, server.Name)
		job := jobQueue.AddJob(JobIPMI, id, req.Action, "", description)
		jobs = []*Job{job}
	}
	
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
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
	
	// Set transitional status immediately
	cache.SetTransitionalStatus(id, "reboot")
	
	jobs, err := createRebootSequence(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
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
	if req.Action != "on" && req.Action != "off" && req.Action != "reboot" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid action. Must be 'on', 'off', or 'reboot'"})
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
	
	// Check if server has a custom next-path template
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

echo Lab Manager - Booting %s
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
	
	// Check if server has a custom boot template
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
	
	// Default boot script template
	defaultTemplate := `#!ipxe

echo Lab Manager - Booting {{.ServerName}}
echo MAC: {{.MAC}}
echo Role: {{.Role}}
echo Hostname: {{.Hostname}}
echo Incoming IP: {{.IncomingServerIP}}

# Set hostname
set hostname {{.Hostname}}

# Boot from network or local disk based on configuration
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