#!/bin/bash
# Mint bootstrap script — runs as EC2 user-data on first boot.
# Target: Amazon Linux 2023 (AL2023)
#
# Environment variables (passed via user-data template):
#   MINT_EFS_ID       — EFS filesystem ID for user storage
#   MINT_PROJECT_DEV  — Block device for project EBS volume (e.g., /dev/xvdf)
#   MINT_VM_NAME      — VM name tag value
#   MINT_IDLE_TIMEOUT — Idle timeout in minutes (default: 60)
#
# This script is hash-pinned in the Go binary. Any modification requires
# regenerating the embedded hash via `go generate ./internal/bootstrap/...`.

set -euo pipefail

BOOTSTRAP_VERSION="1.0.0"
MINT_STATE_DIR="/var/lib/mint"
MINT_IDLE_TIMEOUT="${MINT_IDLE_TIMEOUT:-60}"

# --- Logging ---

log() {
    echo "[mint-bootstrap] $(date -u '+%Y-%m-%dT%H:%M:%SZ') $*"
}

log "Starting bootstrap v${BOOTSTRAP_VERSION}"

# --- System updates ---

log "Updating system packages"
dnf update -y -q

# --- Git ---

log "Installing git"
dnf install -y -q git

# --- Docker Engine ---

log "Installing Docker Engine"
dnf install -y -q docker
systemctl enable docker
systemctl start docker
usermod -aG docker ec2-user

# Docker Compose plugin
log "Installing Docker Compose plugin"
mkdir -p /usr/local/lib/docker/cli-plugins
COMPOSE_VERSION=$(curl -fsSL https://api.github.com/repos/docker/compose/releases/latest | grep '"tag_name"' | head -1 | cut -d'"' -f4)
COMPOSE_DOWNLOAD="/usr/local/lib/docker/cli-plugins/docker-compose"
curl -fsSL "https://github.com/docker/compose/releases/download/${COMPOSE_VERSION}/docker-compose-linux-$(uname -m)" \
    -o "$COMPOSE_DOWNLOAD"

# TODO: Update this checksum when pinning a specific Docker Compose version
DOCKER_COMPOSE_SHA256="PLACEHOLDER_UPDATE_BEFORE_RELEASE"
echo "${DOCKER_COMPOSE_SHA256}  ${COMPOSE_DOWNLOAD}" | sha256sum --check || {
    log "FATAL: checksum mismatch for ${COMPOSE_DOWNLOAD}"
    exit 1
}

chmod +x "$COMPOSE_DOWNLOAD"

# --- Node.js LTS ---

log "Installing Node.js LTS"
dnf install -y -q nodejs npm

# --- devcontainer CLI ---

log "Installing devcontainer CLI"
npm install -g @devcontainers/cli

# --- tmux ---

log "Installing tmux"
dnf install -y -q tmux

# Configure tmux defaults for all users
cat > /etc/tmux.conf << 'TMUX_CONF'
# Mint tmux defaults
set -g mouse on
set -g history-limit 50000
set -g default-terminal "tmux-256color"
set -ga terminal-overrides ",xterm-256color:Tc"
TMUX_CONF

# --- mosh ---

log "Installing mosh-server"
dnf install -y -q mosh

# --- GitHub CLI ---

log "Installing GitHub CLI (gh)"
dnf install -y -q 'dnf-command(config-manager)'
dnf config-manager --add-repo https://cli.github.com/packages/rpm/gh-cli.repo
dnf install -y -q gh

# --- AWS CLI v2 ---

log "Installing AWS CLI v2"
if ! command -v aws &> /dev/null; then
    curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m).zip" -o /tmp/awscliv2.zip

    # TODO: Update this checksum when pinning a specific AWS CLI version
    AWSCLI_SHA256="PLACEHOLDER_UPDATE_BEFORE_RELEASE"
    echo "${AWSCLI_SHA256}  /tmp/awscliv2.zip" | sha256sum --check || {
        log "FATAL: checksum mismatch for /tmp/awscliv2.zip"
        exit 1
    }

    cd /tmp && unzip -q awscliv2.zip
    /tmp/aws/install
    rm -rf /tmp/aws /tmp/awscliv2.zip
fi

# --- EC2 Instance Connect ---

log "Installing EC2 Instance Connect agent"
dnf install -y -q ec2-instance-connect

# --- SSH configuration (ADR-0016) ---

