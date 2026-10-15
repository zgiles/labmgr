package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Servers []Server    `yaml:"servers"`
	PDUs    []PDU       `yaml:"pdus"`
	Delays  DelayConfig `yaml:"delays"`
}

type DelayConfig struct {
	PDUToPowerOn     int `yaml:"pdu_to_power_on_seconds"`     // Delay from PDU on to IPMI power on
	PowerOffToPDU    int `yaml:"power_off_to_pdu_seconds"`    // Delay from IPMI off to PDU off
	BetweenIPMI      int `yaml:"between_ipmi_seconds"`        // Delay between IPMI actions
	BetweenPDU       int `yaml:"between_pdu_seconds"`         // Delay between PDU outlet actions
}

type Server struct {
	ID          string            `yaml:"id" json:"id"`
	Name        string            `yaml:"name" json:"name"`
	Description string            `yaml:"description" json:"description"`
	IPMI        IPMIConfig        `yaml:"ipmi" json:"ipmi"`
	PDUOutlets  []string          `yaml:"pdu_outlets" json:"pdu_outlets"`
	Metadata    map[string]string `yaml:"metadata" json:"metadata"`
}

type IPMIConfig struct {
	Host     string `yaml:"host" json:"host"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

type PDU struct {
	ID            string `yaml:"id" json:"id"`
	Name          string `yaml:"name" json:"name"`
	Host          string `yaml:"host" json:"host"`
	Username      string `yaml:"username" json:"username"`                             // HTTP username
	Password      string `yaml:"password" json:"password"`                             // HTTP password
	Type          string `yaml:"type" json:"type"`                                     // "apc" for SNMP, others for HTTP
	SNMPCommunity string `yaml:"snmp_community,omitempty" json:"snmp_community,omitempty"` // SNMP community string
	SNMPVersion   string `yaml:"snmp_version,omitempty" json:"snmp_version,omitempty"`     // "v1", "v2c", "v3"
	SNMPPort      int    `yaml:"snmp_port,omitempty" json:"snmp_port,omitempty"`           // Default: 161
}

// Public returns a copy of the config that is safe to send to clients: credentials are removed
func (c *Config) Public() *Config {
	public := &Config{Delays: c.Delays}
	for _, server := range c.Servers {
		server.IPMI.Password = ""
		public.Servers = append(public.Servers, server)
	}
	for _, pdu := range c.PDUs {
		pdu.Password = ""
		pdu.SNMPCommunity = ""
		public.PDUs = append(public.PDUs, pdu)
	}
	return public
}

func loadConfig() (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	// Validate configuration
	err = validateConfig(&config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func validateConfig(config *Config) error {
	// Validate servers
	serverIDs := make(map[string]bool)
	for i, server := range config.Servers {
		if server.ID == "" {
			return fmt.Errorf("server[%d]: ID cannot be empty", i)
		}
		if serverIDs[server.ID] {
			return fmt.Errorf("server[%d]: duplicate ID '%s'", i, server.ID)
		}
		serverIDs[server.ID] = true

		// Check if server has either IPMI or PDU configuration
		hasIPMI := server.IPMI.Host != ""
		hasPDU := len(server.PDUOutlets) > 0
		if !hasIPMI && !hasPDU {
			return fmt.Errorf("server[%d] '%s': must have either IPMI host or PDU outlets configured", i, server.ID)
		}

		// Validate PDU outlet format if present
		for j, outlet := range server.PDUOutlets {
			parts := strings.Split(outlet, ":")
			if len(parts) != 2 {
				return fmt.Errorf("server[%d] '%s' pdu_outlets[%d]: invalid format '%s', expected 'pdu-id:outlet-number'", i, server.ID, j, outlet)
			}
			pduID := parts[0]
			outletNum := parts[1]
			
			// Check PDU ID exists
			pduFound := false
			for _, pdu := range config.PDUs {
				if pdu.ID == pduID {
					pduFound = true
					break
				}
			}
			if !pduFound {
				return fmt.Errorf("server[%d] '%s': references unknown PDU '%s'", i, server.ID, pduID)
			}

			// Validate outlet number
			if _, err := strconv.Atoi(outletNum); err != nil {
				return fmt.Errorf("server[%d] '%s': invalid outlet number '%s'", i, server.ID, outletNum)
			}
		}
	}

	// Validate PDUs  
	pduIDs := make(map[string]bool)
	for i, pdu := range config.PDUs {
		if pdu.ID == "" {
			return fmt.Errorf("pdu[%d]: ID cannot be empty", i)
		}
		if pduIDs[pdu.ID] {
			return fmt.Errorf("pdu[%d]: duplicate ID '%s'", i, pdu.ID)
		}
		pduIDs[pdu.ID] = true

		if pdu.Host == "" {
			return fmt.Errorf("pdu[%d] '%s': host cannot be empty", i, pdu.ID)
		}
		if pdu.Type == "" {
			return fmt.Errorf("pdu[%d] '%s': type cannot be empty", i, pdu.ID)
		}
	}

	return nil
}