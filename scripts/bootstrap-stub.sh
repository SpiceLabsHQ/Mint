#!/bin/bash
# Mint bootstrap stub — EC2 user-data, Ubuntu 24.04 LTS.
# This tiny stub is sent as EC2 user-data. It fetches the real bootstrap.sh
# from GitHub, verifies its SHA256, then execs it. All __PLACEHOLDER__ tokens
# are substituted by Go before the stub is sent to EC2.

set -euo pipefail

export MINT_EFS_ID="__MINT_EFS_ID__"
export MINT_PROJECT_DEV="__MINT_PROJECT_DEV__"
export MINT_VM_NAME="__MINT_VM_NAME__"
export MINT_IDLE_TIMEOUT="__MINT_IDLE_TIMEOUT__"
export MINT_USER_BOOTSTRAP="__MINT_USER_BOOTSTRAP__"

_STUB_URL="__MINT_BOOTSTRAP_URL__"
_STUB_SHA256="__MINT_BOOTSTRAP_SHA256__"

_tmp=$(mktemp)
trap 'rm -f "$_tmp"' EXIT

curl -fsSL --retry 3 --retry-delay 2 -o "$_tmp" "$_STUB_URL"

_actual=$(sha256sum "$_tmp" | awk '{print $1}')
if [ "$_actual" != "$_STUB_SHA256" ]; then
    echo "[mint-stub] SHA256 mismatch: expected $_STUB_SHA256 got $_actual" >&2
    exit 1
fi

chmod +x "$_tmp"
exec "$_tmp"
