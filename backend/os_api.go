package main

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

func registerOSRoutes(api *gin.RouterGroup, a *App) {
	api.GET("/os/templates", getOSTemplates)
	api.GET("/os/templates/:id", getOSTemplate)
	api.POST("/os/templates", a.createOSTemplate)
	api.PUT("/os/templates/:id", a.updateOSTemplate)
	api.DELETE("/os/templates/:id", a.deleteOSTemplate)
	api.GET("/servers/:id/os", getServerOSAssignment)
	api.PUT("/servers/:id/os", a.assignServerOS)
	api.DELETE("/servers/:id/os", a.removeServerOSAssignment)
	api.POST("/os/bulk-assign", a.bulkAssignOS)
	api.GET("/os/assignments", getAllOSAssignments)
}

// osChanged persists OS data, notes it in the activity log, and tells clients to refresh
func (a *App) osChanged(serverID, message, actor string) {
	saveOSData()
	a.recordEvent(&Event{ServerID: serverID, Kind: "os", Message: message, Actor: actor, Level: "info"})
	a.hub.Publish("os", nil)
}

func getOSTemplates(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"templates": osManager.GetAllTemplates()})
}

func getOSTemplate(c *gin.Context) {
	tmpl, exists := osManager.GetTemplate(c.Param("id"))
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Template not found"})
		return
	}
	c.JSON(http.StatusOK, tmpl)
}

func validateTemplate(t OSTemplate) error {
	switch t.BootMethod {
	case "local":
	case "chain":
		if strings.TrimSpace(t.NextPath) == "" {
			return fmt.Errorf("chain templates need a next path")
		}
	case "template":
		if strings.TrimSpace(t.BootTemplate) == "" {
			return fmt.Errorf("template boot method needs an iPXE script")
		}
	default:
		return fmt.Errorf("boot_method must be local, chain, or template")
	}
	// Catch template syntax errors at save time rather than at boot time
	vars := iPXETemplateVars{Metadata: map[string]string{}}
	for _, text := range []string{t.NextPath, t.BootTemplate} {
		if _, err := processTemplate(text, vars); err != nil {
			return fmt.Errorf("template syntax: %v", err)
		}
	}
	return nil
}

func (a *App) createOSTemplate(c *gin.Context) {
	var tmpl OSTemplate
	if err := c.ShouldBindJSON(&tmpl); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateTemplate(tmpl); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	created, err := osManager.AddTemplate(tmpl)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a.osChanged("", fmt.Sprintf("Created OS template %s", created.Name), requester(c, ""))
	c.JSON(http.StatusCreated, created)
}

func (a *App) updateOSTemplate(c *gin.Context) {
	var tmpl OSTemplate
	if err := c.ShouldBindJSON(&tmpl); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tmpl.ID = c.Param("id")
	if err := validateTemplate(tmpl); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := osManager.UpdateTemplate(tmpl); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a.osChanged("", fmt.Sprintf("Updated OS template %s", tmpl.Name), requester(c, ""))
	c.JSON(http.StatusOK, tmpl)
}

func (a *App) deleteOSTemplate(c *gin.Context) {
	id := c.Param("id")
	if err := osManager.DeleteTemplate(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a.osChanged("", fmt.Sprintf("Deleted OS template %s", id), requester(c, ""))
	c.JSON(http.StatusOK, gin.H{"message": "Template deleted"})
}

func getServerOSAssignment(c *gin.Context) {
	assignment, exists := osManager.GetServerOSAssignment(c.Param("id"))
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "No OS assignment found"})
		return
	}
	tmpl, _ := osManager.GetTemplate(assignment.TemplateID)
	c.JSON(http.StatusOK, gin.H{"assignment": assignment, "template": tmpl})
}

func (a *App) assignServerOS(c *gin.Context) {
	serverID := c.Param("id")
	var req struct {
		TemplateID string `json:"template_id" binding:"required"`
		AssignedBy string `json:"assigned_by,omitempty"`
		OneShot    bool   `json:"one_shot,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	server := a.config.Get().Server(serverID)
	if server == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Server not found"})
		return
	}

	who := requester(c, req.AssignedBy)
	var err error
	if req.OneShot {
		err = osManager.AssignOneShot(serverID, req.TemplateID, who)
	} else {
		err = osManager.AssignOSToServer(serverID, req.TemplateID, who)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tmpl, _ := osManager.GetTemplate(req.TemplateID)
	message := fmt.Sprintf("Network boot set to %s", tmpl.Name)
	if req.OneShot {
		message = fmt.Sprintf("Next network boot only: %s", tmpl.Name)
	}
	a.osChanged(serverID, message, who)
	c.JSON(http.StatusOK, gin.H{"message": "OS assigned to server"})
}

func (a *App) removeServerOSAssignment(c *gin.Context) {
	serverID := c.Param("id")
	osManager.RemoveServerOSAssignment(serverID)
	a.osChanged(serverID, "Network boot set to local disk", requester(c, ""))
	c.JSON(http.StatusOK, gin.H{"message": "OS assignment removed"})
}

func (a *App) bulkAssignOS(c *gin.Context) {
	var req struct {
		ServerIDs  []string `json:"server_ids" binding:"required"`
		TemplateID string   `json:"template_id" binding:"required"`
		AssignedBy string   `json:"assigned_by,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	config := a.config.Get()
	for _, id := range req.ServerIDs {
		if config.Server(id) == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("server %s not found", id)})
			return
		}
	}

	who := requester(c, req.AssignedBy)
	if err := osManager.BulkAssignOS(req.ServerIDs, req.TemplateID, who); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tmpl, _ := osManager.GetTemplate(req.TemplateID)
	for _, id := range req.ServerIDs {
		a.recordEvent(&Event{ServerID: id, Kind: "os", Message: "Network boot set to " + tmpl.Name, Actor: who, Level: "info"})
	}
	saveOSData()
	a.hub.Publish("os", nil)
	c.JSON(http.StatusOK, gin.H{
		"message":          fmt.Sprintf("OS assigned to %d servers", len(req.ServerIDs)),
		"assigned_servers": len(req.ServerIDs),
	})
}

func getAllOSAssignments(c *gin.Context) {
	assignments := osManager.GetAllAssignments()
	enriched := make([]gin.H, len(assignments))
	for i, assignment := range assignments {
		tmpl, _ := osManager.GetTemplate(assignment.TemplateID)
		enriched[i] = gin.H{"assignment": assignment, "template": tmpl}
	}
	c.JSON(http.StatusOK, gin.H{"assignments": enriched})
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
