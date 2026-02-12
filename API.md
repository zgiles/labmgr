# API Reference

Lab Manager provides a comprehensive REST API for integration with external systems, automation scripts, and custom applications.

## Base URL

```
http://localhost:8098/api/v1
```

## Authentication

Currently, Lab Manager does not require authentication. For production deployments, consider placing behind a reverse proxy with authentication (nginx, Apache, etc.).

## Response Format

All API responses are in JSON format:

```json
{
  "status": "success|error",
  "data": {...},
  "message": "Human readable message"
}
```

Error responses include HTTP status codes and error details:

```json
{
  "error": "Error description",
  "details": "Additional error information"
}
```

## Server Management

### List All Servers

**GET** `/servers`

Returns a list of all configured servers.

**Response:**
```json
{
  "servers": [
    {
      "id": "server-01",
      "name": "Web Server 1", 
      "description": "Production web server",
      "ipmi": {
        "host": "192.168.1.100",
        "username": "admin"
      },
      "pdu_outlets": ["pdu1:1", "pdu1:2"],
      "metadata": {
        "hostname": "web1.lab.local",
        "role": "webserver",
        "mac": "aa:bb:cc:dd:ee:01"
      }
    }
  ],
  "pdus": [...],
  "delays": {...}
}
```

### Get Server Details

**GET** `/servers/:id`

Returns details for a specific server.

**Parameters:**
- `id` (string): Server ID

**Response:**
```json
{
  "id": "server-01",
  "name": "Web Server 1",
  "description": "Production web server",
  "ipmi": {
    "host": "192.168.1.100",
    "username": "admin"
  },
  "pdu_outlets": ["pdu1:1"],
  "metadata": {
    "hostname": "web1.lab.local",
    "role": "webserver"
  }
}
```

**Error Responses:**
- `404 Not Found`: Server not found

## Power Control

### Server Power Control

**POST** `/servers/:id/power`

Control server power state through intelligent power sequences.

**Parameters:**
- `id` (string): Server ID

**Request Body:**
```json
{
  "action": "on|off"
}
```

**Actions:**
- `on`: Power on sequence (PDU → wait → IPMI on)
- `off`: Power off sequence (IPMI off → wait → PDU off)

**Response:**
```json
{
  "status": "queued",
  "jobs": [
    {
      "id": "job-12345",
      "type": "PDU_ON",
      "server_id": "server-01",
      "status": "pending",
      "scheduled_time": "2026-01-30T10:30:00Z",
      "description": "PDU Power On for Web Server 1"
    }
  ],
  "message": "Queued 2 job(s)"
}
```

**Error Responses:**
- `400 Bad Request`: Invalid action
- `404 Not Found`: Server not found
- `500 Internal Server Error`: Configuration or system error

### Server Reboot

**POST** `/servers/:id/reboot`

Reboot a server using intelligent power sequence.

**Parameters:**
- `id` (string): Server ID

**Response:**
```json
{
  "status": "queued", 
  "jobs": [
    {
      "id": "job-12346",
      "type": "IPMI",
      "action": "off",
      "server_id": "server-01",
      "status": "pending"
    },
    {
      "id": "job-12347", 
      "type": "IPMI",
      "action": "on",
      "server_id": "server-01",
      "status": "pending"
    }
  ],
  "message": "Queued 2 job(s) for reboot sequence"
}
```

### Direct IPMI Control

**POST** `/servers/:id/ipmi`

Execute direct IPMI commands bypassing power sequences.

**Parameters:**
- `id` (string): Server ID

**Request Body:**
```json
{
  "action": "on|off|reboot"
}
```

**Response:**
```json
{
  "status": "queued",
  "job": {
    "id": "job-12348",
    "type": "IPMI",
    "action": "reboot",
    "server_id": "server-01",
    "status": "pending",
    "description": "Direct IPMI reboot for server-01"
  },
  "message": "Queued direct IPMI reboot job for server-01"
}
```

### PDU Outlet Control

**POST** `/pdu/:outlet/power`

Direct PDU outlet control.

**Parameters:**
- `outlet` (string): Outlet identifier in format "pdu-id:outlet-number" (e.g., "pdu1:5")

**Request Body:**
```json
{
  "action": "on|off"
}
```

**Response:**
```json
{
  "status": "success"
}
```

**Error Responses:**
- `400 Bad Request`: Invalid outlet format or action
- `500 Internal Server Error`: PDU communication error

## Status Monitoring

### Get Server Status

