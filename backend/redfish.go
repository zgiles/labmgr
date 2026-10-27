package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/stmcginnis/gofish"
	"github.com/stmcginnis/gofish/schemas"
)

// redfishBMC talks to a BMC over Redfish. It uses HTTP basic auth rather than
// sessions so frequent polling doesn't exhaust the BMC's session slots.
type redfishBMC struct {
	config IPMIConfig
}

func (b *redfishBMC) connect(ctx context.Context) (*gofish.APIClient, error) {
	endpoint := b.config.Host
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	c, err := gofish.ConnectContext(ctx, gofish.ClientConfig{
		Endpoint:  endpoint,
		Username:  b.config.Username,
		Password:  b.config.Password,
		Insecure:  true,
		BasicAuth: true,
	})
	if err != nil {
		return nil, friendlyError(err)
	}
	return c, nil
}

// friendlyError turns common transport and auth failures into short, plain explanations
func friendlyError(err error) error {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "no route to host"):
		return fmt.Errorf("no route to host (is it plugged in and on the network?)")
	case strings.Contains(lower, "connection refused"):
		return fmt.Errorf("connection refused (BMC web service down or still booting)")
	case strings.Contains(lower, "i/o timeout"), strings.Contains(lower, "deadline exceeded"), strings.Contains(lower, "timeout awaiting"):
		return fmt.Errorf("timed out waiting for the BMC")
	case strings.Contains(lower, "401"), strings.Contains(lower, "unauthorized"):
		return fmt.Errorf("login rejected (check the BMC username and password)")
	case strings.Contains(lower, "no such host"):
		return fmt.Errorf("BMC hostname doesn't resolve")
	case strings.Contains(lower, "network is unreachable"):
		return fmt.Errorf("network unreachable from Lab Manager")
	}
	return err
}

func (b *redfishBMC) withSystem(ctx context.Context, fn func(*gofish.APIClient, *schemas.ComputerSystem) error) error {
	c, err := b.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Logout()

	systems, err := c.Service.Systems()
	if err != nil {
		return fmt.Errorf("list systems: %w", err)
	}
	if len(systems) == 0 {
		return fmt.Errorf("BMC reports no systems")
	}
	return fn(c, systems[0])
}

func (b *redfishBMC) PowerState(ctx context.Context) (PowerState, error) {
	state := PowerUnknown
	err := b.withSystem(ctx, func(_ *gofish.APIClient, system *schemas.ComputerSystem) error {
		switch system.PowerState {
		case schemas.OnPowerState, schemas.PoweringOffPowerState:
			state = PowerOn
		case schemas.OffPowerState, schemas.PoweringOnPowerState:
			state = PowerOff
		}
		return nil
	})
	return state, err
}

var resetTypes = map[string]schemas.ResetType{
	ActionOn:               schemas.OnResetType,
	ActionOff:              schemas.ForceOffResetType,
	ActionReboot:           schemas.ForceRestartResetType,
	ActionGracefulShutdown: schemas.GracefulShutdownResetType,
	ActionGracefulRestart:  schemas.GracefulRestartResetType,
	ActionPowerCycle:       schemas.PowerCycleResetType,
	ActionNMI:              schemas.NmiResetType,
}

func (b *redfishBMC) Reset(ctx context.Context, action string) error {
	resetType, ok := resetTypes[action]
	if !ok {
		return fmt.Errorf("unknown action: %s", action)
	}
	return b.withSystem(ctx, func(_ *gofish.APIClient, system *schemas.ComputerSystem) error {
		_, err := system.Reset(resetType)
		return err
	})
}

var bootSources = map[string]schemas.BootSource{
	BootPXE:  schemas.PxeBootSource,
	BootDisk: schemas.HddBootSource,
	BootBIOS: schemas.BiosSetupBootSource,
	BootCD:   schemas.CdBootSource,
	BootNone: schemas.NoneBootSource,
}

func (b *redfishBMC) BootOverride(ctx context.Context) (BootOverride, error) {
	var override BootOverride
	err := b.withSystem(ctx, func(_ *gofish.APIClient, system *schemas.ComputerSystem) error {
		override.Target = string(system.Boot.BootSourceOverrideTarget)
		for name, source := range bootSources {
			if source == system.Boot.BootSourceOverrideTarget {
				override.Target = name
			}
		}
		override.Enabled = string(system.Boot.BootSourceOverrideEnabled)
		override.Mode = string(system.Boot.BootSourceOverrideMode)
		return nil
	})
	return override, err
}

