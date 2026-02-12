# Lab Manager

A comprehensive lab management system for controlling and monitoring physical servers through IPMI/Redfish and PDU (Power Distribution Unit) interfaces. Built with Go backend and Vue.js frontend, providing real-time status monitoring, job queue management, and flexible power control for diverse hardware configurations.

## Features

### Device Support
- **IPMI/Redfish Control**: Full BMC integration for server power management and status monitoring
- **PDU Integration**: Support for APC PDUs via SNMP and generic HTTP-based PDUs
- **Flexible Configuration**: Support for IPMI-only, PDU-only, or combined IPMI+PDU devices
- **Intelligent Fallback**: Automatic fallback to available control methods when primary fails

### Power Management
- **Smart Power Sequences**: Coordinated power-on/off sequences with configurable delays
- **Job Queue System**: Reliable job execution with dependencies, scheduling, and retry logic
- **State Machine Logic**: Prevents invalid operations based on actual device state
- **Bulk Operations**: Efficient bulk PDU operations to minimize SNMP load

### Monitoring & Status
- **Real-time Status**: Live server status with dual tracking (system vs power status)
- **Background Polling**: Optimized polling with different intervals for reachable/unreachable devices
- **Visual Indicators**: Error triangles with tooltips for failed operations
- **Connection Management**: Robust frontend with offline data caching and reconnection logic

### User Interface
- **Modern Web UI**: Responsive Vue.js interface with real-time updates
- **Job Management**: Complete job queue visibility with retry and cancellation
- **Error Tracking**: Persistent error indicators with detailed failure information
- **PXE Boot System**: Advanced iPXE integration with templating and chain loading

## Installation

### Prerequisites
- Go 1.19 or later
- Node.js 16+ and npm (for frontend development)
- Access to IPMI-enabled servers
- APC PDUs or HTTP-controllable PDUs

### Quick Start

1. **Clone the repository**
   ```bash
   git clone https://github.com/your-org/labmgr.git
   cd labmgr
   ```

2. **Build the application**
   ```bash
   # Build frontend
   cd frontend
   npm install
   npm run build
   cd ..
   
   # Build backend with embedded frontend
   cd backend
   go build -o ../labmgr .
   ```

3. **Create configuration**
   ```bash
   mkdir -p config
   cp config.example.yaml config/servers.yaml
   # Edit config/servers.yaml with your server details
   ```

4. **Run the application**
   ```bash
   ./labmgr -config config/servers.yaml
   ```

5. **Access the web interface**
   Open http://localhost:8098 in your browser

### Development Setup

For development with hot-reload:

```bash
# Terminal 1: Start backend
cd backend
go run . -debug -config ../config/servers.yaml

# Terminal 2: Start frontend dev server
cd frontend
npm run serve
```

## Configuration

The system uses a YAML configuration file to define servers and PDUs. See `config.example.yaml` for a complete example.

### Basic Server Configuration

```yaml
servers:
  - id: "server1"
    name: "Web Server 1"
    description: "Production web server"
    ipmi:
      host: "192.168.1.100"
      username: "admin"
      password: "password"
    pdu_outlets:
      - "pdu1:1"
      - "pdu1:2"
    metadata:
      role: "webserver"
      mac: "aa:bb:cc:dd:ee:ff"
      hostname: "web1.lab.local"

pdus:
  - id: "pdu1"
    name: "Rack 1 PDU"
    host: "192.168.1.200"
    type: "apc"
    snmp_community: "private"
    snmp_version: "v2c"

delays:
  pdu_to_power_on_seconds: 10
  power_off_to_pdu_seconds: 30
  between_ipmi_seconds: 2
  between_pdu_seconds: 1
```

### Device Types

- **IPMI + PDU**: Full control with both BMC and power outlet management
- **IPMI Only**: Server control via BMC, no PDU outlets configured
- **PDU Only**: Power control via PDU outlets, no IPMI configuration

## API Reference

### Server Control
- `GET /api/v1/servers` - List all servers
- `GET /api/v1/servers/:id` - Get server details
- `POST /api/v1/servers/:id/power` - Power control (on/off/reboot)
- `POST /api/v1/servers/:id/ipmi` - Direct IPMI commands
- `GET /api/v1/servers/:id/status` - Get server status

### Job Management
- `GET /api/v1/jobs` - List all jobs
- `GET /api/v1/jobs/:id` - Get job details
- `POST /api/v1/jobs/:id/retry` - Retry failed job
- `DELETE /api/v1/jobs/:id` - Cancel job
- `POST /api/v1/jobs/schedule` - Schedule job for later execution

### Status Monitoring
- `GET /api/v1/status` - Get all server statuses
- `GET /api/v1/status?force=true` - Force refresh all statuses

