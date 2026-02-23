#!/bin/bash
# Mint bootstrap script — EC2 user-data, Ubuntu 24.04 LTS.
# Hash-pinned in the Go binary; run `go generate ./internal/bootstrap/...` after changes.

set -euo pipefail

BOOTSTRAP_VERSION="1.0.0"
MINT_STATE_DIR="/var/lib/mint"
MINT_IDLE_TIMEOUT="${MINT_IDLE_TIMEOUT:-60}"

# Track whether bootstrap completed successfully (used by EXIT trap).
_bootstrap_ok=false

export DEBIAN_FRONTEND=noninteractive

# --- Logging ---

log() {
    echo "[mint-bootstrap] $(date -u '+%Y-%m-%dT%H:%M:%SZ') $*"
}

# Fetch instance identity once — reused by EXIT trap and EFS mount.
_IMDS_TOKEN=$(curl -s -X PUT "http://169.254.169.254/latest/api/token" \
    -H "X-aws-ec2-metadata-token-ttl-seconds: 21600" 2>/dev/null) || true
_TRAP_INSTANCE_ID=$(curl -s -H "X-aws-ec2-metadata-token: ${_IMDS_TOKEN}" \
    http://169.254.169.254/latest/meta-data/instance-id 2>/dev/null) || true
_TRAP_REGION=$(curl -s -H "X-aws-ec2-metadata-token: ${_IMDS_TOKEN}" \
    http://169.254.169.254/latest/meta-data/placement/region 2>/dev/null) || true

# EXIT trap: tag instance mint:bootstrap=complete or failed.
_bootstrap_exit() {
    local _tag_value
    if [ "$_bootstrap_ok" = true ]; then
        _tag_value="complete"
    else
        _tag_value="failed"
        log "Bootstrap did NOT complete successfully — tagging mint:bootstrap=failed"
    fi
    if [ -n "${_TRAP_INSTANCE_ID:-}" ] && [ -n "${_TRAP_REGION:-}" ]; then
        aws ec2 create-tags \
            --resources "${_TRAP_INSTANCE_ID}" \
            --tags "Key=mint:bootstrap,Value=${_tag_value}" \
            --region "${_TRAP_REGION}" 2>/dev/null \
            && log "Tagged instance ${_TRAP_INSTANCE_ID} with mint:bootstrap=${_tag_value}" \
            || log "WARNING: Failed to set mint:bootstrap=${_tag_value} tag"
    fi
}
trap '_bootstrap_exit' EXIT

log "Starting bootstrap v${BOOTSTRAP_VERSION}"

# --- System updates ---

log "Updating system packages"
apt-get update -qq
apt-get upgrade -y -qq

# --- Git ---

log "Installing git"
apt-get install -y -qq git

# --- Docker Engine (official apt repository) ---

log "Installing Docker Engine"
apt-get install -y -qq ca-certificates curl gnupg

install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc

echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "${VERSION_CODENAME}") stable" \
  > /etc/apt/sources.list.d/docker.list

apt-get update -qq
apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
systemctl enable docker
systemctl start docker
usermod -aG docker ubuntu

# --- Node.js LTS ---

log "Installing Node.js LTS"
NODESOURCE_KEYRING="/etc/apt/keyrings/nodesource.gpg"
curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key \
    | gpg --dearmor -o "${NODESOURCE_KEYRING}"
chmod a+r "${NODESOURCE_KEYRING}"
NODE_MAJOR=22
echo "deb [arch=$(dpkg --print-architecture) signed-by=${NODESOURCE_KEYRING}] https://deb.nodesource.com/node_${NODE_MAJOR}.x nodistro main" \
    > /etc/apt/sources.list.d/nodesource.list
apt-get update -qq
apt-get install -y -qq nodejs

# --- devcontainer CLI ---

log "Installing devcontainer CLI"
npm install -g @devcontainers/cli

# --- tmux ---

log "Installing tmux"
apt-get install -y -qq tmux

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
apt-get install -y -qq mosh

# --- GitHub CLI ---

log "Installing GitHub CLI (gh)"
curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
    -o /etc/apt/keyrings/githubcli-archive-keyring.gpg
chmod a+r /etc/apt/keyrings/githubcli-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
    > /etc/apt/sources.list.d/github-cli.list
apt-get update -qq
apt-get install -y -qq gh

# --- AWS CLI v2 ---

log "Installing AWS CLI v2"
if ! command -v aws &> /dev/null; then
    apt-get install -y -qq unzip
    AWS_CLI_VERSION="2.22.0"
    AWS_CLI_ARCH="$(uname -m)"
    case "${AWS_CLI_ARCH}" in
        x86_64)  AWS_CLI_SHA256="f315aa564190a12ae05a05bd8ab7b0645dd4a1ad71ce9e47dae4ff3dfeee8ceb" ;;
        aarch64) AWS_CLI_SHA256="c932ac00901ea3c430f3829140b8dc00fa6e9b8b99d6891929a4795947de7f3e" ;;
        *) log "ERROR: Unsupported architecture for AWS CLI: ${AWS_CLI_ARCH}"; exit 1 ;;
    esac
    curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${AWS_CLI_ARCH}-${AWS_CLI_VERSION}.zip" \
        -o /tmp/awscliv2.zip
    echo "${AWS_CLI_SHA256}  /tmp/awscliv2.zip" | sha256sum -c - \
        || { log "ERROR: AWS CLI checksum mismatch — aborting installation"; exit 1; }
    cd /tmp && unzip -q awscliv2.zip
    /tmp/aws/install
    rm -rf /tmp/aws /tmp/awscliv2.zip
