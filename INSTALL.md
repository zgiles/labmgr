# Installation Guide

This guide provides detailed installation instructions for Lab Manager across different environments and use cases.

## System Requirements

### Minimum Requirements
- **Operating System**: Linux, macOS, or Windows
- **Go**: Version 1.19 or later
- **Memory**: 512MB RAM minimum
- **Disk**: 100MB free space
- **Network**: Access to target IPMI interfaces and PDUs

### Recommended Requirements
- **Memory**: 1GB+ RAM for large environments (50+ devices)
- **Network**: Gigabit Ethernet for fastest IPMI/SNMP operations
- **Storage**: SSD for faster startup and job processing

### Network Requirements
- **IPMI Access**: Port 623/UDP to server BMCs
- **SNMP Access**: Port 161/UDP to PDUs
- **Web Interface**: Port 8098/TCP (configurable)

## Installation Methods

### Method 1: Binary Release (Recommended)

Download the latest release binary for your platform:

```bash
# Linux x64
wget https://github.com/your-org/labmgr/releases/latest/download/labmgr-linux-amd64
chmod +x labmgr-linux-amd64
sudo mv labmgr-linux-amd64 /usr/local/bin/labmgr

# macOS
wget https://github.com/your-org/labmgr/releases/latest/download/labmgr-darwin-amd64
chmod +x labmgr-darwin-amd64
sudo mv labmgr-darwin-amd64 /usr/local/bin/labmgr

# Windows
# Download labmgr-windows-amd64.exe from releases page
```

### Method 2: Build from Source

#### Prerequisites
```bash
# Install Go (1.19+)
# Ubuntu/Debian
sudo apt update
sudo apt install golang-go

# CentOS/RHEL
sudo yum install golang

# macOS (with Homebrew)
brew install go

# Verify Go installation
go version
```

#### Install Node.js (for frontend development)
```bash
# Ubuntu/Debian
curl -fsSL https://deb.nodesource.com/setup_18.x | sudo -E bash -
sudo apt-get install -y nodejs

# CentOS/RHEL
curl -fsSL https://rpm.nodesource.com/setup_18.x | sudo bash -
sudo yum install -y nodejs

# macOS
brew install node

# Verify installation
node --version
npm --version
```

#### Build Process
```bash
# Clone repository
git clone https://github.com/your-org/labmgr.git
cd labmgr

# Build frontend
cd frontend
npm install
npm run build
cd ..

# Build backend with embedded frontend
cd backend
go mod download
go build -o ../labmgr .

# Optional: Build for different platforms
GOOS=linux GOARCH=amd64 go build -o ../labmgr-linux-amd64 .
GOOS=darwin GOARCH=amd64 go build -o ../labmgr-darwin-amd64 .
GOOS=windows GOARCH=amd64 go build -o ../labmgr-windows-amd64.exe .
```

### Method 3: Docker Container

#### Using Docker Hub Image
```bash
# Pull the latest image
docker pull your-org/labmgr:latest

# Run with mounted config
docker run -d \
  --name labmgr \
  -p 8098:8098 \
  -v /path/to/config:/app/config \
  your-org/labmgr:latest
```

#### Build Docker Image
```bash
# Clone and build image
git clone https://github.com/your-org/labmgr.git
cd labmgr

# Build image
docker build -t labmgr .

# Run container
docker run -d \
  --name labmgr \
  -p 8098:8098 \
  -v $(pwd)/config:/app/config \
  labmgr
```

#### Docker Compose
```yaml
# docker-compose.yml
version: '3.8'
services:
  labmgr:
    image: your-org/labmgr:latest
    ports:
      - "8098:8098"
    volumes:
      - ./config:/app/config
    environment:
      - CONFIG_PATH=/app/config/servers.yaml
      - DEBUG=false
    restart: unless-stopped
```

## Configuration Setup

### 1. Create Configuration Directory
```bash
sudo mkdir -p /etc/labmgr
cd /etc/labmgr
```

### 2. Copy Example Configuration
```bash
# From source directory
cp config.example.yaml servers.yaml

# Or download example
wget https://raw.githubusercontent.com/your-org/labmgr/main/config.example.yaml -O servers.yaml
```