**GET** `/servers/:id/status`

Get current status for a specific server.

**Parameters:**
- `id` (string): Server ID
- `force` (query, optional): Set to "true" to bypass cache and force fresh status check

**Response:**
```json
{
  "status": "on|off|unreachable|powering_on|powering_off"
}
```

**Status Values:**
- `on`: Server is powered on and responding
- `off`: Server is powered off
- `unreachable`: Cannot communicate with server
- `powering_on`: Server is in the process of powering on
- `powering_off`: Server is in the process of powering off

### Get All Server Statuses

**GET** `/status`

Get current status for all servers.

**Parameters:**
- `force` (query, optional): Set to "true" to force refresh of all server statuses

**Response:**
```json
{
  "server-01": {
    "server_id": "server-01",
    "status": "on",
    "system_status": "on",
    "power_status": "on", 
    "last_checked": "2026-01-30T10:25:00Z",
    "is_reachable": true,
    "ipmi_reachable": true,
    "pdu_reachable": true,
    "is_transitional": false
  },
  "server-02": {
    "server_id": "server-02",
    "status": "off",
    "system_status": "off",
    "power_status": "off",
    "last_checked": "2026-01-30T10:24:30Z", 
    "is_reachable": true,
    "ipmi_reachable": true,
    "pdu_reachable": false
  }
}
```

## Job Management

### List All Jobs

**GET** `/jobs`

Get all jobs in the system (pending, running, completed, failed).

**Response:**
```json
[
  {
    "id": "job-12345",
    "type": "PDU_ON",
    "server_id": "server-01",
    "action": "on",
    "outlet": "pdu1:1",
    "status": "completed",
    "created_time": "2026-01-30T10:20:00Z",
    "started_time": "2026-01-30T10:20:05Z", 
    "completed_time": "2026-01-30T10:20:08Z",
    "description": "PDU Power On for Web Server 1",
    "retry_count": 0,
    "max_retries": 3,
    "depends_on": null
  },
  {
    "id": "job-12346",
    "type": "IPMI", 
    "server_id": "server-01",
    "action": "on",
    "status": "failed",
    "error": "IPMI connection timeout",
    "last_error": "Connection refused",
    "retry_count": 2,
    "max_retries": 3,
    "depends_on": "job-12345"
  }
]
```

### Get Job Details

**GET** `/jobs/:id`

Get details for a specific job.

**Parameters:**
- `id` (string): Job ID

**Response:**
```json
{
  "id": "job-12345",
  "type": "IPMI",
  "server_id": "server-01", 
  "action": "on",
  "status": "running",
  "created_time": "2026-01-30T10:20:00Z",
  "started_time": "2026-01-30T10:20:05Z",
  "description": "IPMI Power On for Web Server 1",
  "retry_count": 1,
  "max_retries": 3
}
```

**Error Responses:**
- `404 Not Found`: Job not found

### Get Server Jobs

**GET** `/servers/:id/jobs`

Get all jobs for a specific server.

**Parameters:**
- `id` (string): Server ID

**Response:**
```json
[
  {
    "id": "job-12345",
    "type": "PDU_ON",
    "server_id": "server-01",
    "status": "completed"
  },
  {
    "id": "job-12346", 
    "type": "IPMI",
    "server_id": "server-01",
    "status": "running"
  }
]
```

### Cancel Job

**DELETE** `/jobs/:id`

Cancel a pending or running job.

**Parameters:**
- `id` (string): Job ID

**Response:**
```json
{
  "status": "cancelled"
}
```

**Error Responses:**
- `400 Bad Request`: Job cannot be cancelled (already completed/failed)
- `404 Not Found`: Job not found

### Retry Failed Job

**POST** `/jobs/:id/retry`

Retry a failed job.

**Parameters:**
- `id` (string): Job ID

**Response:**
```json
{
  "status": "job retry initiated"
}
```

**Error Responses:**
- `400 Bad Request`: Job is not in failed state
- `404 Not Found`: Job not found

### Execute Job Immediately

**POST** `/jobs/:id/run-now`

Mark a pending job for immediate execution.

**Parameters:**
- `id` (string): Job ID

**Response:**
```json
{
  "status": "marked for immediate execution"
}
```

### Schedule Job

**POST** `/jobs/schedule`

Schedule a job to run at a specific time.

**Request Body:**
```json
{
  "server_id": "server-01",
  "action": "on|off|reboot",
  "scheduled_time": "2026-01-30T22:00:00Z",
  "description": "Scheduled maintenance reboot"
}
```