log "Configuring SSH on port 41122"
sed -i 's/^#\?Port .*/Port 41122/' /etc/ssh/sshd_config
sed -i 's/^#\?PasswordAuthentication .*/PasswordAuthentication no/' /etc/ssh/sshd_config
sed -i 's/^#\?ChallengeResponseAuthentication .*/ChallengeResponseAuthentication no/' /etc/ssh/sshd_config

# Ensure the non-standard port is used
if ! grep -q '^Port 41122' /etc/ssh/sshd_config; then
    echo 'Port 41122' >> /etc/ssh/sshd_config
fi

systemctl restart sshd

# --- Storage mounts (ADR-0004) ---

log "Setting up storage mounts"
mkdir -p /mint/user /mint/projects "${MINT_STATE_DIR}"

# Mount EFS at /mint/user
if [ -n "${MINT_EFS_ID:-}" ]; then
    log "Mounting EFS ${MINT_EFS_ID} at /mint/user"
    dnf install -y -q amazon-efs-utils
    mount -t efs "${MINT_EFS_ID}:/" /mint/user
    # Write fstab entry for EFS
    if ! grep -q '/mint/user' /etc/fstab; then
        echo "${MINT_EFS_ID}:/ /mint/user efs _netdev,tls 0 0" >> /etc/fstab
    fi
    chown ec2-user:ec2-user /mint/user
fi

# Format and mount project EBS at /mint/projects
if [ -n "${MINT_PROJECT_DEV:-}" ]; then
    log "Setting up project volume ${MINT_PROJECT_DEV} at /mint/projects"
    # Only format if no filesystem exists
    if ! blkid "${MINT_PROJECT_DEV}" &> /dev/null; then
        mkfs.ext4 -q "${MINT_PROJECT_DEV}"
    fi
    mount "${MINT_PROJECT_DEV}" /mint/projects
    # Write fstab entry for project EBS
    PROJECT_UUID=$(blkid -s UUID -o value "${MINT_PROJECT_DEV}")
    if ! grep -q '/mint/projects' /etc/fstab; then
        echo "UUID=${PROJECT_UUID} /mint/projects ext4 defaults,nofail 0 2" >> /etc/fstab
    fi
    chown ec2-user:ec2-user /mint/projects
fi

# --- Idle detection (ADR-0018) ---

log "Installing idle detection systemd timer and service"

cat > /etc/systemd/system/mint-idle-check.service << 'IDLE_SERVICE'
[Unit]
Description=Mint idle detection check
After=network.target docker.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/mint-idle-check
IDLE_SERVICE

cat > /etc/systemd/system/mint-idle-check.timer << 'IDLE_TIMER'
[Unit]
Description=Mint idle detection timer (5-minute interval)

[Timer]
OnBootSec=5min
OnUnitActiveSec=5min
AccuracySec=30s

[Install]
WantedBy=timers.target
IDLE_TIMER

cat > /usr/local/bin/mint-idle-check << 'IDLE_SCRIPT'
#!/bin/bash
# Mint idle detection script (ADR-0018)
# Checks SSH/mosh sessions, tmux clients, claude processes in containers,
# and manual extend timestamp.

set -euo pipefail

IDLE_TIMEOUT="${MINT_IDLE_TIMEOUT:-60}"
STATE_DIR="/var/lib/mint"
IDLE_FILE="${STATE_DIR}/idle-since"
EXTEND_FILE="${STATE_DIR}/idle-extended-until"
NOW=$(date +%s)
ACTIVE_CRITERIA=()

# Check SSH sessions
if pgrep -x sshd | while read pid; do
    # Check for child processes (actual sessions, not the listener)
    children=$(pgrep -P "$pid" 2>/dev/null || true)
    [ -n "$children" ] && exit 0
done 2>/dev/null; then
    ACTIVE_CRITERIA+=("ssh_session")
fi

# Check mosh sessions
if pgrep -f "mosh-server" > /dev/null 2>&1; then
    ACTIVE_CRITERIA+=("mosh_session")
fi

# Check tmux attached clients
if command -v tmux &> /dev/null && tmux list-clients 2>/dev/null | grep -q .; then
    ACTIVE_CRITERIA+=("tmux_client")
fi

# Check claude processes in containers
if command -v docker &> /dev/null; then
    for container_id in $(docker ps -q 2>/dev/null); do
        if docker top "$container_id" 2>/dev/null | grep -q "claude"; then
            ACTIVE_CRITERIA+=("claude_process")
            break
        fi
    done
fi

# Check manual extend
if [ -f "$EXTEND_FILE" ]; then
    EXTEND_UNTIL=$(cat "$EXTEND_FILE")
    if [ "$NOW" -lt "$EXTEND_UNTIL" ] 2>/dev/null; then
        ACTIVE_CRITERIA+=("manual_extend")
    fi