### 3. Edit Configuration
```bash
nano servers.yaml
# Configure your servers and PDUs
# See config.example.yaml for all options
```

### 4. Validate Configuration
```bash
# Test configuration
labmgr -config servers.yaml -debug
# Check for configuration errors in output
```

## Production Deployment

### Systemd Service (Linux)

Create systemd service file:
```bash
sudo nano /etc/systemd/system/labmgr.service
```

```ini
[Unit]
Description=Lab Manager - IPMI and PDU Control System
Documentation=https://github.com/your-org/labmgr
After=network.target

[Service]
Type=simple
User=labmgr
Group=labmgr
WorkingDirectory=/opt/labmgr
ExecStart=/usr/local/bin/labmgr -config /etc/labmgr/servers.yaml
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=labmgr

# Security settings
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/labmgr

[Install]
WantedBy=multi-user.target
```

Create dedicated user:
```bash
# Create user and directories
sudo useradd -r -s /bin/false -d /opt/labmgr labmgr
sudo mkdir -p /opt/labmgr
sudo chown labmgr:labmgr /opt/labmgr

# Install binary
sudo cp labmgr /usr/local/bin/
sudo chown root:root /usr/local/bin/labmgr
sudo chmod 755 /usr/local/bin/labmgr

# Set config permissions
sudo chown labmgr:labmgr /etc/labmgr/servers.yaml
sudo chmod 600 /etc/labmgr/servers.yaml

# Enable and start service
sudo systemctl daemon-reload
sudo systemctl enable labmgr
sudo systemctl start labmgr

# Check status
sudo systemctl status labmgr
sudo journalctl -u labmgr -f
```

### Reverse Proxy Setup

#### Nginx Configuration
```nginx
# /etc/nginx/sites-available/labmgr
server {
    listen 80;
    server_name labmgr.yourdomain.com;

    location / {
        proxy_pass http://localhost:8098;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_cache_bypass $http_upgrade;
    }
}

# Enable site
sudo ln -s /etc/nginx/sites-available/labmgr /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

#### Apache Configuration
```apache
# /etc/apache2/sites-available/labmgr.conf
<VirtualHost *:80>
    ServerName labmgr.yourdomain.com
    
    ProxyPreserveHost On
    ProxyPass / http://localhost:8098/
    ProxyPassReverse / http://localhost:8098/
    
    # WebSocket support
    RewriteEngine on
    RewriteCond %{HTTP:Upgrade} websocket [NC]
    RewriteCond %{HTTP:Connection} upgrade [NC]
    RewriteRule ^/?(.*) "ws://localhost:8098/$1" [P,L]
</VirtualHost>

# Enable modules and site
sudo a2enmod proxy proxy_http rewrite
sudo a2ensite labmgr
sudo systemctl reload apache2
```

## Development Setup

### Hot Reload Development
```bash
# Terminal 1: Start backend with hot reload
cd backend
go install github.com/cosmtrek/air@latest
air

# Terminal 2: Start frontend dev server
cd frontend
npm run serve
```

### IDE Configuration

#### VS Code
Install recommended extensions:
- Go extension
- Vue Language Features (Volar)
- ESLint
- Prettier

#### GoLand/IntelliJ
- Enable Go support
- Install Vue.js plugin
- Configure run configurations

## Security Considerations

### File Permissions
```bash
# Restrict config file access
chmod 600 /etc/labmgr/servers.yaml
chown root:root /etc/labmgr/servers.yaml

# Or for service user
chown labmgr:labmgr /etc/labmgr/servers.yaml
```

### Firewall Configuration
```bash
# UFW (Ubuntu)
sudo ufw allow 8098/tcp

# Firewalld (CentOS/RHEL)
sudo firewall-cmd --permanent --add-port=8098/tcp
sudo firewall-cmd --reload