func (b *redfishBMC) SetBootOverride(ctx context.Context, target string, persistent bool) error {
	source, ok := bootSources[target]
	if !ok {
		return fmt.Errorf("unknown boot target: %s", target)
	}
	enabled := schemas.OnceBootSourceOverrideEnabled
	if persistent {
		enabled = schemas.ContinuousBootSourceOverrideEnabled
	}
	if target == BootNone {
		enabled = schemas.DisabledBootSourceOverrideEnabled
	}
	return b.withSystem(ctx, func(_ *gofish.APIClient, system *schemas.ComputerSystem) error {
		return system.SetBoot(&schemas.Boot{
			BootSourceOverrideTarget:  source,
			BootSourceOverrideEnabled: enabled,
		})
	})
}

func (b *redfishBMC) Info(ctx context.Context) (BMCInfo, error) {
	var info BMCInfo
	err := b.withSystem(ctx, func(c *gofish.APIClient, system *schemas.ComputerSystem) error {
		info.Manufacturer = system.Manufacturer
		info.Model = system.Model
		info.SerialNumber = system.SerialNumber
		info.BIOSVersion = system.BiosVersion
		info.Health = string(system.Status.Health)
		info.SerialSSH = system.SerialConsole.SSH.ServiceEnabled
		info.SerialIPMI = system.SerialConsole.IPMI.ServiceEnabled
		info.GraphicalKVM = system.GraphicalConsole.ServiceEnabled

		if managers, err := c.Service.Managers(); err == nil && len(managers) > 0 {
			info.BMCModel = managers[0].Model
			info.BMCFirmware = managers[0].FirmwareVersion
		}
		return nil
	})
	return info, err
}

// Logs returns the most recent entries from the BMC's event logs, newest first
func (b *redfishBMC) Logs(ctx context.Context, limit int) ([]BMCLogEntry, error) {
	entries := []BMCLogEntry{}
	err := b.withSystem(ctx, func(c *gofish.APIClient, system *schemas.ComputerSystem) error {
		var services []*schemas.LogService
		if managers, err := c.Service.Managers(); err == nil && len(managers) > 0 {
			if s, err := managers[0].LogServices(); err == nil {
				services = append(services, s...)
			}
		}
		if s, err := system.LogServices(); err == nil {
			services = append(services, s...)
		}

		for _, service := range services {
			// Hardware event logs are what people want here; names vary by vendor
			// (iDRAC Sel/Lclog/FaultList, XCC StandardLog/ActiveLog), so skip the
			// known non-event services rather than guess every event log's name
			id := strings.ToLower(service.ID)
			if strings.Contains(id, "audit") || strings.Contains(id, "maint") || strings.Contains(id, "debug") ||
				strings.Contains(id, "dump") || strings.Contains(id, "crash") {
				continue
			}
			logEntries, err := fetchLogEntries(ctx, c, service.ODataID+"/Entries", 200)
			if err != nil {
				continue
			}
			for _, e := range logEntries {
				if isLogNoise(e.Message) {
					continue
				}
				entries = append(entries, e)
			}
		}
		return nil
	})
	// iDRAC records many events in both its SEL and lifecycle log
	seen := make(map[string]bool)
	unique := entries[:0]
	for _, e := range entries {
		key := e.Created + "|" + e.Message
		if !seen[key] {
			seen[key] = true
			unique = append(unique, e)
		}
	}
	entries = unique
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Created > entries[j].Created })
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, err
}

// isLogNoise drops BMC log lines generated by our own polling and other routine chatter
func isLogNoise(message string) bool {
	m := strings.ToLower(message)
	return strings.Contains(m, "successfully logged in") ||
		strings.Contains(m, "logged out") ||
		strings.Contains(m, "logged off") ||
		strings.Contains(m, "previous log entry was repeated") ||
		strings.Contains(m, "login success")
}

type rawLogEntry struct {
	ODataID  string `json:"@odata.id"`
	Created  string `json:"Created"`
	Severity string `json:"Severity"`
	Message  string `json:"Message"`
}

