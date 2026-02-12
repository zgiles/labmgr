# Lab Manager Deployment Guide

## Overview

Lab Manager is a web-based IPMI and PDU management tool designed for lab environments. It supports three device types:

- **IPMI-only devices**: Servers with BMC/iDRAC/iLO
- **PDU-only devices**: Network equipment, switches, etc.
- **Combined IPMI+PDU devices**: Servers with both BMC and power outlets

## Production Deployment

### Prerequisites

- Go 1.18+ (for building)
- Network access to target devices (IPMI BMCs and PDUs)
- Linux/macOS/Windows deployment target

### Building

```bash
# Clone the repository
git clone <repository-url>
cd labmgr

# Build production binary
make build

# OR build manually
go build -ldflags="-s -w" -o labmgr
```

This creates a single static binary `labmgr` with embedded frontend assets (~8.3MB).

### Configuration

Create your `config/servers.yaml` file:

```yaml
servers:
  # IPMI + PDU Device (Full Power Control)
  - id: "server-01"
    name: "Lab Server 01"
    description: "Main compute server"
    ipmi:
      host: "192.168.1.101"
      username: "admin" 
      password: "password"
    pdu_outlets: ["pdu-01:1", "pdu-01:2"]
    metadata:
      hostname: "lab-server-01"
      role: "compute"
      mac: "aa:bb:cc:dd:ee:01"

  # IPMI-Only Device  
  - id: "compute-02"
    name: "Compute Node 02"
    description: "IPMI-only server"
    ipmi:
      host: "192.168.1.102"
      username: "admin"
      password: "password"
    # No pdu_outlets

  # PDU-Only Device
  - id: "switch-01" 
    name: "Network Switch"
    description: "Managed switch"
    ipmi:
      host: ""  # Empty for PDU-only
      username: ""
      password: ""
    pdu_outlets: ["pdu-01:8"]

pdus:
  - id: "pdu-01"
    name: "Main PDU (AP8641)"
    host: "192.168.1.200"
    username: "apc"              # HTTP username (fallback)
    password: "apc"              # HTTP password (fallback)
    type: "apc"                  # Uses SNMP for APC PDUs
    snmp_community: "private"    # SNMP write community
    snmp_version: "v1"           # v1, v2c, or v3
    snmp_port: 161               # Default: 161

delays:
  pdu_to_power_on_seconds: 120   # Wait from PDU on to IPMI on
  power_off_to_pdu_seconds: 10   # Wait from IPMI off to PDU off
  between_ipmi_seconds: 10       # Between IPMI operations
  between_pdu_seconds: 5         # Between PDU outlet operations
```

### Running

```bash
# Production mode
./labmgr --config=config/servers.yaml

# With debug logging  
./labmgr --config=config/servers.yaml --debug

# Access web interface
open http://localhost:8098
```

## Systemd Service (Linux)

Create `/etc/systemd/system/labmgr.service`:

```ini
[Unit]
Description=Lab Manager
After=network.target

[Service]
Type=simple
User=labmgr
WorkingDirectory=/opt/labmgr
ExecStart=/opt/labmgr/labmgr --config=/opt/labmgr/config/servers.yaml
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

Setup and start:

```bash
# Create user and directories
sudo useradd -r -s /bin/false labmgr
sudo mkdir -p /opt/labmgr/config
sudo cp labmgr /opt/labmgr/
sudo cp config/servers.yaml /opt/labmgr/config/
sudo chown -R labmgr:labmgr /opt/labmgr

# Start service
sudo systemctl daemon-reload
sudo systemctl enable labmgr
sudo systemctl start labmgr
sudo systemctl status labmgr
```

## Docker Deployment

### Dockerfile

```dockerfile
FROM golang:1.18-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -ldflags="-s -w" -o labmgr

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/labmgr .
COPY config/servers.yaml config/
EXPOSE 8098
CMD ["./labmgr", "--config=config/servers.yaml"]
```

### Docker Compose

```yaml
version: '3.8'
services:
  labmgr:
    build: .
    ports:
      - "8098:8098"
    volumes:
      - ./config/servers.yaml:/app/config/servers.yaml:ro
    restart: unless-stopped
    environment:
      - GIN_MODE=release