### PXE Boot System
- `GET /boot/:mac` - Get iPXE boot script for MAC address
- `GET /autoexec.ipxe?mac=xx:xx:xx:xx:xx:xx` - Standard iPXE auto-execution endpoint

## iPXE Integration

Lab Manager provides a comprehensive iPXE boot system with templating and chain loading capabilities for automated server deployment.

### Endpoints

- **`/api/v1/autoexec.ipxe`** - Standard iPXE auto-execution endpoint
- **`/api/v1/boot/:mac`** - Legacy MAC-based boot script endpoint

### Features

#### 1. Template Variables
Dynamic iPXE scripts with access to:
- `{{.MAC}}` - Server MAC address
- `{{.Hostname}}` - Server hostname
- `{{.Role}}` - Server role
- `{{.ServerName}}` - Server display name
- `{{.IncomingServerIP}}` - Client IP address
- `{{.Metadata.field}}` - Any custom metadata field

#### 2. Chain Loading
Redirect servers to external boot services:
```yaml
metadata:
  next-path: "http://netboot.nixos.org/ipxe?hostname={{.Hostname}}&mac={{.MAC}}"
```

#### 3. Custom Boot Templates
Complete control over iPXE scripts:
```yaml
metadata:
  boot-template: |
    #!ipxe
    echo Booting {{.Hostname}} with role {{.Role}}
    kernel http://example.com/vmlinuz hostname={{.Hostname}}
    initrd http://example.com/initrd.gz
    boot
```

### Configuration Examples

**NixOS Chain Loading:**
```yaml
servers:
  - id: "nixos-dev"
    metadata:
      mac: "aa:bb:cc:dd:ee:04"
      hostname: "nixos-dev1"
      role: "development"
      next-path: "http://netboot.nixos.org/ipxe?hostname={{.Hostname}}&mac={{.MAC}}&role={{.Role}}"
```

**Ubuntu Autoinstall:**
```yaml
servers:
  - id: "ubuntu-web"
    metadata:
      mac: "aa:bb:cc:dd:ee:05"
      hostname: "web1"
      role: "webserver"
      ubuntu_version: "22.04"
      boot-template: |
        #!ipxe
        echo Ubuntu {{.Metadata.ubuntu_version}} autoinstall for {{.Hostname}}
        kernel http://archive.ubuntu.com/ubuntu/dists/jammy/main/installer-amd64/current/legacy-images/netboot/ubuntu-installer/amd64/linux
        initrd http://archive.ubuntu.com/ubuntu/dists/jammy/main/installer-amd64/current/legacy-images/netboot/ubuntu-installer/amd64/initrd.gz
        imgargs linux auto=true hostname={{.Hostname}} preseed/url=http://lab.example.com/preseed/{{.Role}}.cfg
        boot
```

**Simple Local Boot (Default):**
```yaml
servers:
  - id: "local-server"
    metadata:
      mac: "aa:bb:cc:dd:ee:06"
      hostname: "server1"
      role: "storage"
      # No next-path or boot-template = uses default local boot
```

### Integration Workflow

1. **Server boots** and requests `http://lab-manager:8098/static/boot.ipxe`
2. **Static script** chains to `/api/v1/autoexec.ipxe?mac=${mac}`
3. **Lab Manager** looks up server by MAC address
4. **Template processing** applies variables to next-path or boot-template
5. **Generated script** returned with server-specific configuration

## Architecture

### Backend (Go)
- **Gin HTTP Framework**: RESTful API with embedded static files
- **Job Queue System**: Concurrent job processing with dependencies
- **Status Cache**: Intelligent caching with dual status tracking
- **Device Abstraction**: Unified interface for IPMI and PDU operations

### Frontend (Vue.js)
- **Reactive UI**: Real-time updates with automatic reconnection
- **Component Architecture**: Modular design with reusable components
- **Error Handling**: Comprehensive error display with user feedback
- **Offline Support**: Graceful degradation with cached data

### Integration Points
- **IPMI/Redfish**: Direct BMC communication for server control
- **SNMP**: APC PDU integration with bulk operations
- **HTTP APIs**: Generic PDU support via REST interfaces

## Troubleshooting

### Common Issues

**Servers show "unreachable"**
- Check IPMI credentials and network connectivity
- Verify PDU SNMP community strings
- Review firewall rules for IPMI (623) and SNMP (161) ports

**Jobs failing repeatedly**
- Check server power states match expectations
- Verify delay configurations for your hardware
- Review job logs in the web interface

**Slow status updates**
- Confirm PDU SNMP response times
- Adjust polling intervals in poller.go if needed
- Check network latency to IPMI interfaces

### Debug Mode

Run with debug logging enabled:
```bash
./labmgr -debug -config config/servers.yaml
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable  
5. Submit a pull request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Support

For issues and questions:
- Create an issue on GitHub
- Check the troubleshooting section above
- Review the configuration examples