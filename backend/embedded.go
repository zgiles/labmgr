package main

import (
	"embed"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:static
var embeddedFiles embed.FS

// serveEmbeddedFile serves embedded static files
func serveEmbeddedFile(c *gin.Context) {
	path := c.Request.URL.Path
	
	// Remove leading slash and add static prefix
	cleanPath := strings.TrimPrefix(path, "/")
	if cleanPath == "" {
		cleanPath = "index.html"
	}
	
	// Try to read the file from embedded filesystem
	fullPath := filepath.Join("static", cleanPath)
	data, err := embeddedFiles.ReadFile(fullPath)
	if err != nil {
		// If file not found, serve index.html for SPA routing
		if strings.HasSuffix(cleanPath, ".html") || !strings.Contains(cleanPath, ".") {
			data, err = embeddedFiles.ReadFile("static/index.html")
			if err != nil {
				c.String(http.StatusNotFound, "404 Not Found")
				return
			}
			c.Header("Content-Type", "text/html")
		} else {
			c.String(http.StatusNotFound, "404 Not Found")
			return
		}
	} else {
		// Set appropriate content type based on file extension
		ext := filepath.Ext(cleanPath)
		switch ext {
		case ".js":
			c.Header("Content-Type", "application/javascript")
		case ".css":
			c.Header("Content-Type", "text/css")
		case ".html":
			c.Header("Content-Type", "text/html")
		case ".png":
			c.Header("Content-Type", "image/png")
		case ".jpg", ".jpeg":
			c.Header("Content-Type", "image/jpeg")
		case ".svg":
			c.Header("Content-Type", "image/svg+xml")
		case ".ico":
			c.Header("Content-Type", "image/x-icon")
		}
	}
	
	c.Data(http.StatusOK, c.GetHeader("Content-Type"), data)
}

// getEmbeddedStaticFS returns the embedded filesystem for static files
func getEmbeddedStaticFS() (http.FileSystem, error) {
	sub, err := fs.Sub(embeddedFiles, "static")
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}