**Response:**
```json
{
  "status": "scheduled",
  "jobs": [
    {
      "id": "job-12349",
      "scheduled_time": "2026-01-30T22:00:00Z"
    }
  ],
  "message": "Scheduled 1 job(s) for 2026-01-30 22:00:00"
}
```

## PXE Boot Integration

### Get Boot Script

**GET** `/boot/:mac`

Get iPXE boot script for a server based on MAC address.

**Parameters:**
- `mac` (string): MAC address (e.g., "aa:bb:cc:dd:ee:01")

**GET** `/autoexec.ipxe`

Standard iPXE auto-execution endpoint (recommended for integration).

**Parameters:**
- `mac` (query, required): MAC address (e.g., "aa:bb:cc:dd:ee:01")

**Response:** (Content-Type: text/plain)

The response depends on the server configuration:

**Default Response:**
```
#!ipxe

echo Lab Manager - Booting Web Server 1
echo MAC: aa:bb:cc:dd:ee:01
echo Role: webserver
echo Hostname: web1.lab.local
echo Incoming IP: 192.168.1.50

# Set hostname
set hostname web1.lab.local

# Boot from network or local disk based on configuration
sanboot --no-describe --drive 0x80 ||

echo Boot failed
shell
```

**Chain Loading Response (with next-path):**
```
#!ipxe

echo Lab Manager - Booting NixOS Development Server
echo MAC: aa:bb:cc:dd:ee:04
echo Role: development
echo Hostname: nixos-dev1
echo Incoming IP: 192.168.1.50

# Chain load to custom path
echo Chaining to: http://netboot.nixos.org/ipxe?hostname=nixos-dev1&mac=aa:bb:cc:dd:ee:04&role=development
chain http://netboot.nixos.org/ipxe?hostname=nixos-dev1&mac=aa:bb:cc:dd:ee:04&role=development ||

echo Chain load failed, booting from local disk
sanboot --no-describe --drive 0x80 ||

echo Boot failed
shell
```

**Custom Template Response (with boot-template):**
```
#!ipxe

echo Lab Manager - Custom Ubuntu Boot
echo Hostname: ubuntu1
echo MAC: aa:bb:cc:dd:ee:05
echo Incoming IP: 192.168.1.50
echo Ubuntu Version: 22.04

# Set network configuration
set hostname ubuntu1
set net0/ip 192.168.1.50

# Boot Ubuntu autoinstall
kernel http://archive.ubuntu.com/ubuntu/dists/jammy/main/installer-amd64/current/legacy-images/netboot/ubuntu-installer/amd64/linux
initrd http://archive.ubuntu.com/ubuntu/dists/jammy/main/installer-amd64/current/legacy-images/netboot/ubuntu-installer/amd64/initrd.gz
imgargs linux auto=true hostname=ubuntu1 preseed/url=http://lab.example.com/preseed/webserver.cfg
boot
```

**Template Variables:**
Available variables for use in `next-path` and `boot-template` metadata fields:

- `{{.MAC}}` - Server MAC address
- `{{.Hostname}}` - Server hostname (from metadata or name)
- `{{.Role}}` - Server role from metadata
- `{{.ServerName}}` - Server display name
- `{{.ServerID}}` - Server ID
- `{{.Description}}` - Server description
- `{{.IncomingServerIP}}` - IP address of the requesting client
- `{{.Metadata.fieldname}}` - Any custom metadata field

**Configuration Examples:**

1. **Chain Loading to External Service:**
```yaml
metadata:
  mac: "aa:bb:cc:dd:ee:04"
  hostname: "nixos-dev1"
  role: "development"
  next-path: "http://netboot.nixos.org/ipxe?hostname={{.Hostname}}&mac={{.MAC}}&role={{.Role}}"
```

2. **Custom Boot Template:**
```yaml
metadata:
  mac: "aa:bb:cc:dd:ee:05"
  hostname: "ubuntu1"
  role: "webserver"
  ubuntu_version: "22.04"
  boot-template: |
    #!ipxe
    echo Custom boot for {{.Hostname}}
    echo Version: {{.Metadata.ubuntu_version}}
    kernel http://example.com/vmlinuz hostname={{.Hostname}}
    initrd http://example.com/initrd.gz
    boot
```

**Error Responses:**
- Returns fallback boot script for unknown servers
- Returns error message for malformed MAC addresses

## Integration Examples

### Python Example

