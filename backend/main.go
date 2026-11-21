package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

const version = "2.0.0"

var configPath string
var osDataFile string // OS templates and assignments; default next to the config
var debugMode bool
var osManager *OSManager

// App holds the running services that HTTP handlers use
type App struct {
	config   *ConfigStore
	state    *StateStore
	poller   *Poller
	engine   *Engine
	store    *Store
	hub      *Hub
	hw       Hardware
	consoles *ConsoleHub
}

func main() {
	var listenAddr, dataPath string
	flag.StringVar(&configPath, "config", "config/servers.yaml", "Path to configuration file")
	flag.StringVar(&listenAddr, "listen", "0.0.0.0:8098", "Address to listen on")
	flag.StringVar(&dataPath, "data", "", "Operations/activity database (default: next to the config, e.g. config/servers.db)")
	flag.StringVar(&osDataFile, "os-data", "", "Boot images and assignments file (default: next to the config, e.g. config/servers.os.json)")
	flag.BoolVar(&debugMode, "debug", false, "Enable debug logging")
	flag.Parse()

	if dataPath == "" {
		dataPath = strings.TrimSuffix(configPath, filepath.Ext(configPath)) + ".db"
	}

	config, err := NewConfigStore(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	osManager = NewOSManager()
	if err := osManager.LoadFromFile(osDataPath()); err != nil {
		log.Printf("Warning: Failed to load OS assignments from %s: %v", osDataPath(), err)
	}

	store, err := OpenStore(dataPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer store.Close()

	hub := NewHub()
	state := NewStateStore(hub)
	state.Sync(config.Get())
	config.OnReload(state.Sync)
	config.OnReload(func(*Config) {
		event := &Event{Kind: "config", Message: "Configuration reloaded", Level: "info"}
		store.AppendEvent(event)
		hub.Publish("event", event)
		hub.Publish("config", nil)
	})

	hw := newRealHardware()
	poller := NewPoller(config, hw, state)
	poller.store = store
	engine := NewEngine(config, hw, state, poller, store, hub, osManager)
	if err := engine.Load(); err != nil {
		log.Fatalf("Failed to load operations: %v", err)
	}

	consoles := NewConsoleHub(config, hw)
	consoles.dir = filepath.Join(filepath.Dir(dataPath), "console")
	consoles.state = state
	engine.onStart = consoles.captureForOperation

	app := &App{config: config, state: state, poller: poller, engine: engine, store: store, hub: hub, hw: hw, consoles: consoles}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go poller.Run(ctx)
	go engine.Run(ctx)
	go watchConfig(ctx, config)
	go consoles.Run(ctx)

	server := &http.Server{
		Addr:        listenAddr,
		Handler:     app.router(),
		BaseContext: func(net.Listener) context.Context { return ctx }, // ends SSE streams on shutdown
	}
	go func() {
		log.Printf("Lab Manager %s listening on %s (config %s, data %s)", version, listenAddr, configPath, dataPath)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(shutdownCtx)
}

// watchConfig reloads on SIGHUP and whenever the file changes
func watchConfig(ctx context.Context, config *ConfigStore) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			if err := config.Reload(); err != nil {
				log.Printf("Config reload failed, keeping previous config: %v", err)
			}
		case <-ticker.C:
			config.reloadIfChanged()
		}
	}
}

func (a *App) router() http.Handler {
	if !debugMode {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger())

	v1 := r.Group("/api/v1")
	v1.Use(a.resolveServerParam())
	a.registerV1(v1)
	v2 := r.Group("/api/v2")
	v2.Use(a.resolveServerParam())
	a.registerV2(v2)

	r.GET("/kvm/:id/", a.kvmPage)
	r.GET("/kvm/:id/sdk/:file", a.kvmSDK)
	r.GET("/kvm/:id/ws/*rest", a.kvmRelay)
	r.GET("/boot.ipxe", serveBootstrap)
	r.GET("/static/boot.ipxe", serveBootstrap)
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "API endpoint not found"})
			return
		}
		serveEmbeddedFile(c)
	})
	return r
}

// requestLogger logs changes and errors, not the steady stream of status reads
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if c.Request.Method == http.MethodGet && c.Writer.Status() < 400 && !debugMode {
			return
		}
		log.Printf("%s %s %d %s %s", c.Request.Method, c.Request.URL.Path, c.Writer.Status(), time.Since(start).Round(time.Millisecond), c.ClientIP())
	}
}