fi

log "Installing EC2 Instance Connect agent"
apt-get install -y -qq ec2-instance-connect

# --- SSH configuration (ADR-0016) ---

log "Configuring SSH on port 41122"
cat > /etc/ssh/sshd_config.d/mint.conf << 'SSH_CONF'
# Mint SSH configuration (ADR-0016)
Port 41122
PasswordAuthentication no
ChallengeResponseAuthentication no
SSH_CONF

systemctl restart ssh

# --- Storage mounts (ADR-0004) ---

log "Setting up storage mounts"
mkdir -p /mint/user /mint/projects "${MINT_STATE_DIR}"

# Mount EFS at /mint/user
if [ -n "${MINT_EFS_ID:-}" ]; then
    log "Mounting EFS ${MINT_EFS_ID} at /mint/user"

    # Mount EFS via native NFSv4 (VPC security groups provide access control).
    apt-get install -y -qq nfs-common
    EFS_ENDPOINT="${MINT_EFS_ID}.efs.${_TRAP_REGION}.amazonaws.com"
    mount -t nfs4 -o nfsvers=4.1,rsize=1048576,wsize=1048576,hard,timeo=600,retrans=2,noresvport "${EFS_ENDPOINT}:/" /mint/user
    # Write fstab entry for EFS
    if ! grep -q '/mint/user' /etc/fstab; then
        echo "${EFS_ENDPOINT}:/ /mint/user nfs4 nfsvers=4.1,rsize=1048576,wsize=1048576,hard,timeo=600,retrans=2,noresvport,_netdev 0 0" >> /etc/fstab
    fi
    chown ubuntu:ubuntu /mint/user

    # --- EFS symlinks (persistent home directories) ---
    log "Creating EFS-backed home directory symlinks"
    mkdir -p /mint/user/.ssh /mint/user/.config /mint/user/projects
    chown -R ubuntu:ubuntu /mint/user/.ssh /mint/user/.config /mint/user/projects
    chmod 700 /mint/user/.ssh

    ln -sfn /mint/user/.ssh /home/ubuntu/.ssh
    ln -sfn /mint/user/.config /home/ubuntu/.config
    ln -sfn /mint/user/projects /home/ubuntu/projects
    chown -h ubuntu:ubuntu /home/ubuntu/.ssh /home/ubuntu/.config /home/ubuntu/projects
fi

# Format and mount project EBS at /mint/projects
if [ -n "${MINT_PROJECT_DEV:-}" ]; then
    _dev="${MINT_PROJECT_DEV}"
    # Poll up to 90s for block device (handles NVMe naming on Nitro instances).
    _t=90
    while [ "${_t}" -gt 0 ] && [ ! -b "${_dev}" ]; do
        _root_disk=$(lsblk -rno NAME,MOUNTPOINT 2>/dev/null|awk '$2=="/"{n=$1;sub("p[0-9]+$","",n);print n;exit}')
        _candidate=$(lsblk -rno NAME,TYPE 2>/dev/null \
            | awk -v r="${_root_disk}" 'r!="" && $2=="disk" && $1!=r {print "/dev/"$1; exit}')
        if [ -n "${_candidate:-}" ]; then
            _dev="${_candidate}"
            break
        fi
        sleep 5
        _t=$(( _t - 5 ))
    done
    log "Setting up project volume ${_dev} at /mint/projects"
    if ! blkid "${_dev}" &> /dev/null; then
        mkfs.ext4 -q "${_dev}"
    fi
    mount "${_dev}" /mint/projects
    PROJECT_UUID=$(blkid -s UUID -o value "${_dev}")
    if ! grep -q '/mint/projects' /etc/fstab; then
        echo "UUID=${PROJECT_UUID} /mint/projects ext4 defaults,nofail 0 2" >> /etc/fstab
    fi
    chown ubuntu:ubuntu /mint/projects
fi

# --- Boot reconciliation service ---

