package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gosnmp/gosnmp"
)

// APC sPDUOutletCtl: reading gives 1=on 2=off, writing 1=on 2=off 3=reboot.
// Works on the AP86xx/AP79xx families.
const apcOutletCtlOID = ".1.3.6.1.4.1.318.1.1.4.4.2.1.3."

var pduCommands = map[string]int{"on": 1, "off": 2, "reboot": 3}

// apcSNMP drives an APC switched PDU over SNMP
type apcSNMP struct {
	pdu PDU
}

func (a *apcSNMP) client(ctx context.Context) *gosnmp.GoSNMP {
	port := uint16(161)
	if a.pdu.SNMPPort > 0 {
		port = uint16(a.pdu.SNMPPort)
	}
	version := gosnmp.Version1
	if a.pdu.SNMPVersion == "v2c" {
		version = gosnmp.Version2c
	}
	return &gosnmp.GoSNMP{
		Context:   ctx,
		Target:    a.pdu.Host,
		Port:      port,
		Community: a.pdu.SNMPCommunity,
		Version:   version,
		Timeout:   4 * time.Second,
		Retries:   1,
		MaxOids:   gosnmp.MaxOids,
	}
}

func (a *apcSNMP) OutletStates(ctx context.Context, outlets []int) (map[int]PowerState, error) {
	snmp := a.client(ctx)
	if err := snmp.Connect(); err != nil {
		return nil, fmt.Errorf("SNMP connect to %s: %w", a.pdu.Host, err)
	}
	defer snmp.Conn.Close()

	states := make(map[int]PowerState, len(outlets))
	for start := 0; start < len(outlets); start += snmp.MaxOids {
		chunk := outlets[start:min(start+snmp.MaxOids, len(outlets))]
		oids := make([]string, len(chunk))
		for i, outlet := range chunk {
			oids[i] = fmt.Sprintf("%s%d", apcOutletCtlOID, outlet)
		}
		result, err := snmp.Get(oids)
		if err != nil {
			return nil, fmt.Errorf("SNMP get from %s: %w", a.pdu.Host, err)
		}
		for i, variable := range result.Variables {
			if i >= len(chunk) {
				break
			}
			switch gosnmp.ToBigInt(variable.Value).Int64() {
			case 1:
				states[chunk[i]] = PowerOn
			case 2:
				states[chunk[i]] = PowerOff
			default:
				states[chunk[i]] = PowerUnknown
			}
		}
	}
	return states, nil
}

func (a *apcSNMP) SetOutlet(ctx context.Context, outlet int, action string) error {
	command, ok := pduCommands[action]
	if !ok {
		return fmt.Errorf("unknown PDU action: %s", action)
	}
	snmp := a.client(ctx)
	if err := snmp.Connect(); err != nil {
		return fmt.Errorf("SNMP connect to %s: %w", a.pdu.Host, err)
	}
	defer snmp.Conn.Close()

	if debugMode {
		log.Printf("DEBUG: SNMP SET %s outlet %d = %d (%s)", a.pdu.Host, outlet, command, action)
	}
	_, err := snmp.Set([]gosnmp.SnmpPDU{{
		Name:  fmt.Sprintf("%s%d", apcOutletCtlOID, outlet),
		Type:  gosnmp.Integer,
		Value: command,
	}})
	if err != nil {
		return fmt.Errorf("set outlet %d %s on %s: %w", outlet, action, a.pdu.Host, err)
	}
	return nil
}

// httpPDU drives PDUs with a simple "/outlet?N=command" web interface.
// They don't expose per-outlet state, so OutletStates reports unknown.
type httpPDU struct {
	pdu PDU
}

func (h *httpPDU) OutletStates(ctx context.Context, outlets []int) (map[int]PowerState, error) {
	states := make(map[int]PowerState, len(outlets))
	for _, outlet := range outlets {
		states[outlet] = PowerUnknown
	}
	return states, nil
}

func (h *httpPDU) SetOutlet(ctx context.Context, outlet int, action string) error {
	command, ok := pduCommands[action]
	if !ok {
		return fmt.Errorf("unknown PDU action: %s", action)
	}
	url := fmt.Sprintf("http://%s/outlet?%d=%d", h.pdu.Host, outlet, command)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(h.pdu.Username, h.pdu.Password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send PDU command: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PDU returned status %d", resp.StatusCode)
	}
	return nil
}