# iptables
sudo iptables -A INPUT -p tcp --dport 8098 -j ACCEPT
```

### Network Security
- Use dedicated management network for IPMI/PDU access
- Implement VPN access for remote management
- Regular password rotation for device credentials
- Monitor access logs for unauthorized attempts

## Troubleshooting

### Common Issues

**Binary won't start**
```bash
# Check permissions
ls -la /usr/local/bin/labmgr
chmod +x /usr/local/bin/labmgr

# Check dependencies
ldd /usr/local/bin/labmgr
```

**Configuration errors**
```bash
# Validate YAML syntax
python -c "import yaml; yaml.load(open('servers.yaml'))"

# Test with debug mode
labmgr -debug -config servers.yaml
```

**Service won't start**
```bash
# Check service status
sudo systemctl status labmgr
sudo journalctl -u labmgr --no-pager

# Check file permissions
ls -la /etc/labmgr/servers.yaml
ls -la /usr/local/bin/labmgr
```

**Network connectivity**
```bash
# Test IPMI connectivity
ipmitool -I lanplus -H 192.168.1.100 -U admin -P password power status

# Test SNMP connectivity  
snmpget -v2c -c private 192.168.1.200 1.3.6.1.4.1.318.1.1.4.4.2.1.3.1
```

### Debug Mode
```bash
# Enable debug logging
labmgr -debug -config servers.yaml

# Or set environment variable
export DEBUG=true
labmgr -config servers.yaml
```

### Log Analysis
```bash
# View systemd logs
sudo journalctl -u labmgr -f

# Filter for errors
sudo journalctl -u labmgr | grep -i error

# Export logs
sudo journalctl -u labmgr --since "1 hour ago" > labmgr.log
```

## Performance Tuning

### Large Environments (50+ Devices)
```bash
# Increase system limits
echo "labmgr soft nofile 4096" >> /etc/security/limits.conf
echo "labmgr hard nofile 8192" >> /etc/security/limits.conf

# Tune polling intervals in source code if needed
# See poller.go for timing constants
```

### Memory Usage
```bash
# Monitor memory usage
ps aux | grep labmgr
top -p $(pgrep labmgr)

# Typical usage: 50-200MB depending on device count
```

## Upgrade Process

### Service Upgrade
```bash
# Stop service
sudo systemctl stop labmgr

# Backup configuration
sudo cp /etc/labmgr/servers.yaml /etc/labmgr/servers.yaml.backup

# Install new binary
sudo cp labmgr-new /usr/local/bin/labmgr

# Start service
sudo systemctl start labmgr

# Verify upgrade
curl http://localhost:8098/api/v1/servers
```

### Docker Upgrade
```bash
# Pull latest image
docker pull your-org/labmgr:latest

# Stop and remove old container
docker stop labmgr
docker rm labmgr

# Start with new image
docker run -d --name labmgr -p 8098:8098 -v $(pwd)/config:/app/config your-org/labmgr:latest
```

## iPXE Integration Setup

### Basic iPXE Configuration

Lab Manager includes comprehensive iPXE boot support for automated server deployment.

#### 1. DHCP Server Configuration

Configure your DHCP server to point to Lab Manager's boot script:

**ISC DHCP (dhcpd.conf):**
```
subnet 192.168.1.0 netmask 255.255.255.0 {
  range 192.168.1.100 192.168.1.200;
  
  # Lab Manager iPXE boot
  if exists user-class and option user-class = "iPXE" {
    filename "http://192.168.1.50:8098/api/v1/autoexec.ipxe";
  } else {
    filename "http://192.168.1.50:8098/static/boot.ipxe";
  }
  
  next-server 192.168.1.50;
}
```

**Dnsmasq:**
```
# Basic PXE boot
dhcp-boot=http://192.168.1.50:8098/static/boot.ipxe

# Or for iPXE-aware clients
dhcp-match=set:ipxe,175
dhcp-boot=tag:ipxe,http://192.168.1.50:8098/api/v1/autoexec.ipxe
```

#### 2. Server Configuration

Add MAC addresses and boot configuration to your servers:

**Basic Example:**
```yaml
servers:
  - id: "web-01"
    name: "Web Server 1"
    metadata:
      mac: "52:54:00:12:34:56"
      hostname: "web1.lab.local"
      role: "webserver"
      # Uses default local boot