```

## Reverse Proxy (Nginx)

```nginx
server {
    listen 80;
    server_name labmgr.example.com;
    
    location / {
        proxy_pass http://localhost:8098;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## Security Considerations

### Network Security
- Run Lab Manager on isolated management network
- Use VPN or bastion host for remote access
- Configure firewall rules to restrict PDU/IPMI access

### Credential Management
- Use dedicated service accounts for IPMI/PDU access
- Store `servers.yaml` with restricted permissions (600)
- Consider using environment variables for sensitive data

### PDU Security
- Use SNMP v3 with authentication when available
- Change default SNMP community strings
- Limit SNMP access by source IP

## Monitoring and Logging

### Application Logs
```bash
# Systemd journal
journalctl -u labmgr -f

# Debug mode
./labmgr --config=config/servers.yaml --debug
```

### Health Checks
```bash
# API health check
curl http://localhost:8098/api/v1/servers

# Specific device status
curl http://localhost:8098/api/v1/servers/server-01/status
```

### Prometheus Metrics
Lab Manager exposes basic HTTP metrics on the standard Gin router. For detailed monitoring:

```yaml
# prometheus.yml
- job_name: 'labmgr'
  static_configs:
    - targets: ['localhost:8098']
```

## Troubleshooting

### Common Issues

**PDU Connection Failed**
```bash
# Test SNMP connectivity
snmpget -v1 -c public PDU_IP .1.3.6.1.4.1.318.1.1.4.4.2.1.3.1

# Check PDU configuration
curl http://PDU_IP/status.xml -u username:password
```

**IPMI Connection Failed**  
```bash
# Test IPMI connectivity
curl -k https://BMC_IP/redfish/v1/ -u username:password

# Check network connectivity
ping BMC_IP
telnet BMC_IP 443
```

**Configuration Validation Errors**
```bash
# Test configuration
./labmgr --config=config/servers.yaml --debug
```

### Debug Mode
Enable debug logging to see detailed SNMP and HTTP operations:

```bash
./labmgr --config=config/servers.yaml --debug
```

## Performance Tuning

### For Large Deployments (100+ devices)

1. **Adjust polling intervals** in code if needed
2. **Increase system limits**:
   ```bash
   # /etc/security/limits.conf
   labmgr soft nofile 4096
   labmgr hard nofile 8192
   ```

3. **Use connection pooling** for PDU operations
4. **Monitor job queue** performance via API

### Resource Usage
- **Memory**: ~50MB base + ~1MB per 100 devices
- **CPU**: Minimal, mostly I/O bound
- **Network**: Concurrent SNMP/HTTP requests to devices

## Backup and Recovery

### Configuration Backup
```bash
# Backup configuration
cp config/servers.yaml config/servers.yaml.backup

# Automated backup
0 2 * * * cp /opt/labmgr/config/servers.yaml /backup/labmgr-$(date +\%Y\%m\%d).yaml
```

### Disaster Recovery
1. Backup `servers.yaml` configuration
2. Rebuild binary from source
3. Restore configuration  
4. Restart service

Lab Manager is stateless - all state is in the configuration file.

## API Integration

### Power Control
```bash
# Power on device
curl -X POST http://localhost:8098/api/v1/servers/server-01/power \
  -H "Content-Type: application/json" \
  -d '{"action": "on"}'

# Get status
curl http://localhost:8098/api/v1/servers/server-01/status

# Monitor jobs
curl http://localhost:8098/api/v1/jobs
```

### Automation Examples
```bash
# Power on all compute servers
curl http://localhost:8098/api/v1/servers | jq -r '.Servers[] | select(.metadata.role=="compute") | .id' | \
while read server; do
  curl -X POST http://localhost:8098/api/v1/servers/$server/power -H "Content-Type: application/json" -d '{"action": "on"}'
done
```

This deployment guide provides comprehensive instructions for production deployment, monitoring, and troubleshooting of Lab Manager in enterprise environments.