fi

# Determine idle state
if [ ${#ACTIVE_CRITERIA[@]} -gt 0 ]; then
    # Active — reset idle timer
    rm -f "$IDLE_FILE"
    IDLE_ELAPSED=0
    ACTION="none"
    STOP_RESULT=null
else
    # Idle — check if we've exceeded timeout
    if [ ! -f "$IDLE_FILE" ]; then
        echo "$NOW" > "$IDLE_FILE"
    fi
    IDLE_SINCE=$(cat "$IDLE_FILE")
    IDLE_ELAPSED=$(( (NOW - IDLE_SINCE) / 60 ))

    if [ "$IDLE_ELAPSED" -ge "$IDLE_TIMEOUT" ]; then
        ACTION="stop"
        # Get instance ID from metadata and stop self
        TOKEN=$(curl -s -X PUT "http://169.254.169.254/latest/api/token" \
            -H "X-aws-ec2-metadata-token-ttl-seconds: 21600")
        INSTANCE_ID=$(curl -s -H "X-aws-ec2-metadata-token: $TOKEN" \
            http://169.254.169.254/latest/meta-data/instance-id)
        REGION=$(curl -s -H "X-aws-ec2-metadata-token: $TOKEN" \
            http://169.254.169.254/latest/meta-data/placement/region)

        if aws ec2 stop-instances --instance-ids "$INSTANCE_ID" --region "$REGION" 2>/dev/null; then
            STOP_RESULT="success"
        else
            STOP_RESULT="failed"
        fi
    else
        ACTION="none"
        STOP_RESULT=null
    fi
fi

# Write structured log to journald (ADR-0018)
CRITERIA_JSON=$(printf '%s\n' "${ACTIVE_CRITERIA[@]:-}" | jq -R . | jq -s .)
logger -t mint-idle --id=$$ -p daemon.info "$(jq -nc \
    --arg ts "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
    --argjson criteria "$CRITERIA_JSON" \
    --argjson idle "$IDLE_ELAPSED" \
    --arg action "${ACTION}" \
    --arg stop "${STOP_RESULT}" \
    '{check_timestamp: $ts, active_criteria_met: $criteria, idle_elapsed_minutes: $idle, action_taken: $action, stop_result: (if $stop == "null" then null else $stop end)}'
)"
IDLE_SCRIPT

chmod +x /usr/local/bin/mint-idle-check

systemctl daemon-reload
systemctl enable mint-idle-check.timer
systemctl start mint-idle-check.timer

# --- Bootstrap version ---

log "Writing bootstrap version"
echo "${BOOTSTRAP_VERSION}" > /var/lib/mint/bootstrap-version

# --- Health check ---

log "Running health check"
HEALTH_OK=true
HEALTH_ERRORS=""

check_command() {
    if ! command -v "$1" &> /dev/null; then
        HEALTH_OK=false
        HEALTH_ERRORS="${HEALTH_ERRORS}  - $1 not found\n"
    fi
}

check_service() {
    if ! systemctl is-active --quiet "$1"; then
        HEALTH_OK=false
        HEALTH_ERRORS="${HEALTH_ERRORS}  - $1 service not active\n"
    fi
}

check_command docker
check_command docker-compose || check_command "docker compose" || true
check_command devcontainer
check_command tmux
check_command mosh-server
check_command git
check_command gh
check_command node
check_command npm
check_command aws
check_service docker
check_service sshd

if [ "$HEALTH_OK" = true ]; then
    log "Health check passed"

    # Tag instance as bootstrap complete
    TOKEN=$(curl -s -X PUT "http://169.254.169.254/latest/api/token" \
        -H "X-aws-ec2-metadata-token-ttl-seconds: 21600")
    INSTANCE_ID=$(curl -s -H "X-aws-ec2-metadata-token: $TOKEN" \
        http://169.254.169.254/latest/meta-data/instance-id)
    REGION=$(curl -s -H "X-aws-ec2-metadata-token: $TOKEN" \
        http://169.254.169.254/latest/meta-data/placement/region)

    aws ec2 create-tags \
        --resources "$INSTANCE_ID" \
        --tags "Key=mint:bootstrap,Value=complete" \
        --region "$REGION"

    log "Tagged instance ${INSTANCE_ID} with mint:bootstrap=complete"
else
    log "Health check FAILED:"
    echo -e "$HEALTH_ERRORS" | while read -r line; do
        log "$line"
    done
fi

log "Bootstrap v${BOOTSTRAP_VERSION} finished"