log "Installing boot reconciliation systemd service"

cat > /etc/systemd/system/mint-reconcile.service << 'RECONCILE_SERVICE'
[Unit]
Description=Mint boot reconciliation — remounts storage and restores symlinks
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=no
ExecStart=/usr/local/bin/mint-reconcile

[Install]
WantedBy=multi-user.target
RECONCILE_SERVICE

cat > /usr/local/bin/mint-reconcile << 'RECONCILE_SCRIPT'
#!/bin/bash
# Mint boot reconciliation — remounts storage and restores symlinks.
set -euo pipefail
log() { logger -t mint-reconcile "$*"; }

log "Starting boot reconciliation"

if grep -q '/mint/user' /etc/fstab && ! mountpoint -q /mint/user 2>/dev/null; then
    log "EFS not mounted at /mint/user — mounting from fstab"
    mount /mint/user || log "WARNING: Failed to mount /mint/user"
fi

if grep -q '/mint/projects' /etc/fstab && ! mountpoint -q /mint/projects 2>/dev/null; then
    log "Project volume not mounted at /mint/projects — mounting from fstab"
    mount /mint/projects || log "WARNING: Failed to mount /mint/projects"
fi

if mountpoint -q /mint/user 2>/dev/null; then
    mkdir -p /mint/user/.ssh /mint/user/.config /mint/user/projects
    chown -R ubuntu:ubuntu /mint/user/.ssh /mint/user/.config /mint/user/projects
    chmod 700 /mint/user/.ssh
    ln -sfn /mint/user/.ssh /home/ubuntu/.ssh
    ln -sfn /mint/user/.config /home/ubuntu/.config
    ln -sfn /mint/user/projects /home/ubuntu/projects
    chown -h ubuntu:ubuntu /home/ubuntu/.ssh /home/ubuntu/.config /home/ubuntu/projects
    log "EFS symlinks restored"
fi

DRIFT_ISSUES=()
! command -v docker &> /dev/null && DRIFT_ISSUES+=("docker_missing") \
    || ! systemctl is-active --quiet docker && DRIFT_ISSUES+=("docker_not_running") || true
! command -v node &> /dev/null && DRIFT_ISSUES+=("nodejs_missing") || true
! grep -q "^Port 41122" /etc/ssh/sshd_config.d/mint.conf 2>/dev/null && DRIFT_ISSUES+=("ssh_port_drift") || true
! command -v mosh-server &> /dev/null && DRIFT_ISSUES+=("mosh_missing") || true
! command -v tmux &> /dev/null && DRIFT_ISSUES+=("tmux_missing") || true

if [ ${#DRIFT_ISSUES[@]} -eq 0 ]; then HEALTH_STATUS="healthy"; else HEALTH_STATUS="degraded"; fi

TOKEN=$(curl -s -X PUT "http://169.254.169.254/latest/api/token" \
    -H "X-aws-ec2-metadata-token-ttl-seconds: 21600" 2>/dev/null) || true
if [ -n "$TOKEN" ]; then
    INSTANCE_ID=$(curl -s -H "X-aws-ec2-metadata-token: $TOKEN" \
        http://169.254.169.254/latest/meta-data/instance-id 2>/dev/null) || true
    REGION=$(curl -s -H "X-aws-ec2-metadata-token: $TOKEN" \
        http://169.254.169.254/latest/meta-data/placement/region 2>/dev/null) || true
    if [ -n "$INSTANCE_ID" ] && [ -n "$REGION" ]; then
        aws ec2 create-tags \
            --resources "$INSTANCE_ID" \
            --tags "Key=mint:health,Value=${HEALTH_STATUS}" \
            --region "$REGION" 2>/dev/null || log "Failed to update health tag"
    fi
fi

DRIFT_JSON=$(printf '%s\n' "${DRIFT_ISSUES[@]:-}" | jq -R . 2>/dev/null | jq -s . 2>/dev/null || echo "[]")
log "reconciliation complete: health=${HEALTH_STATUS} drift_issues=${DRIFT_JSON}"
log "Boot reconciliation complete"
RECONCILE_SCRIPT

chmod +x /usr/local/bin/mint-reconcile

systemctl daemon-reload
systemctl enable mint-reconcile.service

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
check_command devcontainer
check_command tmux
check_command mosh-server
check_command git
check_command gh
check_command node
check_command npm
check_command aws
check_service docker
check_service ssh

if [ "$HEALTH_OK" = true ]; then
    log "Health check passed"
else
    log "Health check FAILED:"
    echo -e "$HEALTH_ERRORS" | while read -r line; do
        log "$line"
    done
    exit 1
fi

# Signal the EXIT trap that bootstrap completed successfully.
_bootstrap_ok=true
log "Bootstrap v${BOOTSTRAP_VERSION} finished"
