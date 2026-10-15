<template>
  <div :class="['container', { 'with-connection-status': showConnectionStatus }]">
    <!-- Connection Status Notification -->
    <div :class="['connection-status', connectionStatus, { show: showConnectionStatus }]">
      <span v-if="connectionStatus === 'disconnected'">
        ⚠️ Connection lost - showing cached data
        <button class="retry-btn" @click="manualRetry">Retry</button>
      </span>
      <span v-else-if="connectionStatus === 'reconnecting'">
        🔄 Reconnecting...
      </span>
      <span v-else-if="connectionStatus === 'connected'">
        ✅ Connected
      </span>
    </div>
    
    <h1>Lab Manager</h1>
    
    <!-- View Controls -->
    <div class="header-controls">
      <div class="view-controls">
        <div class="view-toggle">
          <button @click="viewMode = 'grid'" :class="{ active: viewMode === 'grid' }">🖥️ Grid</button>
          <button @click="viewMode = 'table'" :class="{ active: viewMode === 'table' }">📋 Table</button>
          <button @click="viewMode = 'os-management'" :class="{ active: viewMode === 'os-management' }">💿 OS Management</button>
        </div>
      </div>
      <div v-if="viewMode !== 'os-management'" class="refresh-controls">
        <button class="btn btn-muted" @click="refreshAll" :disabled="refreshing || isDisabledDueToConnection()">
          {{ refreshing ? 'Refreshing...' : isDisabledDueToConnection() ? 'Offline' : 'Refresh All' }}
        </button>
      </div>
      <div v-else class="os-management-controls">
        <button class="btn btn-primary" @click="newTemplate">
          + New Template
        </button>
        <button class="btn btn-muted" @click="loadOSData" :disabled="loading">
          {{ loading ? 'Loading...' : 'Refresh' }}
        </button>
      </div>
    </div>
    
    <!-- Job Drawer Toggle -->
    <button class="job-drawer-toggle" @click="toggleJobDrawer">
      <div class="hamburger">
        <span></span>
        <span></span>
        <span></span>
      </div>
    </button>
    
    <!-- Job Drawer -->
    <div :class="['job-drawer', { open: jobDrawerOpen }]">
      <div class="job-drawer-header">
        <div class="job-drawer-title">Job Queue</div>
        <button class="job-drawer-close" @click="toggleJobDrawer">&times;</button>
      </div>
      <div class="job-drawer-content">
        <!-- Active Jobs -->
        <div v-if="activeJobs.length > 0" class="job-section">
          <div class="job-section-title">Active Jobs ({{ activeJobs.length }})</div>
          <div 
            v-for="job in activeJobs" 
            :key="job.id"
            :class="['drawer-job-item', getJobClass(job.status)]"
            @click="openJobModal(job)"
          >
            <div class="drawer-job-header">
              <span class="drawer-job-id">{{ job.id }}</span>
              <span :class="['drawer-job-status', getJobClass(job.status)]">
                {{ job.status }}
              </span>
            </div>
            <div class="drawer-job-description">{{ job.description }}</div>
            <div v-if="job.status === 'waiting' && job.not_before" class="drawer-job-waiting">
              ⏱️ Waiting: {{ formatWaitingTime(job) }}
            </div>
            <div class="drawer-job-server">{{ getServerName(job.server_id) }}</div>
          </div>
        </div>
        
        <!-- Recent Jobs -->
        <div v-if="recentJobs.length > 0" class="job-section">
          <div class="job-section-title">Recently Completed ({{ recentJobs.length }})</div>
          <div 
            v-for="job in recentJobs" 
            :key="job.id"
            :class="['drawer-job-item', getJobClass(job.status)]"
            @click="openJobModal(job)"
          >
            <div class="drawer-job-header">
              <span class="drawer-job-id">{{ job.id }}</span>
              <span :class="['drawer-job-status', getJobClass(job.status)]">
                {{ job.status }}
              </span>
            </div>
            <div class="drawer-job-description">{{ job.description }}</div>
            <div v-if="job.status === 'waiting' && job.not_before" class="drawer-job-waiting">
              ⏱️ Waiting: {{ formatWaitingTime(job) }}
            </div>
            <div class="drawer-job-server">{{ getServerName(job.server_id) }}</div>
          </div>
        </div>
        
        <div v-if="activeJobs.length === 0 && recentJobs.length === 0" style="text-align: center; color: #666; padding: 40px;">
          No jobs found
        </div>
      </div>
    </div>
    
    <!-- Job Modal -->
    <div v-if="selectedJob" class="modal-overlay" @click="closeJobModal">
      <div class="modal" @click.stop>
        <div class="modal-header">
          <div class="modal-title">Job Details</div>
          <button class="modal-close" @click="closeJobModal">&times;</button>
        </div>
        <div class="modal-body">
          <div class="job-detail">
            <div class="job-detail-label">Job ID:</div>
            <div class="job-detail-value">{{ selectedJob.id }}</div>
          </div>
          <div class="job-detail">
            <div class="job-detail-label">Status:</div>
            <div class="job-detail-value">
              <span :class="['drawer-job-status', getJobClass(selectedJob.status)]">
                {{ selectedJob.status }}
              </span>
            </div>
          </div>
          <div class="job-detail">
            <div class="job-detail-label">Description:</div>
            <div class="job-detail-value">{{ selectedJob.description }}</div>
          </div>
          <div class="job-detail">
            <div class="job-detail-label">Server:</div>
            <div class="job-detail-value">{{ getServerName(selectedJob.server_id) }}</div>
          </div>
          <div class="job-detail">
            <div class="job-detail-label">Type:</div>
            <div class="job-detail-value">{{ selectedJob.type }}</div>
          </div>
          <div class="job-detail">
            <div class="job-detail-label">Action:</div>
            <div class="job-detail-value">{{ selectedJob.action }}</div>
          </div>
          <div class="job-detail">
            <div class="job-detail-label">Created:</div>
            <div class="job-detail-value">{{ formatDate(selectedJob.created_at) }}</div>
          </div>
          <div v-if="selectedJob.started_at" class="job-detail">
            <div class="job-detail-label">Started:</div>
            <div class="job-detail-value">{{ formatDate(selectedJob.started_at) }}</div>
          </div>
          <div v-if="selectedJob.completed_at" class="job-detail">
            <div class="job-detail-label">Completed:</div>
            <div class="job-detail-value">{{ formatDate(selectedJob.completed_at) }}</div>
          </div>
          <div v-if="selectedJob.not_before" class="job-detail">
            <div class="job-detail-label">Not Before:</div>
            <div class="job-detail-value">{{ formatDate(selectedJob.not_before) }}</div>
          </div>
          <div v-if="selectedJob.depends_on" class="job-detail">
            <div class="job-detail-label">Depends On:</div>
            <div class="job-detail-value">{{ selectedJob.depends_on }}</div>
          </div>
          <div v-if="selectedJob.error" class="job-detail">
            <div class="job-detail-label">Error:</div>
            <div class="job-detail-value" style="color: #dc3545;">{{ selectedJob.error }}</div>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-secondary" @click="closeJobModal">Close</button>
          <button 
            v-if="canCancelJob(selectedJob)"
            class="btn btn-danger" 
            @click="cancelJob(selectedJob.id)"
            :disabled="jobActionLoading"
          >
            Cancel
          </button>
          <button 
            v-if="canRunNowJob(selectedJob)"
            class="btn btn-warning" 
            @click="runJobNow(selectedJob.id)"
            :disabled="jobActionLoading"
          >
            Run Now
          </button>
          <button 
            v-if="canRetryJob(selectedJob)"
            class="btn btn-primary" 
            @click="retryJob(selectedJob.id)"
            :disabled="jobActionLoading"
          >
            Retry Job
          </button>
        </div>
      </div>
    </div>

    <!-- Error Modal -->
    <div v-if="errorModal.show" class="modal-overlay" @click="closeErrorModal">
      <div class="modal-content" @click.stop>
        <div class="modal-header">
          <div class="modal-title">{{ errorModal.title }}</div>
          <button class="modal-close" @click="closeErrorModal">&times;</button>
        </div>
        <div class="modal-body">
          <div class="error-detail">
            <div class="error-detail-label">Server:</div>
            <div class="error-detail-value">{{ errorModal.serverName }}</div>
          </div>
          <div class="error-detail">
            <div class="error-detail-label">Status:</div>
            <div class="error-detail-value">{{ errorModal.status }}</div>
          </div>
          <div class="error-detail">
            <div class="error-detail-label">IPMI Host:</div>
            <div class="error-detail-value">{{ errorModal.ipmiHost }}</div>
          </div>
          <div class="error-detail">
            <div class="error-detail-label">Error:</div>
            <div class="error-detail-value error-text">{{ errorModal.error }}</div>
          </div>
          <div class="error-detail">
            <div class="error-detail-label">Note:</div>
            <div class="error-detail-value">{{ errorModal.note }}</div>
          </div>
        </div>
      </div>
    </div>
    
    <!-- OS Management View -->
    <div v-else-if="viewMode === 'os-management'" class="server-table">
      <table>
        <thead>
          <tr>
            <th class="name-header">Name</th>
            <th class="status-header">Boot Method</th>
            <th class="description-header">Description</th>
            <th class="ip-header">Next Path / Template</th>
            <th class="actions-header">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="template in osTemplates" :key="template.id" class="clickable-row" @click="editTemplate(template)">
            <td class="name-cell">
              <strong>{{ template.name }}</strong>
              <div v-if="template.is_built_in" style="font-size:11px;color:#888;margin-top:2px;">built-in</div>
            </td>
            <td class="status-cell">
              <div :class="['status-badge', 'status-' + template.boot_method]">{{ template.boot_method }}</div>
            </td>
            <td class="description-cell">{{ template.description }}</td>
            <td class="ip-cell" style="font-family:monospace;font-size:12px;max-width:260px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">
              <span v-if="template.next_path" :title="template.next_path">{{ template.next_path }}</span>
              <span v-else-if="template.boot_template" style="color:#666;font-style:italic;">inline template</span>
              <span v-else style="color:#999;">—</span>
            </td>
            <td class="actions-cell">
              <button class="btn btn-secondary" @click.stop="editTemplate(template)">Edit</button>
              <button v-if="!template.is_built_in" class="btn btn-danger" @click.stop="deleteTemplate(template.id)">Delete</button>
            </td>
          </tr>
          <tr v-if="osTemplates.length === 0">
            <td colspan="5" style="text-align:center;color:#999;padding:32px;">No OS templates yet. Click + New Template to create one.</td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Server Details Modal -->
    <div v-if="selectedServer" class="modal-overlay" @click="closeServerModal">
      <div class="modal-content server-details-modal" @click.stop>
        <div class="modal-header">
          <div class="modal-title">Server Details - {{ selectedServer.name }}</div>
          <button class="modal-close" @click="closeServerModal">&times;</button>
        </div>
        <div class="modal-body">
          <!-- Basic Information -->
          <div class="details-section">
            <h3 class="details-section-title">Basic Information</h3>
            <div class="server-detail">
              <div class="server-detail-label">ID:</div>
              <div class="server-detail-value">{{ selectedServer.id }}</div>
            </div>
            <div class="server-detail">
              <div class="server-detail-label">Name:</div>
              <div class="server-detail-value">{{ selectedServer.name }}</div>
            </div>
            <div class="server-detail">
              <div class="server-detail-label">Description:</div>
              <div class="server-detail-value">{{ selectedServer.description || 'N/A' }}</div>
            </div>
          </div>

          <!-- Status Information -->
          <div class="details-section">
            <h3 class="details-section-title">Status Information</h3>
            <div class="server-detail">
              <div class="server-detail-label">Current Status:</div>
              <div class="server-detail-value">
                <span :class="['status-badge', getStatusClass(selectedServer.status)]">
                  {{ selectedServer.status || 'Unknown' }}
                </span>
              </div>
            </div>
            <div v-if="selectedServer.system_status" class="server-detail">
              <div class="server-detail-label">System Status:</div>
              <div class="server-detail-value">{{ selectedServer.system_status || 'Unknown' }}</div>
            </div>
            <div v-if="selectedServer.power_status" class="server-detail">
              <div class="server-detail-label">Power Status:</div>
              <div class="server-detail-value">{{ selectedServer.power_status || 'Unknown' }}</div>
            </div>
            <div v-if="selectedServer.last_checked" class="server-detail">
              <div class="server-detail-label">Last Checked:</div>
              <div class="server-detail-value">{{ formatDate(selectedServer.last_checked) }}</div>
            </div>
            <div v-if="selectedServer.ipmi_reachable !== undefined" class="server-detail">
              <div class="server-detail-label">IPMI Reachable:</div>
              <div class="server-detail-value">
                <span :class="selectedServer.ipmi_reachable ? 'status-good' : 'status-bad'">
                  {{ selectedServer.ipmi_reachable ? 'Yes' : 'No' }}
                </span>
              </div>
            </div>
            <div v-if="selectedServer.pdu_reachable !== undefined" class="server-detail">
              <div class="server-detail-label">PDU Reachable:</div>
              <div class="server-detail-value">
                <span :class="selectedServer.pdu_reachable ? 'status-good' : 'status-bad'">
                  {{ selectedServer.pdu_reachable ? 'Yes' : 'No' }}
                </span>
              </div>
            </div>
          </div>

          <!-- IPMI Configuration -->
          <div v-if="selectedServer.ipmi && selectedServer.ipmi.host" class="details-section">
            <h3 class="details-section-title">IPMI Configuration</h3>
            <div class="server-detail">
              <div class="server-detail-label">Host:</div>
              <div class="server-detail-value code">{{ selectedServer.ipmi.host }}</div>
            </div>
            <div class="server-detail">
              <div class="server-detail-label">Username:</div>
              <div class="server-detail-value code">{{ selectedServer.ipmi.username }}</div>
            </div>
            <div v-if="selectedServer.ipmi_error" class="server-detail">
              <div class="server-detail-label">Last Error:</div>
              <div class="server-detail-value error-text">{{ selectedServer.ipmi_error }}</div>
            </div>
          </div>

          <!-- PDU Configuration -->
          <div v-if="selectedServer.pdu_outlets && selectedServer.pdu_outlets.length > 0" class="details-section">
            <h3 class="details-section-title">PDU Configuration</h3>
            <div class="server-detail">
              <div class="server-detail-label">Outlets:</div>
              <div class="server-detail-value">
                <div v-for="(outlet, index) in selectedServer.pdu_outlets" :key="index" class="outlet-item">
                  <span class="code">{{ outlet }}</span>
                </div>
              </div>
            </div>
            <div v-if="selectedServer.pdu_error" class="server-detail">
              <div class="server-detail-label">Last Error:</div>
              <div class="server-detail-value error-text">{{ selectedServer.pdu_error }}</div>
            </div>
          </div>

          <!-- OS Assignment -->
          <div class="details-section">
            <h3 class="details-section-title">OS Assignment</h3>
            <div v-if="getServerOSAssignment(selectedServer.id)" class="server-detail">
              <div class="server-detail-label">Assigned OS:</div>
              <div class="server-detail-value">
                <div class="assigned-os-info">
                  <div class="os-name">{{ getServerOSAssignment(selectedServer.id).template.name }}</div>
                  <div class="os-method">{{ getServerOSAssignment(selectedServer.id).template.boot_method }}</div>
                </div>
              </div>
            </div>
            <div v-else class="server-detail">
              <div class="server-detail-label">Assigned OS:</div>
              <div class="server-detail-value">
                <span class="no-os-assignment">Local Boot (Default)</span>
              </div>
            </div>
            <div v-if="getServerOSAssignment(selectedServer.id)" class="server-detail">
              <div class="server-detail-label">Template ID:</div>
              <div class="server-detail-value code">{{ getServerOSAssignment(selectedServer.id).template.id }}</div>
            </div>
            <div v-if="getServerOSAssignment(selectedServer.id)" class="server-detail">
              <div class="server-detail-label">Assigned:</div>
              <div class="server-detail-value">{{ formatDate(getServerOSAssignment(selectedServer.id).assignment.assigned_at) }}</div>
            </div>
            <div v-if="getServerOSAssignment(selectedServer.id)" class="server-detail">
              <div class="server-detail-label">Assigned By:</div>
              <div class="server-detail-value">{{ getServerOSAssignment(selectedServer.id).assignment.assigned_by || 'System' }}</div>
            </div>
            <div class="server-detail">
              <div class="server-detail-label">Quick Change:</div>
              <div class="server-detail-value">
                <select 
                  class="os-select" 
                  :value="getServerOSAssignment(selectedServer.id)?.assignment.template_id || ''" 
                  @change="assignOSToServer(selectedServer.id, $event.target.value)"
                >
                  <option value="">Local Boot (Default)</option>
                  <option 
                    v-for="template in osTemplates" 
                    :key="template.id" 
                    :value="template.id"
                  >
                    {{ template.name }}
                  </option>
                </select>
              </div>
            </div>
            <div v-if="getServerOSAssignment(selectedServer.id) && getServerOSAssignment(selectedServer.id).template.description" class="server-detail">
              <div class="server-detail-label">Description:</div>
              <div class="server-detail-value">{{ getServerOSAssignment(selectedServer.id).template.description }}</div>
            </div>
          </div>

          <!-- Metadata -->
          <div v-if="selectedServer.metadata && Object.keys(selectedServer.metadata).length > 0" class="details-section">
            <h3 class="details-section-title">Metadata</h3>
            <div v-for="(value, key) in selectedServer.metadata" :key="key" class="server-detail">
              <div class="server-detail-label">{{ key }}:</div>
              <div class="server-detail-value">
                <span v-if="key.includes('path') || key.includes('template')" class="code">{{ value }}</span>
                <span v-else>{{ value }}</span>
              </div>
            </div>
          </div>

          <!-- Recent Jobs -->
          <div v-if="getServerJobs(selectedServer.id).length > 0" class="details-section">
            <h3 class="details-section-title">Recent Jobs (Last 5)</h3>
            <div class="jobs-list">
              <div 
                v-for="job in getServerJobs(selectedServer.id).slice(0, 5)" 
                :key="job.id"
                :class="['job-item-details', getJobClass(job.status)]"
                @click="openJobModal(job)"
              >
                <div class="job-header-details">
                  <span class="job-id">{{ job.id }}</span>
                  <span :class="['job-status', getJobClass(job.status)]">{{ job.status }}</span>
                </div>
                <div class="job-description">{{ job.description }}</div>
                <div class="job-time">{{ formatDate(job.created_at) }}</div>
              </div>
            </div>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-secondary" @click="closeServerModal">Close</button>
        </div>
      </div>
    </div>

    <!-- OS Template Create/Edit Modal -->
    <div v-if="showTemplateModal" class="modal-overlay" @click="closeTemplateModal">
      <div class="modal-content" @click.stop style="max-width:600px;">
        <div class="modal-header">
          <div class="modal-title">{{ selectedTemplate && selectedTemplate.id ? 'Edit Template' : 'New OS Template' }}</div>
          <button class="modal-close" @click="closeTemplateModal">&times;</button>
        </div>
        <div class="modal-body">
          <div class="details-section">
            <div class="server-detail">
              <div class="server-detail-label">Name</div>
              <div class="server-detail-value">
                <input type="text" v-model="templateForm.name" placeholder="e.g. NixOS Installer" style="width:100%;padding:6px 8px;border:1px solid #ced4da;border-radius:4px;font-size:14px;" />
              </div>
            </div>
            <div class="server-detail">
              <div class="server-detail-label">Description</div>
              <div class="server-detail-value">
                <input type="text" v-model="templateForm.description" placeholder="Short description" style="width:100%;padding:6px 8px;border:1px solid #ced4da;border-radius:4px;font-size:14px;" />
              </div>
            </div>
            <div class="server-detail">
              <div class="server-detail-label">Boot Method</div>
              <div class="server-detail-value">
                <select v-model="templateForm.boot_method" style="width:100%;padding:6px 8px;border:1px solid #ced4da;border-radius:4px;font-size:14px;">
                  <option value="local">local — boot from local disk</option>
                  <option value="chain">chain — chain-load a remote URL</option>
                  <option value="template">template — custom iPXE script</option>
                </select>
              </div>
            </div>
            <div v-if="templateForm.boot_method === 'chain'" class="server-detail">
              <div class="server-detail-label">Next Path / URL</div>
              <div class="server-detail-value">
                <input type="text" v-model="templateForm.next_path" placeholder="e.g. http://&#123;&#123;.IncomingServerIP&#125;&#125;/nixos/boot.ipxe" style="width:100%;padding:6px 8px;border:1px solid #ced4da;border-radius:4px;font-size:14px;font-family:monospace;" />
                <div style="font-size:11px;color:#666;margin-top:4px;" v-pre>Variables: {{.MAC}}, {{.Hostname}}, {{.Role}}, {{.IncomingServerIP}}</div>
              </div>
            </div>
            <div v-if="templateForm.boot_method === 'template'" class="server-detail">
              <div class="server-detail-label">Boot Template (iPXE)</div>
              <div class="server-detail-value">
                <textarea v-model="templateForm.boot_template" rows="8" placeholder="#!ipxe" style="width:100%;padding:6px 8px;border:1px solid #ced4da;border-radius:4px;font-size:13px;font-family:monospace;resize:vertical;"></textarea>
                <div style="font-size:11px;color:#666;margin-top:4px;" v-pre>Variables: {{.MAC}}, {{.Hostname}}, {{.Role}}, {{.IncomingServerIP}}</div>
              </div>
            </div>
          </div>
          <div v-if="templateFormError" style="color:#dc3545;margin-top:8px;font-size:14px;">{{ templateFormError }}</div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-secondary" @click="closeTemplateModal">Cancel</button>
          <button class="btn btn-primary" @click="saveTemplate" :disabled="templateFormSaving">
            {{ templateFormSaving ? 'Saving...' : 'Save Template' }}
          </button>
        </div>
      </div>
    </div>

    <!-- Loading State -->
    <div v-if="loading" style="text-align: center; padding: 40px;">
      Loading servers...
    </div>
    
    <!-- Server Views -->
    <div v-else-if="viewMode === 'grid'" class="server-grid">
      <div v-for="server in servers" :key="server.id" class="server-card" :data-server-id="server.id" @click="openServerDetails(server)">
        <div class="server-header">
          <div class="server-name">{{ server.name }}</div>
          <div :class="getStatusClass(server.status)" :key="server.id + '_' + server.status">
            {{ server.status || 'Unknown' }}
            <span 
              v-if="hasRecentFailures(server)" 
              class="error-indicator" 
              @click.stop="showErrorDetails(server)"
              :title="getErrorTooltip(server)"
            >
              ⚠️
            </span>
          </div>
        </div>
        
        <div class="server-info">
          <div class="info-item"><strong>ID:</strong> {{ server.id }}</div>
          <div class="info-item"><strong>Description:</strong> {{ server.description }}</div>
          <div class="info-item"><strong>IPMI Host:</strong> {{ server.ipmi.host }}</div>
          <div v-if="server.pdu_outlets && server.pdu_outlets.length" class="info-item">
            <strong>PDU Outlets:</strong> {{ server.pdu_outlets.join(', ') }}
          </div>
        </div>

        <div class="actions">
          <button 
            class="btn btn-success" 
            @click.stop="performAction(server.id, 'on')"
            :disabled="!canPowerOn(server)"
          >
            Power On
          </button>
          <button 
            class="btn btn-danger" 
            @click.stop="performAction(server.id, 'off')"
            :disabled="!canPowerOff(server)"
          >
            Power Off
          </button>
          <!-- Server Menu -->
          <div :class="['server-menu', { active: openMenus[server.id] }]">
            <button 
              class="server-menu-btn" 
              @click.stop="toggleServerMenu(server.id)"
              :disabled="isServerDisabled(server)"
            >
              ⋯
            </button>
            <div :class="['server-menu-dropdown', { show: openMenus[server.id] }]">
              <div class="server-menu-header">Actions</div>
              <div 
                class="server-menu-item" 
                @click.stop="openServerDetails(server), toggleServerMenu(server.id)"
              >
                📋 Details
              </div>
              <div 
                :class="['server-menu-item', { disabled: !isServerOn(server) }]" 
                @click.stop="isServerOn(server) && (performAction(server.id, 'reboot'), toggleServerMenu(server.id))"
              >
                Reboot
              </div>
              <div v-if="hasIPMIControl(server)" class="server-menu-header">IPMI Controls</div>
              <div 
                v-if="hasIPMIControl(server)"
                class="server-menu-item" 
                @click.stop="performIPMIAction(server.id, 'on'), toggleServerMenu(server.id)"
              >
                IPMI Power On
              </div>
              <div 
                v-if="hasIPMIControl(server)"
                class="server-menu-item" 
                @click.stop="performIPMIAction(server.id, 'off'), toggleServerMenu(server.id)"
              >
                IPMI Power Off
              </div>
              <div 
                v-if="hasIPMIControl(server)"
                class="server-menu-item" 
                @click.stop="performIPMIAction(server.id, 'reboot'), toggleServerMenu(server.id)"
              >
                IPMI Reset
              </div>
              <div v-if="hasPDUControl(server)" class="server-menu-header">PDU Outlets</div>
              <div 
                v-if="hasPDUControl(server)"
                class="server-menu-item" 
                @click.stop="performPDUAction(server.id, 'on'), toggleServerMenu(server.id)"
              >
                Turn On PDU Outlets
              </div>
              <div 
                v-if="hasPDUControl(server)"
                class="server-menu-item" 
                @click.stop="performPDUAction(server.id, 'off'), toggleServerMenu(server.id)"
              >
                Turn Off PDU Outlets
              </div>
              <div class="server-menu-header">Scheduled Actions</div>
              <div 
                :class="['server-menu-item', { disabled: !isServerOn(server) }]" 
                @click.stop="isServerOn(server) && scheduleAction(server.id, 'off', 1)"
              >
                Power off in 1 hour
              </div>
              <div 
                :class="['server-menu-item', { disabled: !isServerOn(server) }]" 
                @click.stop="isServerOn(server) && scheduleAction(server.id, 'off', 2)"
              >
                Power off in 2 hours
              </div>
              <div 
                :class="['server-menu-item', { disabled: !isServerOn(server) }]" 
                @click.stop="isServerOn(server) && scheduleAction(server.id, 'off', 4)"
              >
                Power off in 4 hours
              </div>
              <div 
                :class="['server-menu-item', { disabled: !isServerOn(server) }]" 
                @click.stop="isServerOn(server) && scheduleAction(server.id, 'off', 8)"
              >
                Power off in 8 hours
              </div>
              <div 
                :class="['server-menu-item', { disabled: !isServerOn(server) }]" 
                @click.stop="isServerOn(server) && scheduleAction(server.id, 'off', 12)"
              >
                Power off in 12 hours
              </div>
            </div>
          </div>
        </div>

        <!-- Progress Indicator -->
        <div v-if="hasActiveJobs(server.id)" class="progress-indicator">
          <div class="spinner"></div>
          <span>{{ getActiveJobsText(server.id) }}</span>
        </div>

        <div v-if="serverJobs[server.id] && serverJobs[server.id].length > 0" class="jobs-section">
          <strong>Active Jobs:</strong>
          <div class="job-list">
            <div 
              v-for="job in serverJobs[server.id]" 
              :key="job.id"
              :class="getJobClass(job.status)"
            >
              <span class="job-description">{{ job.description }}</span>
              <span class="job-time">{{ formatJobTime(job) }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
    
    <!-- Table View -->
    <div v-else-if="viewMode === 'table'" class="server-table">
      <table>
        <thead>
          <tr>
            <th class="name-header">Name</th>
            <th class="status-header">Status</th>
            <th class="description-header">Description</th>
            <th class="ip-header">IPMI Host</th>
            <th class="actions-header">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="server in servers" :key="server.id" :data-server-id="server.id" @click="openServerDetails(server)" class="clickable-row">
            <td class="name-cell"><strong>{{ server.name }}</strong></td>
            <td class="status-cell">
              <div :class="getStatusClass(server.status)" :key="server.id + '_' + server.status">
                {{ server.status || 'Unknown' }}
                <span 
                  v-if="hasRecentFailures(server)" 
                  class="error-indicator" 
                  @click.stop="showErrorDetails(server)"
                  :title="getErrorTooltip(server)"
                >
                  ⚠️
                </span>
              </div>
            </td>
            <td class="description-cell">{{ server.description }}</td>
            <td class="ip-cell">{{ server.ipmi.host }}</td>
            <td class="actions-cell">
              <button 
                class="btn btn-success" 
                @click.stop="performAction(server.id, 'on')"
                :disabled="!canPowerOn(server)"
              >
                On
              </button>
              <button 
                class="btn btn-danger" 
                @click.stop="performAction(server.id, 'off')"
                :disabled="!canPowerOff(server)"
              >
                Off
              </button>
              <!-- Server Menu -->
              <div :class="['server-menu', { active: openMenus[server.id] }]">
                <button 
                  class="server-menu-btn" 
                  @click.stop="toggleServerMenu(server.id)"
                  :disabled="isServerDisabled(server)"
                >
                  ⋯
                </button>
                <div :class="['server-menu-dropdown', { show: openMenus[server.id] }]">
                  <div class="server-menu-header">Actions</div>
                  <div 
                    class="server-menu-item" 
                    @click.stop="openServerDetails(server), toggleServerMenu(server.id)"
                  >
                    📋 Details
                  </div>
                  <div 
                    :class="['server-menu-item', { disabled: !isServerOn(server) }]" 
                    @click.stop="isServerOn(server) && (performAction(server.id, 'reboot'), toggleServerMenu(server.id))"
                  >
                    Reboot
                  </div>
                  <div v-if="hasPDUControl(server)" class="server-menu-header">PDU Outlets</div>
                  <div 
                    v-if="hasPDUControl(server)"
                    class="server-menu-item" 
                    @click.stop="performPDUAction(server.id, 'on'), toggleServerMenu(server.id)"
                  >
                    Turn On PDU Outlets
                  </div>
                  <div 
                    v-if="hasPDUControl(server)"
                    class="server-menu-item" 
                    @click.stop="performPDUAction(server.id, 'off'), toggleServerMenu(server.id)"
                  >
                    Turn Off PDU Outlets
                  </div>
                  <div v-if="hasIPMIControl(server)" class="server-menu-header">IPMI Controls</div>
                  <div 
                    v-if="hasIPMIControl(server)"
                    class="server-menu-item" 
                    @click.stop="performIPMIAction(server.id, 'on'), toggleServerMenu(server.id)"
                  >
                    IPMI Power On
                  </div>
                  <div 
                    v-if="hasIPMIControl(server)"
                    class="server-menu-item" 
                    @click.stop="performIPMIAction(server.id, 'off'), toggleServerMenu(server.id)"
                  >
                    IPMI Power Off
                  </div>
                  <div 
                    v-if="hasIPMIControl(server)"
                    class="server-menu-item" 
                    @click.stop="performIPMIAction(server.id, 'reboot'), toggleServerMenu(server.id)"
                  >
                    IPMI Reset
                  </div>
                  <div class="server-menu-header">Scheduled Actions</div>
                  <div 
                    :class="['server-menu-item', { disabled: !isServerOn(server) }]" 
                    @click.stop="isServerOn(server) && scheduleAction(server.id, 'off', 1)"
                  >
                    Power off in 1 hour
                  </div>
                  <div 
                    :class="['server-menu-item', { disabled: !isServerOn(server) }]" 
                    @click.stop="isServerOn(server) && scheduleAction(server.id, 'off', 2)"
                  >
                    Power off in 2 hours
                  </div>
                  <div 
                    :class="['server-menu-item', { disabled: !isServerOn(server) }]" 
                    @click.stop="isServerOn(server) && scheduleAction(server.id, 'off', 4)"
                  >
                    Power off in 4 hours
                  </div>
                  <div 
                    :class="['server-menu-item', { disabled: !isServerOn(server) }]" 
                    @click.stop="isServerOn(server) && scheduleAction(server.id, 'off', 8)"
                  >
                    Power off in 8 hours
                  </div>
                  <div 
                    :class="['server-menu-item', { disabled: !isServerOn(server) }]" 
                    @click.stop="isServerOn(server) && scheduleAction(server.id, 'off', 12)"
                  >
                    Power off in 12 hours
                  </div>
                </div>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    
    <!-- Mobile Bottom Sheet Menu -->
    <template v-if="isMobile()">
      <div v-if="mobileBottomSheet.show" class="mobile-bottom-sheet-overlay" @click="closeMobileBottomSheet"></div>
      <div :class="['mobile-bottom-sheet', { show: mobileBottomSheet.show }]">
      <div class="mobile-bottom-sheet-header">
        <div class="mobile-bottom-sheet-title">{{ mobileBottomSheet.serverName }}</div>
        <button class="mobile-bottom-sheet-close" @click="closeMobileBottomSheet">&times;</button>
      </div>
      <div class="mobile-bottom-sheet-content">
        <div v-if="mobileBottomSheet.server" class="mobile-bottom-sheet-section">
          <div class="mobile-bottom-sheet-section-title">Actions</div>
          <div 
            :class="['mobile-bottom-sheet-item', { disabled: !isServerOn(mobileBottomSheet.server) }]"
            @click="handleMobileAction('reboot')"
          >
            Reboot
          </div>
        </div>
        <div v-if="mobileBottomSheet.server && mobileBottomSheet.server.pdu_outlets && mobileBottomSheet.server.pdu_outlets.length > 0" class="mobile-bottom-sheet-section">
          <div class="mobile-bottom-sheet-section-title">PDU Outlets</div>
          <div 
            class="mobile-bottom-sheet-item"
            @click="handleMobilePDUAction('on')"
          >
            Turn On PDU Outlets
          </div>
          <div 
            class="mobile-bottom-sheet-item"
            @click="handleMobilePDUAction('off')"
          >
            Turn Off PDU Outlets
          </div>
        </div>
        <div v-if="mobileBottomSheet.server" class="mobile-bottom-sheet-section">
          <div class="mobile-bottom-sheet-section-title">Scheduled Actions</div>
          <div 
            :class="['mobile-bottom-sheet-item', { disabled: !isServerOn(mobileBottomSheet.server) }]"
            @click="handleMobileSchedule('off', 1)"
          >
            Power off in 1 hour
          </div>
          <div 
            :class="['mobile-bottom-sheet-item', { disabled: !isServerOn(mobileBottomSheet.server) }]"
            @click="handleMobileSchedule('off', 2)"
          >
            Power off in 2 hours
          </div>
          <div 
            :class="['mobile-bottom-sheet-item', { disabled: !isServerOn(mobileBottomSheet.server) }]"
            @click="handleMobileSchedule('off', 4)"
          >
            Power off in 4 hours
          </div>
          <div 
            :class="['mobile-bottom-sheet-item', { disabled: !isServerOn(mobileBottomSheet.server) }]"
            @click="handleMobileSchedule('off', 8)"
          >
            Power off in 8 hours
          </div>
          <div 
            :class="['mobile-bottom-sheet-item', { disabled: !isServerOn(mobileBottomSheet.server) }]"
            @click="handleMobileSchedule('off', 12)"
          >
            Power off in 12 hours
          </div>
        </div>
      </div>
      </div>
    </template>
  </div>
</template>

<script>
import axios from 'axios'

export default {
  name: 'App',
  data() {
    return {
      servers: [],
      loading: true,
      actionLoading: {},
      serverJobs: {},
      serverFailures: {}, // Track persistent failures per server until success
      jobDrawerOpen: false,
      allJobs: [],
      selectedJob: null,
      errorModal: {
        show: false,
        title: '',
        serverName: '',
        status: '',
        ipmiHost: '',
        error: '',
        note: ''
      },
      selectedServer: null, // For server details modal
      jobActionLoading: false,
      viewMode: this.isMobile() ? 'grid' : 'table', // grid on mobile, table on desktop
      refreshing: false,
      serverDisabled: {}, // Track disabled state per server
      connectionStatus: 'connected', // 'connected', 'disconnected', 'reconnecting'
      showConnectionStatus: false,
      connectionRetryCount: 0,
      lastSuccessfulCall: Date.now(),
      openMenus: {}, // Track which server menus are open
      pendingRequests: new Set(), // Track pending API requests
      
      // OS Management
      osTemplates: [],
      osAssignments: [],
      showTemplateModal: false,
      showBulkAssignModal: false,
      selectedTemplate: null,
      templateForm: { name: '', description: '', boot_method: 'chain', next_path: '', boot_template: '' },
      templateFormError: '',
      templateFormSaving: false,
      retryTimeout: null, // Track active retry timeout
      pollingPaused: false, // Prevent polling floods
      mobileBottomSheet: {
        show: false,
        serverId: null,
        serverName: '',
        server: null
      }
    }
  },
  async mounted() {
    await this.loadServers()
    this.startStatusPolling()
    this.startJobPolling() // This now handles both jobs and server job grouping
    this.startConnectionMonitoring() // Monitor connection health
    
    // Load OS management data
    await this.loadOSData()
    
    // Close menus when clicking outside
    document.addEventListener('click', (event) => {
      if (!event.target.closest('.server-menu')) {
        this.openMenus = {}
      }
    })
  },
  computed: {
    activeJobs() {
      return this.allJobs.filter(job => 
        job.status === 'pending' || job.status === 'waiting' || job.status === 'running'
      )
    },
    recentJobs() {
      const recentTime = Date.now() - (5 * 60 * 1000) // Last 5 minutes
      return this.allJobs
        .filter(job => 
          (job.status === 'completed' || job.status === 'failed') &&
          job.completed_at && new Date(job.completed_at).getTime() > recentTime
        )
        .sort((a, b) => new Date(b.completed_at) - new Date(a.completed_at))
        .slice(0, 10) // Show max 10 recent jobs
    }
  },
  methods: {
    async loadServers() {
      try {
        const response = await axios.get('/api/v1/servers')
        this.servers = response.data.Servers || []
        
        
        // Load initial status for all servers at once
        await this.refreshAllStatus()
      } catch (error) {
        console.error('Failed to load servers:', error)
        alert('Failed to load servers. Please check the backend connection.')
      } finally {
        this.loading = false
      }
    },
    
    async performAction(serverId, action) {
      // Don't proceed if disconnected
      if (this.isDisabledDueToConnection()) {
        return
      }
      
      // 1. Immediately disable the server and show transitional status
      this.serverDisabled[serverId] = true
      this.setTransitionalStatus(serverId, action)
      
      try {
        const endpoint = action === 'reboot' 
          ? `/api/v1/servers/${serverId}/reboot`
          : `/api/v1/servers/${serverId}/power`
        
        const data = action === 'reboot' ? {} : { action }
        
        await this.apiCall(async () => {
          return await axios.post(endpoint, data)
        })
        
        // 2. Start monitoring for completion - when BMC actually changes, enable buttons
        this.monitorForCompletion(serverId, action)
        
      } catch (error) {
        console.error(`Failed to ${action} server:`, error)
        // Only show alert if it's not a connection issue (which shows status bar)
        if (this.connectionStatus === 'connected') {
          alert(`Failed to ${action} server: ${this.errorMessage(error)}`)
        }
        // Re-enable on error
        this.serverDisabled[serverId] = false
      }
    },
    
    async performPDUAction(serverId, action) {
      // Don't proceed if disconnected
      if (this.isDisabledDueToConnection()) {
        return
      }
      
      const server = this.servers.find(s => s.id === serverId)
      if (!server || !server.pdu_outlets || server.pdu_outlets.length === 0) {
        console.error('Server has no PDU outlets configured')
        return
      }
      
      try {
        // Create individual PDU jobs for each outlet with delays to avoid overwhelming PDU
        for (let i = 0; i < server.pdu_outlets.length; i++) {
          const outlet = server.pdu_outlets[i]
          const endpoint = `/api/v1/pdu/${outlet}/power`
          const data = { action }
          
          await this.apiCall(async () => {
            return await axios.post(endpoint, data)
          })
          
          console.log(`PDU ${action} action sent for outlet ${outlet}`)
          
          // Add 1-second delay between API calls to avoid rapid-fire requests
          if (i < server.pdu_outlets.length - 1) {
            await new Promise(resolve => setTimeout(resolve, 1000))
          }
        }
        
        // Refresh job list to show the new PDU jobs
        setTimeout(() => {
          this.loadAllJobs()
          this.updateServerJobsFromGlobal()
        }, 500)
        
      } catch (error) {
        console.error(`Failed to ${action} PDU outlets:`, error)
        if (this.connectionStatus === 'connected') {
          alert(`Failed to ${action} PDU outlets. Please try again.`)
        }
      }
    },
    
    async performIPMIAction(serverId, action) {
      // Don't proceed if disconnected
      if (this.isDisabledDueToConnection()) {
        return
      }
      
      const server = this.servers.find(s => s.id === serverId)
      const hasIPMI = (server.ipmi && server.ipmi.host) || (server.IPMI && server.IPMI.host)
      if (!server || !hasIPMI) {
        console.error('Server has no IPMI configured')
        return
      }
      
      try {
        const endpoint = `/api/v1/servers/${serverId}/ipmi`
        const data = { action }
        
        await this.apiCall(async () => {
          return await axios.post(endpoint, data)
        })
        
        console.log(`Direct IPMI ${action} action sent for server ${serverId}`)
        
        // Refresh job list to show the new IPMI job
        setTimeout(() => {
          this.loadAllJobs()
          this.updateServerJobsFromGlobal()
        }, 500)
        
      } catch (error) {
        console.error(`Failed to perform IPMI ${action}:`, error)
        if (this.connectionStatus === 'connected') {
          alert(`Failed to perform IPMI ${action}. Please try again.`)
        }
      }
    },
    
    setTransitionalStatus(serverId, action) {
      const serverIndex = this.servers.findIndex(s => s.id === serverId)
      if (serverIndex !== -1) {
        let transitionalStatus
        switch (action) {
          case 'on': transitionalStatus = 'Powering On...'; break
          case 'off': transitionalStatus = 'Powering Off...'; break
          case 'reboot': transitionalStatus = 'Rebooting...'; break
          default: transitionalStatus = 'Pending...'
        }
        
        const updatedServer = {
          ...this.servers[serverIndex],
          status: transitionalStatus,
          _statusUpdateTime: Date.now()
        }
        this.servers.splice(serverIndex, 1, updatedServer)
      }
    },
    
    async monitorForCompletion(serverId, action) {
      const expectedFinalStates = {
        'on': ['on', 'poweredon', 'powered'],
        'off': ['off', 'poweredoff'],
        'reboot': ['on', 'poweredon', 'powered'] // After reboot, should be on
      }
      
      const startTime = Date.now()
      const maxWaitTime = 60000 // Max 60 seconds
      
      const checkCompletion = async () => {
        await this.refreshAllStatus()
        
        const server = this.servers.find(s => s.id === serverId)
        const currentStatus = server?.status?.toLowerCase()
        const elapsed = Date.now() - startTime
        
        // Check if server reached expected final state (case-insensitive)
        const reachedFinalState = expectedFinalStates[action]?.includes(currentStatus)
        
        if (server && reachedFinalState) {
          // Server has reached expected final state - enable buttons immediately
          this.serverDisabled[serverId] = false
        } else if (elapsed > maxWaitTime) {
          // Safety timeout - re-enable buttons after max wait time
          this.serverDisabled[serverId] = false
        } else {
          // Not done yet, check again
          setTimeout(checkCompletion, 2000)
        }
      }
      
      // Start checking after 3 seconds
      setTimeout(checkCompletion, 3000)
    },
    
    async refreshAllStatus(forceRefresh = false) {
      if (this.pollingPaused) return
      
      try {
        await this.apiCall(async () => {
          const url = forceRefresh ? '/api/v1/status?force=true' : '/api/v1/status'
          const response = await axios.get(url)
          const statuses = response.data || {}
          
          // Update status for each server
          for (const serverId in statuses) {
            const serverIndex = this.servers.findIndex(s => s.id === serverId)
            if (serverIndex !== -1) {
              const oldStatus = this.servers[serverIndex].status
              const newStatus = statuses[serverId].status
              
              // Check if this is a significant status change that should clear error indicators
              if (this.isSignificantStatusChange(oldStatus, newStatus)) {
                if (this.serverFailures[serverId]) {
                  this.serverFailures[serverId].cleared = true
                  console.log(`Cleared error indicator for ${serverId} due to status change: ${oldStatus} → ${newStatus}`)
                }
              }
              
              const updatedServer = {
                ...this.servers[serverIndex],
                status: newStatus,
                _statusUpdateTime: Date.now()
              }
              this.servers.splice(serverIndex, 1, updatedServer)
            }
          }
          return response
        }, 'refresh-status')
      } catch (error) {
        // Error already handled by apiCall wrapper
      }
    },
    
    async refreshStatus(serverId, showLoading = true, forceRefresh = false) {
      if (showLoading) {
        this.actionLoading[serverId] = true
      }
      
      try {
        const url = forceRefresh 
          ? `/api/v1/servers/${serverId}/status?force=true`
          : `/api/v1/servers/${serverId}/status`
          
        const response = await axios.get(url)
        
        const serverIndex = this.servers.findIndex(s => s.id === serverId)
        if (serverIndex !== -1) {
          const oldStatus = this.servers[serverIndex].status
          
          // Force Vue reactivity by completely replacing the server object
          const updatedServer = {
            ...this.servers[serverIndex],
            status: response.data.status,
            _statusUpdateTime: Date.now()
          }
          
          // Replace the server in the array to trigger reactivity (Vue 3 approach)
          this.servers.splice(serverIndex, 1, updatedServer)
        }
      } catch (error) {
        console.error('Failed to get server status:', error)
        const serverIndex = this.servers.findIndex(s => s.id === serverId)
        if (serverIndex !== -1) {
          const updatedServer = { 
            ...this.servers[serverIndex], 
            status: 'unreachable',
            _statusUpdateTime: Date.now()
          }
          this.servers.splice(serverIndex, 1, updatedServer)
        }
      } finally {
        if (showLoading) {
          this.actionLoading[serverId] = false
        }
      }
    },
    
    getStatusClass(status) {
      const baseClass = 'status-badge '
      
      // Check for transitional states (exclude "powered" as it's not transitional, it's a stable yellow state)
      if (status?.includes('...') || status?.includes('Powering') || status?.includes('Rebooting')) {
        return baseClass + 'status-transitional'
      }
      
      switch (status?.toLowerCase()) {
        case 'on':
        case 'poweredon':
          return baseClass + 'status-on'
        case 'powered':
          return baseClass + 'status-powered' // New yellow state for powered but system unreachable
        case 'off':
        case 'poweredoff':
          return baseClass + 'status-off'
        default:
          return baseClass + 'status-unknown'
      }
    },
    
    startStatusPolling() {
      setInterval(() => {
        // Use bulk status refresh instead of individual calls
        if (!Object.values(this.actionLoading).some(loading => loading) && !this.pollingPaused) {
          this.refreshAllStatus()
        }
      }, 30000)
    },
    
    updateServerJobsFromGlobal() {
      // Reset server jobs but preserve server failures
      this.serverJobs = {}
      
      // Show active jobs and recently completed jobs (last 10 seconds)
      const now = Date.now()
      const recentCompletionTime = now - (10 * 1000) // 10 seconds ago
      
      // Track failures across all jobs, not just recent ones
      const serverLastFailure = {} // Track most recent failure per server
      const serverLastSuccess = {} // Track most recent success per server
      
      // First pass: find the most recent failure and success for each server
      this.allJobs.forEach(job => {
        if (!job.server_id || !job.completed_at) return
        
        const completedTime = new Date(job.completed_at).getTime()
        
        if (job.status === 'failed') {
          if (!serverLastFailure[job.server_id] || completedTime > serverLastFailure[job.server_id].time) {
            serverLastFailure[job.server_id] = {
              time: completedTime,
              job: job
            }
          }
        }
        
        if (job.status === 'completed') {
          if (!serverLastSuccess[job.server_id] || completedTime > serverLastSuccess[job.server_id].time) {
            serverLastSuccess[job.server_id] = {
              time: completedTime,
              job: job
            }
          }
        }
      })
      
      // Second pass: determine if failures should be shown based on most recent success/failure
      Object.keys(serverLastFailure).forEach(serverId => {
        const lastFailure = serverLastFailure[serverId]
        const lastSuccess = serverLastSuccess[serverId]
        
        // Show failure indicator if there's no success after the failure
        const shouldShowFailure = !lastSuccess || lastFailure.time > lastSuccess.time
        
        if (shouldShowFailure) {
          this.serverFailures[serverId] = {
            lastFailureTime: lastFailure.time,
            lastFailureJob: lastFailure.job,
            cleared: false
          }
        } else {
          // Clear the failure if there's a more recent success
          if (this.serverFailures[serverId]) {
            this.serverFailures[serverId].cleared = true
          }
        }
      })
      
      // Third pass: populate visible server jobs
      this.allJobs.forEach(job => {
        if (!job.server_id) return
        
        const isRelevant = 
          // Always show active jobs
          (job.status === 'pending' || job.status === 'waiting' || job.status === 'running') ||
          // Show recently completed jobs (success or failure, last 10 seconds)
          ((job.status === 'failed' || job.status === 'completed') && job.completed_at && 
           new Date(job.completed_at).getTime() > recentCompletionTime)
        
        if (isRelevant) {
          if (!this.serverJobs[job.server_id]) {
            this.serverJobs[job.server_id] = []
          }
          this.serverJobs[job.server_id].push(job)
        }
      })
    },
    
    startJobPolling() {
      // Load all jobs initially
      this.loadAllJobs().then(() => {
        this.updateServerJobsFromGlobal()
      })
      
      // Poll jobs every 5 seconds (increased to reduce load)
      setInterval(() => {
        if (!this.pollingPaused) {
          this.loadAllJobs().then(() => {
            this.updateServerJobsFromGlobal()
          })
        }
      }, 5000)
    },
    
    startConnectionMonitoring() {
      // Monitor connection health every 15 seconds
      setInterval(() => {
        // If we haven't had a successful API call in too long, consider connection lost
        const timeSinceLastSuccess = Date.now() - this.lastSuccessfulCall
        // Increased timeout to 90 seconds to avoid false positives
        if (timeSinceLastSuccess > 90000 && this.connectionStatus === 'connected') { // 90 seconds
          console.log(`Connection appears lost - no successful API calls in ${timeSinceLastSuccess}ms`)
          this.handleConnectionError(new Error('Connection timeout - no successful calls'))
        }
      }, 15000) // Check less frequently
    },
    
    getJobClass(status) {
      return `job-item job-${status}`
    },
    
    formatJobTime(job) {
      if (job.status === 'running' && job.started_at) {
        const start = new Date(job.started_at)
        const elapsed = Math.floor((Date.now() - start.getTime()) / 1000)
        return `${elapsed}s`
      } else if (job.status === 'waiting' && job.not_before) {
        const notBefore = new Date(job.not_before)
        const remaining = Math.max(0, Math.floor((notBefore.getTime() - Date.now()) / 1000))
        return `${remaining}s`
      } else if (job.created_at) {
        const created = new Date(job.created_at)
        const elapsed = Math.floor((Date.now() - created.getTime()) / 1000)
        return `${elapsed}s ago`
      }
      return ''
    },
    
    formatWaitingTime(job) {
      if (job.not_before) {
        const notBefore = new Date(job.not_before)
        const remaining = Math.max(0, Math.floor((notBefore.getTime() - Date.now()) / 1000))
        if (remaining > 60) {
          const minutes = Math.floor(remaining / 60)
          const seconds = remaining % 60
          return `${minutes}m ${seconds}s`
        }
        return `${remaining}s`
      }
      return ''
    },
    
    // Job Drawer Methods
    toggleJobDrawer() {
      this.jobDrawerOpen = !this.jobDrawerOpen
    },
    
    async loadAllJobs() {
      if (this.pollingPaused) return
      
      try {
        await this.apiCall(async () => {
          const response = await axios.get('/api/v1/jobs')
          this.allJobs = response.data || []
          return response
        }, 'load-jobs')
      } catch (error) {
        // Error already handled by apiCall wrapper
        // Keep existing jobs data for offline viewing
      }
    },
    
    isServerDisabled(server) {
      // Disable if no connection to backend
      if (this.isDisabledDueToConnection()) {
        return true
      }
      
      // Disable if server is manually disabled
      if (this.serverDisabled[server.id]) {
        return true
      }
      
      // Disable if server has errors (but NOT if just unreachable - PDU may still work)
      if (server.status === 'error') {
        return true
      }
      
      // Only disable for "unreachable" if server has NO control methods available
      if (server.status === 'unreachable') {
        const hasAnyControl = this.hasIPMIControl(server) || this.hasPDUControl(server)
        if (!hasAnyControl) {
          return true
        }
        // If it has control methods but shows unreachable, still allow actions (PDU may work)
      }
      
      // Disable if server is in transitional state (but allow "powered" and stable states)
      if (server.status?.includes('...') || server.status?.includes('Powering') || server.status?.includes('Rebooting')) {
        // Exception: "powered" and stable states should NOT disable controls
        if (server.status?.toLowerCase() === 'powered' || 
            server.status?.toLowerCase() === 'on' ||
            server.status?.toLowerCase() === 'off') {
          return false
        }
        return true
      }
      
      // Allow all other statuses including "pending", "on", "off", "unknown", "powered"
      return false
    },
    
    isServerOn(server) {
      // Check if server is powered on (includes "powered" state for devices with power but unreachable system)
      if (!server) return false
      return server.status?.toLowerCase() === 'on' || 
             server.status?.toLowerCase() === 'poweredon' ||
             server.status?.toLowerCase() === 'powered'
    },
    
    // Check if server can be powered on (has control method and is currently off)
    canPowerOn(server) {
      const hasControl = (server.ipmi && server.ipmi.host) || (server.pdu_outlets && server.pdu_outlets.length > 0)
      const isOff = server.status?.toLowerCase() === 'off' || server.status?.toLowerCase() === 'poweredoff'
      const isDisabled = this.isServerDisabled(server)
      
      return hasControl && isOff && !isDisabled
    },
    
    // Check if server can be powered off (has control method and is currently on)  
    canPowerOff(server) {
      const hasControl = (server.ipmi && server.ipmi.host) || (server.pdu_outlets && server.pdu_outlets.length > 0)
      const isOn = this.isServerOn(server)
      const isDisabled = this.isServerDisabled(server)
      
      return hasControl && isOn && !isDisabled
    },
    
    // Check if IPMI controls should be shown (has IPMI config)
    hasIPMIControl(server) {
      return (server.ipmi && server.ipmi.host) || (server.IPMI && server.IPMI.host)
    },
    
    // Check if PDU controls should be shown (has PDU config)
    hasPDUControl(server) {
      return server.pdu_outlets && server.pdu_outlets.length > 0
    },
    
    // Check if a status change is significant enough to clear error indicators
    isSignificantStatusChange(oldStatus, newStatus) {
      if (!oldStatus || !newStatus) return false
      
      const old = oldStatus.toLowerCase()
      const current = newStatus.toLowerCase()
      
      // Ignore minor changes or transitional states
      if (old === current) return false
      if (current.includes('...') || current.includes('powering') || current.includes('rebooting')) {
        return false // Don't clear on transitional states
      }
      
      // Consider these significant changes that indicate successful operation:
      // off -> on, off -> powered, poweredoff -> on, etc.
      const significantChanges = [
        ['off', 'on'],
        ['off', 'powered'], 
        ['poweredoff', 'on'],
        ['poweredoff', 'powered'],
        ['unreachable', 'on'],
        ['unreachable', 'powered'],
        ['unreachable', 'off'], // Became reachable again
        ['on', 'off'], // Successful power off
        ['powered', 'off'] // Successful power off
      ]
      
      return significantChanges.some(([from, to]) => 
        old === from && current === to
      )
    },
    
    // Check if server has persistent job failures OR IPMI communication issues
    hasRecentFailures(server) {
      // Check for persistent job failures (until cleared by success or status change)
      const hasJobFailures = this.serverFailures[server.id] && !this.serverFailures[server.id].cleared
      
      // Check for IPMI communication issues (when device is on but IPMI unreachable)
      const hasIPMIIssues = server.ipmi_reachable === false && 
                            server.ipmi_error && 
                            (server.status === 'on' || server.status === 'powered')
      
      return hasJobFailures || hasIPMIIssues
    },
    
    // Get persistent job failure details for server
    getRecentFailureDetails(server) {
      // Return the tracked persistent failure if not cleared
      if (this.serverFailures[server.id] && !this.serverFailures[server.id].cleared) {
        return this.serverFailures[server.id].lastFailureJob
      }
      return null
    },
    
    // Generate tooltip text for error indicator
    getErrorTooltip(server) {
      // Check for IPMI communication issues first
      const hasIPMIIssues = server.ipmi_reachable === false && 
                            server.ipmi_error && 
                            (server.status === 'on' || server.status === 'powered')
      
      if (hasIPMIIssues) {
        const ipmiError = server.ipmi_error || 'IPMI unreachable'
        const shortError = ipmiError.length > 60 ? ipmiError.substring(0, 60) + '...' : ipmiError
        return `IPMI Access Failed\nDevice is powered but IPMI unreachable\nError: ${shortError}\nClick for details`
      }
      
      // Otherwise check for job failures
      const failedJob = this.getRecentFailureDetails(server)
      if (!failedJob) return "Recent issue - click for details"
      
      const errorMsg = failedJob.error || failedJob.last_error || 'Unknown error'
      const shortError = errorMsg.length > 80 ? errorMsg.substring(0, 80) + '...' : errorMsg
      return `Job failed: ${failedJob.description || 'Unknown job'}\nError: ${shortError}\nClick for full details`
    },
    
    // Show error details - either job modal or IPMI alert
    showErrorDetails(server) {
      // Check for IPMI communication issues first
      const hasIPMIIssues = server.ipmi_reachable === false && 
                            server.ipmi_error && 
                            (server.status === 'on' || server.status === 'powered')
      
      if (hasIPMIIssues) {
        const ipmiError = server.ipmi_error || 'IPMI unreachable'
        this.openErrorModal(
          'IPMI Communication Failed',
          server.name,
          'Device is powered but IPMI access is failing',
          server.ipmi?.host || 'Unknown',
          ipmiError,
          'Device can still be controlled via PDU. Check IPMI credentials and network connectivity.'
        )
        return
      }
      
      // For job failures, open the job modal instead of alert
      const failedJob = this.getRecentFailureDetails(server)
      if (failedJob) {
        this.openJobModal(failedJob)
      }
    },
    
    isMobile() {
      // Check if screen width is mobile size or if it's a touch device
      return window.innerWidth <= 768 || ('ontouchstart' in window)
    },
    
    async refreshAll() {
      this.refreshing = true
      
      // Reset connection state for manual refresh
      if (this.connectionStatus === 'disconnected') {
        this.connectionRetryCount = 0
        this.pollingPaused = false
      }
      
      try {
        await this.refreshAllStatus(true) // Force fresh data
        await this.loadAllJobs()
        this.updateServerJobsFromGlobal()
      } catch (error) {
        console.error('Failed to refresh:', error)
        // Try to reconnect if manual refresh fails
        if (this.connectionStatus === 'connected') {
          this.retryConnection()
        }
      } finally {
        this.refreshing = false
      }
    },
    
    // Manual retry button handler
    async manualRetry() {
      console.log('Manual retry initiated')
      
      // Reset retry state
      this.connectionRetryCount = 0
      this.pollingPaused = false
      
      // Clear any existing retry timeout
      if (this.retryTimeout) {
        clearTimeout(this.retryTimeout)
        this.retryTimeout = null
      }
      
      // Start fresh retry attempt
      await this.retryConnection()
    },
    
    // Connection Management
    handleConnectionSuccess() {
      this.lastSuccessfulCall = Date.now()
      this.connectionRetryCount = 0
      const wasDisconnected = this.pollingPaused
      this.pollingPaused = false
      
      // Clear any pending retry
      if (this.retryTimeout) {
        clearTimeout(this.retryTimeout)
        this.retryTimeout = null
      }
      
      if (this.connectionStatus !== 'connected') {
        this.connectionStatus = 'connected'
        this.showConnectionStatus = true
        console.log('Connection restored')
        
        // If we were disconnected, immediately refresh all data and resume polling
        if (wasDisconnected) {
          console.log('Immediately resuming full data refresh after reconnection')
          
          // Small delay to let the connection stabilize, then do comprehensive refresh
          setTimeout(() => {
            Promise.all([
              this.refreshAllStatus(true), // Force fresh data after reconnection
              this.loadAllJobs()
            ]).then(() => {
              this.updateServerJobsFromGlobal()
              console.log('Full data refresh completed after reconnection')
            }).catch(console.error)
          }, 100)
        }
        
        // Hide "connected" message after 3 seconds
        setTimeout(() => {
          if (this.connectionStatus === 'connected') {
            this.showConnectionStatus = false
          }
        }, 3000)
      }
    },
    
    handleConnectionError(error) {
      console.error('Connection error:', error)
      
      // Pause polling to prevent floods
      this.pollingPaused = true
      
      // Only show disconnected if we haven't had a successful call recently
      const timeSinceLastSuccess = Date.now() - this.lastSuccessfulCall
      if (timeSinceLastSuccess > 3000) { // Faster detection - 3 seconds
        if (this.connectionStatus === 'connected') {
          this.connectionStatus = 'disconnected'
          this.showConnectionStatus = true
          this.clearStaleData() // Clear old data when disconnected
          
          // Automatically start retry process when we first detect disconnection
          if (!this.retryTimeout) {
            console.log('Starting automatic retry process')
            this.retryConnection()
          }
        }
      }
    },
    
    // Clear stale data when disconnected to avoid confusion
    clearStaleData() {
      console.log('Clearing stale data due to connection loss')
      
      // Keep the last known server status - don't mark servers as "disconnected"
      // The connection banner will show the system is disconnected
      // Server actions will be disabled via isDisabledDueToConnection()
      
      // Don't clear jobs as they might be useful to see offline
      // But ensure no new ones are processed
      this.pollingPaused = true
    },
    
    async retryConnection() {
      // Clear any existing retry timeout
      if (this.retryTimeout) {
        clearTimeout(this.retryTimeout)
        this.retryTimeout = null
      }
      
      this.connectionStatus = 'reconnecting'
      this.connectionRetryCount++
      
      try {
        // Try a simple API call to test connection with timeout
        const controller = new AbortController()
        const timeoutId = setTimeout(() => controller.abort(), 5000) // 5 second timeout
        
        await axios.get('/api/v1/servers', { 
          signal: controller.signal 
        })
        
        clearTimeout(timeoutId)
        this.handleConnectionSuccess()
        
        // Don't need to refresh here - handleConnectionSuccess() will do it
        
      } catch (error) {
        if (error.name === 'AbortError') {
          console.log('Request timeout during retry')
        }
        
        this.handleConnectionError(error)
        
        // Auto retry with aggressive initial attempts, then exponential backoff
        // First 3 attempts: 1s, 1s, 2s (aggressive for quick recovery)
        // Then exponential: 3s, 5s, 8s, 12s, 18s, 27s, 30s max
        let delay
        if (this.connectionRetryCount <= 3) {
          // Fast initial retries for quick backend restarts
          delay = this.connectionRetryCount <= 2 ? 1000 : 2000
        } else {
          // Exponential backoff for extended outages
          delay = Math.min(1000 * Math.pow(1.7, this.connectionRetryCount - 3), 30000)
        }
        
        console.log(`Connection retry ${this.connectionRetryCount} scheduled in ${delay}ms`)
        
        this.retryTimeout = setTimeout(() => {
          this.retryConnection()
        }, delay)
        
        // After reaching max backoff, show disconnected but keep retrying
        if (delay >= 30000) {
          this.connectionStatus = 'disconnected'
        }
      }
    },
    
    // Enhanced API wrapper that handles connection errors and prevents floods
    async apiCall(apiFunction, requestKey = null) {
      // Prevent duplicate requests
      if (requestKey && this.pendingRequests.has(requestKey)) {
        throw new Error('Request already pending')
      }
      
      if (requestKey) {
        this.pendingRequests.add(requestKey)
      }
      
      try {
        const result = await apiFunction()
        this.handleConnectionSuccess()
        return result
      } catch (error) {
        this.handleConnectionError(error)
        throw error
      } finally {
        if (requestKey) {
          this.pendingRequests.delete(requestKey)
        }
      }
    },
    
    // JSON request through the connection-aware wrapper; resolves to the response body
    async request(method, url, data) {
      const response = await this.apiCall(() => axios({ method, url, data }))
      return response.data
    },

    // Best human-readable message from an API error
    errorMessage(error) {
      return error?.response?.data?.error || error?.message || 'Unknown error'
    },

    // Check if actions should be disabled due to connection
    isDisabledDueToConnection() {
      return this.connectionStatus === 'disconnected' || this.connectionStatus === 'reconnecting'
    },
    
    // Server Menu Methods
    toggleServerMenu(serverId) {
      // On mobile, show bottom sheet instead of dropdown
      if (this.isMobile()) {
        const server = this.servers.find(s => s.id === serverId)
        this.mobileBottomSheet = {
          show: true,
          serverId: serverId,
          serverName: server ? server.name : serverId,
          server: server
        }
        return
      }
      
      // Desktop: Close all other menus first
      const newOpenMenus = {}
      if (!this.openMenus[serverId]) {
        newOpenMenus[serverId] = true
      }
      this.openMenus = newOpenMenus
    },
    
    closeMobileBottomSheet() {
      this.mobileBottomSheet.show = false
    },
    
    handleMobileAction(action) {
      if (!this.mobileBottomSheet.server || !this.isServerOn(this.mobileBottomSheet.server)) {
        return
      }
      this.performAction(this.mobileBottomSheet.serverId, action)
      this.closeMobileBottomSheet()
    },
    
    handleMobileSchedule(action, hoursFromNow) {
      if (!this.mobileBottomSheet.server || !this.isServerOn(this.mobileBottomSheet.server)) {
        return
      }
      this.scheduleAction(this.mobileBottomSheet.serverId, action, hoursFromNow)
      this.closeMobileBottomSheet()
    },
    
    handleMobilePDUAction(action) {
      if (!this.mobileBottomSheet.server) {
        return
      }
      this.performPDUAction(this.mobileBottomSheet.serverId, action)
      this.closeMobileBottomSheet()
    },
    
    async scheduleAction(serverId, action, hoursFromNow) {
      const server = this.servers.find(s => s.id === serverId)
      const serverName = server ? server.name : serverId
      
      try {
        // Calculate the scheduled time
        const scheduledTime = new Date(Date.now() + hoursFromNow * 60 * 60 * 1000)
        
        // Create a scheduled job via API
        const response = await this.apiCall(async () => {
          return await axios.post('/api/v1/jobs/schedule', {
            server_id: serverId,
            action: action,
            scheduled_time: scheduledTime.toISOString(),
            description: `Scheduled ${action} for ${serverName} in ${hoursFromNow} hour${hoursFromNow > 1 ? 's' : ''}`
          })
        })
        
        // Close the menu
        this.openMenus[serverId] = false
        
        // Refresh jobs to show the new scheduled job
        await this.loadAllJobs()
        this.updateServerJobsFromGlobal()
        
        // Show confirmation
        alert(`✅ Scheduled power ${action} for ${serverName} in ${hoursFromNow} hour${hoursFromNow > 1 ? 's' : ''}\n\nYou can cancel this job from the Job Queue (hamburger menu).`)
        
      } catch (error) {
        console.error('Failed to schedule action:', error)
        if (this.connectionStatus === 'connected') {
          alert(`Failed to schedule ${action} for ${serverName}. Please try again.`)
        }
      }
    },
    
    getServerName(serverId) {
      if (!serverId) return 'N/A'
      const server = this.servers.find(s => s.id === serverId)
      return server ? server.name : serverId
    },
    
    // Job Modal Methods
    openJobModal(job) {
      this.selectedJob = job
    },
    
    closeJobModal() {
      this.selectedJob = null
      this.jobActionLoading = false
    },

    // Error Modal Methods
    openErrorModal(title, serverName, status, ipmiHost, error, note) {
      this.errorModal = {
        show: true,
        title,
        serverName,
        status,
        ipmiHost,
        error,
        note
      }
    },

    closeErrorModal() {
      this.errorModal.show = false
    },

    // Server Details Modal Methods
    openServerDetails(server) {
      this.selectedServer = server
    },

    closeServerModal() {
      this.selectedServer = null
    },

    getServerJobs(serverId) {
      return this.allJobs.filter(job => job.server_id === serverId)
        .sort((a, b) => new Date(b.created_at) - new Date(a.created_at))
    },
    
    canCancelJob(job) {
      return job.status === 'pending' || job.status === 'waiting'
    },
    
    canRunNowJob(job) {
      return job.status === 'pending' || job.status === 'waiting'
    },
    
    canRetryJob(job) {
      return job.status === 'failed'
    },
    
    async cancelJob(jobId) {
      this.jobActionLoading = true
      try {
        await axios.delete(`/api/v1/jobs/${jobId}`)
        this.closeJobModal()
        this.loadAllJobs() // Refresh jobs
      } catch (error) {
        console.error('Failed to cancel job:', error)
        alert('Failed to cancel job. Please try again.')
      } finally {
        this.jobActionLoading = false
      }
    },
    
    async runJobNow(jobId) {
      this.jobActionLoading = true
      try {
        await axios.post(`/api/v1/jobs/${jobId}/run-now`)
        this.closeJobModal()
        this.loadAllJobs() // Refresh jobs
      } catch (error) {
        console.error('Failed to run job now:', error)
        alert('Failed to run job now. Please try again.')
      } finally {
        this.jobActionLoading = false
      }
    },
    
    async retryJob(jobId) {
      this.jobActionLoading = true
      try {
        await this.apiCall(async () => {
          return await axios.post(`/api/v1/jobs/${jobId}/retry`)
        })
        this.closeJobModal()
        this.loadAllJobs() // Refresh jobs
        console.log(`Job ${jobId} retry initiated`)
      } catch (error) {
        console.error('Failed to retry job:', error)
        if (this.connectionStatus === 'connected') {
          alert('Failed to retry job. Please try again.')
        }
      } finally {
        this.jobActionLoading = false
      }
    },
    
    formatDate(dateString) {
      if (!dateString) return 'N/A'
      const date = new Date(dateString)
      return date.toLocaleString()
    },
    
    // Progress indicator methods
    hasActiveJobs(serverId) {
      if (!this.serverJobs[serverId]) return false
      return this.serverJobs[serverId].some(job => 
        job.status === 'pending' || job.status === 'waiting' || job.status === 'running' || 
        job.status === 'failed' || job.status === 'completed'
      )
    },
    
    getActiveJobsText(serverId) {
      if (!this.serverJobs[serverId]) return ''
      
      const activeJobs = this.serverJobs[serverId].filter(job => 
        job.status === 'pending' || job.status === 'waiting' || job.status === 'running'
      )
      
      if (activeJobs.length === 0) return ''
      
      if (activeJobs.length === 1) {
        const job = activeJobs[0]
        if (job.status === 'running') {
          return `Running: ${job.description}`
        } else if (job.status === 'waiting') {
          return `Waiting: ${job.description}`
        } else if (job.status === 'failed') {
          return `Failed: ${job.description}`
        } else if (job.status === 'completed') {
          return `Completed: ${job.description}`
        } else {
          return `Pending: ${job.description}`
        }
      } else {
        const running = activeJobs.filter(j => j.status === 'running').length
        const waiting = activeJobs.filter(j => j.status === 'waiting').length
        const pending = activeJobs.filter(j => j.status === 'pending').length
        const failed = activeJobs.filter(j => j.status === 'failed').length
        const completed = activeJobs.filter(j => j.status === 'completed').length
        
        let text = `${activeJobs.length} jobs: `
        const parts = []
        if (running > 0) parts.push(`${running} running`)
        if (waiting > 0) parts.push(`${waiting} waiting`)
        if (pending > 0) parts.push(`${pending} pending`)
        if (completed > 0) parts.push(`${completed} completed`)
        if (failed > 0) parts.push(`${failed} failed`)
        
        return text + parts.join(', ')
      }
    },
    
    // OS Management Methods
    async loadOSData() {
      try {
        // Load templates and assignments in parallel
        const [templatesResponse, assignmentsResponse] = await Promise.all([
          this.request('get', '/api/v1/os/templates'),
          this.request('get', '/api/v1/os/assignments')
        ])
        
        this.osTemplates = templatesResponse.templates || []
        this.osAssignments = assignmentsResponse.assignments || []
        
      } catch (error) {
        console.error('Failed to load OS data:', error)
      }
    },
    
    getServerOSAssignment(serverId) {
      return this.osAssignments.find(assignment => 
        assignment.assignment.server_id === serverId
      )
    },
    
    async assignOSToServer(serverId, templateId) {
      try {
        if (templateId === '') {
          // Remove assignment (revert to local boot)
          await this.request('delete', `/api/v1/servers/${serverId}/os`)
        } else {
          // Assign new template
          await this.request('put', `/api/v1/servers/${serverId}/os`, {
            template_id: templateId,
            assigned_by: 'Web UI'
          })
        }
        
        // Reload assignments
        await this.loadOSData()
        
      } catch (error) {
        console.error('Failed to assign OS:', error)
        alert('Failed to assign OS: ' + this.errorMessage(error))
      }
    },
    
    async deleteTemplate(templateId) {
      if (!confirm('Are you sure you want to delete this template?')) {
        return
      }
      
      try {
        await this.request('delete', `/api/v1/os/templates/${templateId}`)
        
        // Reload templates
        await this.loadOSData()
        
      } catch (error) {
        console.error('Failed to delete template:', error)
        alert('Failed to delete template: ' + this.errorMessage(error))
      }
    },
    
    newTemplate() {
      this.selectedTemplate = null
      this.templateForm = { name: '', description: '', boot_method: 'chain', next_path: '', boot_template: '' }
      this.templateFormError = ''
      this.showTemplateModal = true
    },

    editTemplate(template) {
      this.selectedTemplate = { ...template }
      this.templateForm = {
        name: template.name,
        description: template.description || '',
        boot_method: template.boot_method,
        next_path: template.next_path || '',
        boot_template: template.boot_template || '',
      }
      this.templateFormError = ''
      this.showTemplateModal = true
    },

    closeTemplateModal() {
      this.showTemplateModal = false
      this.selectedTemplate = null
      this.templateFormError = ''
    },

    async saveTemplate() {
      if (!this.templateForm.name.trim()) {
        this.templateFormError = 'Name is required.'
        return
      }
      if (this.templateForm.boot_method === 'chain' && !this.templateForm.next_path.trim()) {
        this.templateFormError = 'Next Path is required for chain boot method.'
        return
      }
      if (this.templateForm.boot_method === 'template' && !this.templateForm.boot_template.trim()) {
        this.templateFormError = 'Boot Template is required for template boot method.'
        return
      }

      this.templateFormSaving = true
      this.templateFormError = ''

      try {
        const body = {
          name: this.templateForm.name.trim(),
          description: this.templateForm.description.trim(),
          boot_method: this.templateForm.boot_method,
          next_path: this.templateForm.next_path.trim(),
          boot_template: this.templateForm.boot_template.trim(),
        }

        if (this.selectedTemplate && this.selectedTemplate.id) {
          await this.request('put', `/api/v1/os/templates/${this.selectedTemplate.id}`, body)
        } else {
          await this.request('post', '/api/v1/os/templates', body)
        }

        await this.loadOSData()
        this.closeTemplateModal()
      } catch (error) {
        this.templateFormError = 'Failed to save: ' + this.errorMessage(error)
      } finally {
        this.templateFormSaving = false
      }
    },

    viewTemplateDetails(template) {
      this.editTemplate(template)
    }
  }
}
</script>