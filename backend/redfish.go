package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

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
	var entries []BMCLogEntry
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
			// Event logs are what people want here; skip audit/debug/crash-dump services
			id := strings.ToLower(service.ID)
			if !strings.Contains(id, "sel") && !strings.Contains(id, "event") && !strings.Contains(id, "lclog") && !strings.Contains(id, "platform") {
				continue
			}
			// Bound the fetch: lifecycle logs can hold thousands of entries
			logEntries, err := service.FilteredEntries(schemas.WithTop(200))
			if err != nil {
				if logEntries, err = service.Entries(); err != nil {
					continue
				}
			}
			for _, e := range logEntries {
				if isLogNoise(e.Message) {
					continue
				}
				entries = append(entries, BMCLogEntry{Created: e.Created, Severity: string(e.Severity), Message: e.Message})
			}
		}
		return nil
	})
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