```

**Chain Loading Example:**
```yaml
servers:
  - id: "nixos-01"
    name: "NixOS Development"
    metadata:
      mac: "52:54:00:ab:cd:ef"
      hostname: "nixos-dev1"
      role: "development"
      # Chain to NixOS netboot
      next-path: "https://netboot.nixos.org/ipxe?hostname={{.Hostname}}&arch=x86_64"
```

**Custom Template Example:**
```yaml
servers:
  - id: "ubuntu-01"
    name: "Ubuntu Server"
    metadata:
      mac: "52:54:00:fe:dc:ba"
      hostname: "ubuntu1"
      role: "compute"
      ubuntu_version: "22.04"
      # Custom Ubuntu autoinstall
      boot-template: |
        #!ipxe
        
        echo Lab Manager - Ubuntu {{.Metadata.ubuntu_version}} for {{.Hostname}}
        echo Role: {{.Role}}
        echo Client IP: {{.IncomingServerIP}}
        
        # Download kernel and initrd
        kernel http://archive.ubuntu.com/ubuntu/dists/jammy/main/installer-amd64/current/legacy-images/netboot/ubuntu-installer/amd64/linux
        initrd http://archive.ubuntu.com/ubuntu/dists/jammy/main/installer-amd64/current/legacy-images/netboot/ubuntu-installer/amd64/initrd.gz
        
        # Boot with autoinstall parameters
        imgargs linux auto=true priority=critical
        imgargs linux hostname={{.Hostname}}
        imgargs linux preseed/url=http://lab-manager:8098/preseed/{{.Role}}.cfg
        
        boot
```

#### 3. Template Variables

Available in `next-path` and `boot-template` fields:

| Variable | Description | Example |
|----------|-------------|---------|
| `{{.MAC}}` | Server MAC address | `52:54:00:12:34:56` |
| `{{.Hostname}}` | Server hostname | `web1.lab.local` |
| `{{.Role}}` | Server role | `webserver` |
| `{{.ServerName}}` | Display name | `Web Server 1` |
| `{{.ServerID}}` | Unique ID | `web-01` |
| `{{.IncomingServerIP}}` | Client's IP | `192.168.1.100` |
| `{{.Metadata.field}}` | Custom metadata | `{{.Metadata.ubuntu_version}}` |

#### 4. Testing iPXE Integration

Test your configuration:

```bash
# Test autoexec endpoint
curl "http://lab-manager:8098/api/v1/autoexec.ipxe?mac=52:54:00:12:34:56"

# Test legacy endpoint
curl "http://lab-manager:8098/api/v1/boot/52:54:00:12:34:56"

# Test static boot script
curl "http://lab-manager:8098/static/boot.ipxe"
```

#### 5. Common Use Cases

**Deployment Automation:**
```yaml
# Production deployment with preseed
next-path: "http://deploy.lab.local/ubuntu?hostname={{.Hostname}}&role={{.Role}}&mac={{.MAC}}"
```

**Development Environment:**
```yaml
# NixOS development boxes
next-path: "https://netboot.nixos.org/ipxe?hostname={{.Hostname}}&flake=github:lab/nixos-configs#{{.Role}}"
```

**Rescue Environment:**
```yaml
# System rescue
next-path: "http://rescue.lab.local/sysrescue?target={{.Hostname}}&client={{.IncomingServerIP}}"
```

### Troubleshooting iPXE

**Server not found:**
- Verify MAC address in metadata matches network interface
- Check DHCP server logs for boot requests
- Test endpoints manually with curl

**Template errors:**
- Check Lab Manager logs for template parsing errors
- Verify template syntax with Go template format
- Test with simple templates first

**Boot failures:**
- Verify external URLs are accessible from servers
- Check firewall rules for HTTP/HTTPS access
- Test chain-loaded scripts independently

## Support

For additional help:
- Check the main [README.md](README.md) for iPXE integration examples
- Review [Configuration Examples](config.example.yaml) 
- Submit issues on GitHub
- Enable debug mode for detailed logging