```python
import requests
import json

base_url = "http://labmgr.example.com:8098/api/v1"

def power_on_server(server_id):
    """Power on a server"""
    url = f"{base_url}/servers/{server_id}/power"
    payload = {"action": "on"}
    
    response = requests.post(url, json=payload)
    if response.status_code == 200:
        result = response.json()
        print(f"Queued {len(result['jobs'])} jobs")
        return result['jobs']
    else:
        print(f"Error: {response.text}")
        return None

def get_server_status(server_id, force_refresh=False):
    """Get server status"""
    url = f"{base_url}/servers/{server_id}/status"
    params = {"force": "true"} if force_refresh else {}
    
    response = requests.get(url, params=params)
    if response.status_code == 200:
        return response.json()["status"]
    else:
        return None

def wait_for_jobs(job_ids, timeout=300):
    """Wait for jobs to complete"""
    import time
    start_time = time.time()
    
    while time.time() - start_time < timeout:
        all_complete = True
        for job_id in job_ids:
            response = requests.get(f"{base_url}/jobs/{job_id}")
            if response.status_code == 200:
                status = response.json()["status"]
                if status in ["pending", "running"]:
                    all_complete = False
                elif status == "failed":
                    print(f"Job {job_id} failed")
                    return False
        
        if all_complete:
            return True
        
        time.sleep(5)
    
    return False

# Usage example
if __name__ == "__main__":
    # Power on server
    jobs = power_on_server("server-01")
    if jobs:
        job_ids = [job["id"] for job in jobs]
        print("Waiting for jobs to complete...")
        
        if wait_for_jobs(job_ids):
            status = get_server_status("server-01", force_refresh=True)
            print(f"Server status: {status}")
        else:
            print("Jobs did not complete successfully")
```

### Bash/curl Examples

```bash
#!/bin/bash

BASE_URL="http://localhost:8098/api/v1"

# Function to power on server
power_on_server() {
    local server_id=$1
    curl -X POST "${BASE_URL}/servers/${server_id}/power" \
         -H "Content-Type: application/json" \
         -d '{"action": "on"}'
}

# Function to get server status
get_server_status() {
    local server_id=$1
    curl -s "${BASE_URL}/servers/${server_id}/status" | jq -r '.status'
}

# Function to list all jobs
list_jobs() {
    curl -s "${BASE_URL}/jobs" | jq '.'
}

# Function to get all server statuses
get_all_status() {
    curl -s "${BASE_URL}/status?force=true" | jq '.'
}

# Usage examples
echo "Powering on server-01..."
power_on_server "server-01"

echo "Checking status..."
status=$(get_server_status "server-01")
echo "Server status: $status"

echo "Recent jobs:"
list_jobs | jq '.[] | select(.server_id == "server-01") | {id, type, status, description}'
```

### Ansible Integration

```yaml
---
- name: Manage lab servers via Lab Manager API
  hosts: localhost
  vars:
    labmgr_url: "http://labmgr.example.com:8098/api/v1"
    
  tasks:
    - name: Power on server
      uri:
        url: "{{ labmgr_url }}/servers/{{ server_id }}/power"
        method: POST
        body_format: json
        body:
          action: "on"
      register: power_result
      
    - name: Wait for server to be online
      uri:
        url: "{{ labmgr_url }}/servers/{{ server_id }}/status"
      register: status_result
      until: status_result.json.status == "on"
      retries: 20
      delay: 30
      
    - name: Get job status
      uri:
        url: "{{ labmgr_url }}/jobs/{{ item.id }}"
      loop: "{{ power_result.json.jobs }}"
      register: job_results
```

## Rate Limiting and Best Practices

### API Limits
- No explicit rate limiting currently implemented
- Recommended: No more than 10 requests/second per client
- Bulk operations preferred over individual calls

### Best Practices
1. **Use force refresh sparingly**: Only when fresh data is critical
2. **Poll job status**: Wait for job completion before next action
3. **Handle timeouts**: IPMI and PDU operations can take 30+ seconds
4. **Check dependencies**: Ensure prerequisite jobs complete first
5. **Monitor errors**: Implement retry logic for network failures

### Error Handling
- Always check HTTP status codes
- Parse JSON error responses for details
- Implement exponential backoff for retries
- Log API interactions for debugging

## Webhooks (Future Enhancement)

*Note: Webhook support is planned for future releases*

Planned webhook events:
- Server status changes
- Job completion/failure
- Device unreachable alerts
- Configuration changes