// fetchLogEntries reads up to top entries of a log collection. Some BMCs (iDRAC) return
// entries inline; others (XCC) return only links, which gofish would fetch one by one
// serially. Links are fetched here a few at a time instead.
func fetchLogEntries(ctx context.Context, c *gofish.APIClient, uri string, top int) ([]BMCLogEntry, error) {
	getJSON := func(url string, into any) error {
		resp, err := c.Get(url)
		if err != nil {
			return err
		}
		defer schemas.DeferredCleanupHTTPResponse(resp)
		return json.NewDecoder(resp.Body).Decode(into)
	}

	var collection struct {
		Members []rawLogEntry `json:"Members"`
	}
	if err := getJSON(fmt.Sprintf("%s?$top=%d", uri, top), &collection); err != nil {
		return nil, err
	}
	members := collection.Members
	if len(members) > top {
		members = members[:top]
	}

	entries := make([]BMCLogEntry, len(members))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, m := range members {
		if m.Message != "" || m.Created != "" || m.ODataID == "" {
			entries[i] = BMCLogEntry{Created: m.Created, Severity: m.Severity, Message: m.Message}
			continue
		}
		wg.Add(1)
		go func(i int, link string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			var full rawLogEntry
			if getJSON(link, &full) == nil {
				entries[i] = BMCLogEntry{Created: full.Created, Severity: full.Severity, Message: full.Message}
			}
		}(i, m.ODataID)
	}
	wg.Wait()

	out := entries[:0]
	for _, e := range entries {
		if e.Message != "" {
			out = append(out, e)
		}
	}
	return out, ctx.Err()
}

// KVM returns a browser URL for the BMC's graphical console. On Dell iDRAC9 it
// asks for a one-time console credential (GetKVMSession) so the virtual console
// opens without a login and the real password never reaches the browser.
// Other BMCs get their web UI, where the user logs in once.
func (b *redfishBMC) KVM(ctx context.Context) (KVMLaunch, error) {
	base := b.config.Host
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	base = strings.TrimSuffix(base, "/")
	launch := KVMLaunch{URL: base + "/", Kind: "login", Note: "Log in to the BMC, then open its remote console."}

	err := b.withSystem(ctx, func(c *gofish.APIClient, system *schemas.ComputerSystem) error {
		if !strings.Contains(strings.ToLower(system.Manufacturer), "dell") {
			return nil
		}
		resp, err := c.Post("/redfish/v1/Managers/iDRAC.Embedded.1/Oem/Dell/DelliDRACCardService/Actions/DelliDRACCardService.GetKVMSession",
			map[string]string{"SessionTypeName": "idrac_cert_file.txt"})
		if err != nil {
			launch.Note = "This iDRAC couldn't issue a one-time console login (needs iDRAC9 with an Enterprise license); log in to open its virtual console."
			return nil
		}
		defer schemas.DeferredCleanupHTTPResponse(resp)
		var session struct {
			TempUsername string
			TempPassword string
		}
		if err := json.NewDecoder(resp.Body).Decode(&session); err != nil || session.TempUsername == "" {
			return nil
		}
		launch = KVMLaunch{URL: idracConsoleURL(base, b.config.Username, session.TempUsername, session.TempPassword), Kind: "direct"}
		return nil
	})
	return launch, err
}

// idracConsoleURL builds the iDRAC virtual console link the way Dell's own launcher does:
// parameters in this order, values unencoded. The iDRAC reads the query literally, so
// percent-encoding the one-time password makes it reject the login. Only characters that
// would break the URL itself are escaped.
func idracConsoleURL(base, username, tempUsername, tempPassword string) string {
	literal := strings.NewReplacer("%", "%25", "&", "%26", "#", "%23", " ", "%20")
	return fmt.Sprintf("%s/console?username=%s&tempUsername=%s&tempPassword=%s",
		base, literal.Replace(username), literal.Replace(tempUsername), literal.Replace(tempPassword))
}

// PowerWatts reads the system's current power draw. Chassis Power (PowerControl) is
// what iDRAC and XCC report; newer firmware may only offer EnvironmentMetrics.
func (b *redfishBMC) PowerWatts(ctx context.Context) (float64, error) {
	var watts float64
	found := false
	err := b.withSystem(ctx, func(_ *gofish.APIClient, system *schemas.ComputerSystem) error {
		chassis, err := system.Chassis()
		if err != nil {
			return err
		}
		for _, ch := range chassis {
			if power, err := ch.Power(); err == nil && power != nil {
				for _, pc := range power.PowerControl {
					if pc.PowerConsumedWatts != nil {
						watts, found = float64(*pc.PowerConsumedWatts), true
						return nil
					}
				}
			}
			if metrics, err := ch.EnvironmentMetrics(); err == nil && metrics != nil && metrics.PowerWatts.Reading != nil {
				watts, found = *metrics.PowerWatts.Reading, true
				return nil
			}
		}
		return nil
	})
	if err == nil && !found {
		err = fmt.Errorf("BMC doesn't report power draw")
	}
	return watts, err
}
