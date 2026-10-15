package main

import (
	"embed"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:static
var embeddedFiles embed.FS

// serveEmbeddedFile serves the built frontend; unknown non-file paths get index.html (SPA routing)
func serveEmbeddedFile(c *gin.Context) {
	cleanPath := strings.TrimPrefix(path.Clean(c.Request.URL.Path), "/")
	if cleanPath == "" || cleanPath == "." {
		cleanPath = "index.html"
	}

	data, err := embeddedFiles.ReadFile("static/" + cleanPath)
	if err != nil {
		if path.Ext(cleanPath) != "" && path.Ext(cleanPath) != ".html" {
			c.String(http.StatusNotFound, "404 Not Found")
			return
		}
		cleanPath = "index.html"
		if data, err = embeddedFiles.ReadFile("static/index.html"); err != nil {
			c.String(http.StatusNotFound, "404 Not Found")
			return
		}
	}

	contentType := mime.TypeByExtension(path.Ext(cleanPath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if strings.HasPrefix(cleanPath, "assets/") {
		// Vite puts a content hash in asset names
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "no-cache")
	}
	c.Data(http.StatusOK, contentType, data)
}
