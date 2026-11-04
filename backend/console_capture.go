package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Console recording: after key operations (and optionally all the time) Lab Manager
// keeps the serial console attached and writes what it prints to disk, so a boot or
// an install leaves a log even if nobody was watching.
//
// Recordings live in <dir>/<server>/<YYYYMMDD-HHMMSS>-<operation>.log, or
// <YYYYMMDD>-continuous.log for always-on recording.

const continuousSuffix = "-continuous.log"

type captureFile struct {
	Name    string
	OpID    string
	started time.Time
	until   time.Time // zero: until stopped (continuous)
	file    *os.File
	bytes   int64
}

// CaptureInfo describes a recording for the API
type CaptureInfo struct {
	Name      string    `json:"name"`
	OpID      string    `json:"op_id,omitempty"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Bytes     int64     `json:"bytes"`
	Active    bool      `json:"active"`
	Until     time.Time `json:"until,omitzero"`
}

var captureName = regexp.MustCompile(`^[0-9]{8}(-[0-9]{6}-[A-Za-z0-9._-]+|-continuous)\.log$`)

func (h *ConsoleHub) serverDir(serverID string) string {
	return filepath.Join(h.dir, filepath.Base(serverID))
}

// Capture records a server's console for d (or until stopped, if d is 0), starting
// the session if nobody is viewing it and retrying while the BMC is unreachable
func (h *ConsoleHub) Capture(serverID, opID string, d time.Duration) (string, error) {
	if h.dir == "" {
		return "", errors.New("console recording is disabled")
	}
	if err := os.MkdirAll(h.serverDir(serverID), 0700); err != nil {
		return "", err
	}

	now := time.Now()
	c := &captureFile{OpID: opID, started: now}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if d > 0 {
		c.Name = fmt.Sprintf("%s-%s.log", now.Format("20060102-150405"), opID)
		c.until = now.Add(d)
	} else {
		c.Name = now.Format("20060102") + continuousSuffix
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND // a restart the same day continues the file
	}
	f, err := os.OpenFile(filepath.Join(h.serverDir(serverID), c.Name), flags, 0600)
	if err != nil {
		return "", err
	}
	c.file = f
	if info, err := f.Stat(); err == nil {
		c.bytes = info.Size()
	}

	h.mu.Lock()
	h.captures[serverID] = append(h.captures[serverID], c)
	startHolder := !h.holders[serverID]
	h.holders[serverID] = true
	h.mu.Unlock()

	if startHolder {
		go h.hold(serverID)
	}
	return c.Name, nil
}

// record appends console output to the server's open recordings
func (h *ConsoleHub) record(serverID string, chunk []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.captures[serverID] {
		if n, err := c.file.Write(chunk); err == nil {
			c.bytes += int64(n)
		}
	}
}

// recording reports whether a server has an open recording
func (h *ConsoleHub) recording(serverID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.captures[serverID]) > 0
}

// stopCaptures closes recordings that match; empty files are removed. Caller holds h.mu.
func (h *ConsoleHub) stopCapturesLocked(serverID string, match func(*captureFile) bool) {
	var keep []*captureFile
	for _, c := range h.captures[serverID] {
		if !match(c) {
			keep = append(keep, c)
			continue
		}
		c.file.Close()
		if c.bytes == 0 {
			os.Remove(c.file.Name())
		}
	}
	if len(keep) == 0 {
		delete(h.captures, serverID)
	} else {
		h.captures[serverID] = keep
	}
}

// hold keeps a server's console session up while it has recordings, reconnecting as needed
func (h *ConsoleHub) hold(serverID string) {
	var nextTry time.Time
	for {
		now := time.Now()
		h.mu.Lock()
		h.stopCapturesLocked(serverID, func(c *captureFile) bool { return !c.until.IsZero() && now.After(c.until) })
		active := len(h.captures[serverID]) > 0
		session := h.sessions[serverID]
		if !active {
			delete(h.holders, serverID)
		}
		h.mu.Unlock()

		if !active {
			if session != nil {
				session.shutdownIfUnwatched("recording finished")
			}
			return
		}

		if session == nil && now.After(nextTry) && h.bmcResponding(serverID) {
			ctx, cancel := context.WithTimeout(context.Background(), consoleDialTime+5*time.Second)
			if _, err := h.session(ctx, serverID, 120, 40); err != nil {
				nextTry = now.Add(h.retry)
				log.Printf("Console recording for %s: %v (retrying)", serverID, err)
			}
			cancel()
		}
		time.Sleep(h.tick)
	}
}

func (h *ConsoleHub) bmcResponding(serverID string) bool {
	if h.state == nil {
		return true
	}
	st, ok := h.state.Get(serverID)
	return ok && st.BMC == LinkOK
}

// captureForOperation starts a recording when an operation that reboots a server begins.
// It returns the recording's name, or "" if none was started.
func (h *ConsoleHub) captureForOperation(op Operation) string {
	config := h.config.Get()
	server := config.Server(op.ServerID)
	if h.dir == "" || server == nil || !server.HasBMC() || server.Console.Capture == "off" {
		return ""
	}
	settings := config.ConsoleCapture.WithDefaults()
	wanted := false
	for _, kind := range settings.After {
		if kind == op.Kind {
			wanted = true
		}
	}
	if !wanted {
		return ""
	}
	name, err := h.Capture(op.ServerID, op.ID, time.Duration(settings.Minutes)*time.Minute)
	if err != nil {
		log.Printf("Warning: couldn't start console recording for %s: %v", op.ServerID, err)
		return ""
	}
	return name
}

// Run keeps continuous recordings going (rotating daily) and deletes old recordings
func (h *ConsoleHub) Run(ctx context.Context) {
	if h.dir == "" {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	var lastPrune time.Time
	for {
		h.syncContinuous()
		if time.Since(lastPrune) > time.Hour {
			h.prune()
			lastPrune = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// syncContinuous starts, rotates, or stops always-on recordings to match the config
func (h *ConsoleHub) syncContinuous() {
	config := h.config.Get()
	today := time.Now().Format("20060102") + continuousSuffix
	for i := range config.Servers {
		server := &config.Servers[i]
		want := server.HasBMC() && server.Console.Capture != "off" &&
			(server.Console.Capture == "always" || config.ConsoleCapture.Always)

		h.mu.Lock()
		have := false
		for _, c := range h.captures[server.ID] {
			if c.until.IsZero() && c.Name == today {
				have = true
			}
		}
		// Close yesterday's file, or all continuous recording if it was turned off
		h.stopCapturesLocked(server.ID, func(c *captureFile) bool { return c.until.IsZero() && (!want || c.Name != today) })
		h.mu.Unlock()

		if want && !have {
			if _, err := h.Capture(server.ID, "", 0); err != nil {
				log.Printf("Warning: couldn't start continuous console recording for %s: %v", server.ID, err)
			}
		}
	}
}

// prune deletes recordings older than the retention period
func (h *ConsoleHub) prune() {
	days := h.config.Get().ConsoleCapture.WithDefaults().RetainDays
	cutoff := time.Now().AddDate(0, 0, -days)
	servers, _ := os.ReadDir(h.dir)
	for _, s := range servers {
		files, _ := os.ReadDir(filepath.Join(h.dir, s.Name()))
		for _, f := range files {
			if info, err := f.Info(); err == nil && captureName.MatchString(f.Name()) && info.ModTime().Before(cutoff) {
				os.Remove(filepath.Join(h.dir, s.Name(), f.Name()))
			}
		}
	}
}

// Captures lists a server's recordings, newest first
func (h *ConsoleHub) Captures(serverID string) []CaptureInfo {
	out := []CaptureInfo{}
	if h.dir == "" {
		return out
	}
	files, _ := os.ReadDir(h.serverDir(serverID))
	h.mu.Lock()
	open := make(map[string]*captureFile)
	for _, c := range h.captures[serverID] {
		open[c.Name] = c
	}
	h.mu.Unlock()

	for _, f := range files {
		if !captureName.MatchString(f.Name()) {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		ci := CaptureInfo{Name: f.Name(), Bytes: info.Size(), UpdatedAt: info.ModTime()}
		if t, err := time.ParseInLocation("20060102-150405", f.Name()[:15], time.Local); err == nil && !strings.HasSuffix(f.Name(), continuousSuffix) {
			ci.StartedAt = t
			ci.OpID = strings.TrimSuffix(f.Name()[16:], ".log")
		} else if t, err := time.ParseInLocation("20060102", f.Name()[:8], time.Local); err == nil {
			ci.StartedAt = t
		}
		if c, ok := open[f.Name()]; ok {
			ci.Active = true
			ci.Until = c.until
		}
		out = append(out, ci)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// CapturePath returns the file for a recording name, refusing anything that isn't one
func (h *ConsoleHub) CapturePath(serverID, name string) (string, bool) {
	if h.dir == "" || !captureName.MatchString(name) {
		return "", false
	}
	path := filepath.Join(h.serverDir(serverID), name)
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}
