# NVMe Device Detection Spike — Findings

**Spike**: #134
**Date**: 2026-02-23
**Scope**: `scripts/bootstrap.sh` — project EBS device detection on Nitro (NVMe) instances

---

## 1. Background

On Xen-based EC2 instances (older generation), EBS volumes attached as `/dev/xvdf` appear
at that exact path in the OS. On Nitro-based instances (m5, m6i, c6i, r6i, and most modern
families), the kernel NVMe driver enumerates EBS volumes as `/dev/nvme[N]n1` block devices.
The `/dev/xvdf` path does not exist.

`mint up` hardcodes `/dev/xvdf` as the project EBS device name when interpolating the
`MINT_PROJECT_DEV` variable into user-data (`internal/provision/up.go` line 596). This works
on Xen; it does not work on Nitro without an NVMe fallback.

The bootstrap script already contains a polling fallback loop (added as Fix #94). This spike
investigates whether that fallback is correct and whether there is a safety bug in it.

---

## 2. Root Cause Analysis

### 2.1 What the fallback loop does (lines 200–212)

```bash
_t=90
while [ "${_t}" -gt 0 ] && [ ! -b "${MINT_PROJECT_DEV}" ]; do
    _root_disk=$(lsblk -rno PKNAME "$(findmnt -no SOURCE /)" 2>/dev/null || true)
    _candidate=$(lsblk -rno NAME,TYPE 2>/dev/null \
        | awk -v r="${_root_disk}" '$2=="disk" && $1!=r {print "/dev/"$1; exit}')
    if [ -n "${_candidate:-}" ]; then
        MINT_PROJECT_DEV="${_candidate}"
        break
    fi
    sleep 5
    _t=$(( _t - 5 ))
done
```

**Intended behaviour**: Loop until either `/dev/xvdf` appears (Xen path, condition becomes
false) or until `lsblk` enumerates a non-root disk (NVMe path, `_candidate` is set and
we `break`). The 90-second window covers the NVMe kernel enumeration delay.

**Logic trace on m6i.xlarge (Nitro, Ubuntu 24.04, project EBS attached via BDM)**:

| Step | Value |
|------|-------|
| `MINT_PROJECT_DEV` (initial) | `/dev/xvdf` (hardcoded by Go) |
| `[ ! -b /dev/xvdf ]` | TRUE — file does not exist on Nitro |
| `findmnt -no SOURCE /` | `/dev/nvme0n1p1` (Ubuntu 24.04 uses GPT partitions) |
| `lsblk -rno PKNAME /dev/nvme0n1p1` | `nvme0n1` |
| `_root_disk` | `nvme0n1` |
| `lsblk -rno NAME,TYPE` output (typical) | `nvme0n1 disk`, `nvme0n1p1 part`, `nvme1n1 disk` |
| awk result | `/dev/nvme1n1` (first non-root disk) |
| `MINT_PROJECT_DEV` (after break) | `/dev/nvme1n1` |

On a standard m6i.xlarge with one root volume and one project EBS, the loop correctly
identifies `/dev/nvme1n1`. The NVMe device selection logic is not the problem.

### 2.2 The safety bug: PKNAME returning an empty string

`lsblk -rno PKNAME` returns the **parent kernel name** (PKNAME) of a device — i.e., the disk
that contains a given partition. For a partition (`/dev/nvme0n1p1`), it returns the parent
disk (`nvme0n1`). For a bare disk (`/dev/nvme0n1`), it returns **empty string**, because a
disk has no parent kernel name.

`findmnt -no SOURCE /` can return either form depending on how the root filesystem was
configured:

- Canonical Ubuntu 24.04 EC2 AMIs use GPT → root is on a partition →
  `findmnt` returns `/dev/nvme0n1p1` → `PKNAME` returns `nvme0n1` (correct)
- A custom AMI or ephemeral root filesystem may put root directly on the bare NVMe disk →
  `findmnt` returns `/dev/nvme0n1` → `PKNAME` returns **empty string**

When `_root_disk` is empty, the awk filter degenerates:

```awk
'$2=="disk" && $1!=r'   →   '$2=="disk" && $1!=""'   →   ALL disks match
```

With the awk `exit` on the first match, the **first disk in lsblk output** is selected.
`lsblk` outputs devices in kernel enumeration order. On Nitro, this order is not guaranteed,
but in practice `nvme0n1` (the root disk) often appears before `nvme1n1` (the project EBS).

**If `nvme0n1` (the root disk) is selected as `_candidate`, then:**

```bash
MINT_PROJECT_DEV="/dev/nvme0n1"   # root disk!
break
```

The script then calls `blkid /dev/nvme0n1` (returns data → no mkfs), mounts the root disk a
second time over `/mint/projects`, and writes a stale fstab entry. The instance is
irrecoverably broken. Bootstrap may appear to "succeed" (no error from mount) but the
instance is misconfigured.

This is a **silent data-corruption bug**. It does not cause a bootstrap failure tag; it causes
a misconfigured instance that a developer will connect to and discover is broken.

### 2.3 Classification

| Category | Verdict |
|----------|---------|
| Timing (udev race) | NOT the primary cause — the 90-second polling loop handles this |
| Selection bug (wrong NVMe device) | YES — when `PKNAME` returns empty, root disk may be selected |
| Missing detection | NOT the cause — detection exists but has the safety gap above |

**Root cause**: The awk condition does not guard against `_root_disk=""`, allowing root disk
selection as a candidate device when `lsblk -rno PKNAME` cannot determine the root disk name.

---

## 3. Proposed Fix

Add a guard to the awk condition that skips candidate selection entirely when `_root_disk` is
empty. An empty `_root_disk` means we cannot safely identify the root disk, so we should not
proceed with the heuristic at all — we let the loop retry after sleeping 5 seconds, which
gives the kernel additional time to enumerate the NVMe device (making `findmnt` more reliable
on subsequent iterations).

### Exact change

**File**: `scripts/bootstrap.sh`, line 205

Before (86 bytes including newline):
```bash
            | awk -v r="${_root_disk}" '$2=="disk" && $1!=r {print "/dev/"$1; exit}')
```

After (97 bytes including newline):
```bash
            | awk -v r="${_root_disk}" 'r!="" && $2=="disk" && $1!=r {print "/dev/"$1; exit}')
```

**Change**: Insert `r!="" && ` (9 characters) before the existing `$2=="disk"` condition.

### Byte accounting

| Measurement | Bytes |
|-------------|-------|
| Before fix | 16,331 |
| After fix | 16,340 |
| Delta | +9 |
| Hard limit | 16,384 |
| Remaining headroom | 44 |

The fix fits within the 53-byte headroom. 44 bytes remain after applying it.

### Why this fix is sufficient

When `_root_disk` is empty, `_candidate` remains empty, the `if [ -n "${_candidate:-}" ]`
block is skipped, the loop sleeps 5 seconds and retries. On retry, `findmnt` and `PKNAME`
are called again. If the root filesystem is stable (it always is in user-data context), the
next invocation returns the same empty string — but the important side effect is that the
loop does not select the root disk as the project volume. The loop will exhaust its 90-second
budget and exit, leaving `MINT_PROJECT_DEV="/dev/xvdf"`, which causes `blkid` to fail and
the script to exit with an error, tagging `mint:bootstrap=failed`. This is the correct
observable outcome: the failure is surfaced rather than silently corrupted.

Note: On the standard Ubuntu 24.04 Canonical EC2 AMI (GPT-partitioned root), `PKNAME`
returns `nvme0n1` reliably. The fix prevents a corner case on custom AMIs with bare-disk
roots, not the common path. The common path is unaffected.

### What the fix does NOT address

- It does not fix the bare-disk root corner case positively (i.e., it does not attempt to
  derive the root disk name via an alternative method). This is intentional: introducing more
  device detection heuristics would consume more bytes and increase complexity. The safer
  behaviour — fail loudly rather than corrupt silently — is achieved by the guard alone.
- It does not address instances with instance store (NVMe ephemeral storage). On families
  like `m6id` or `c6id`, instance store NVMe devices appear as additional disks. The awk
  `exit` picks the alphabetically-first non-root disk, which could be an instance store
  device if it enumerates before the EBS project volume. This is a separate issue (#135).
- It does not change the Go-side hardcoding of `/dev/xvdf`. That is the correct AWS device
  name for EBS attachment; the fallback loop is the right place to handle the Nitro renaming.

---

## 4. Test Plan

### 4.1 Unit-level verification (no AWS required)

Run the existing bootstrap verification tests against the modified script:

```bash
go test ./internal/bootstrap/... -v -count=1
```

Expected: All tests pass. The verify_test.go already checks for the NVMe polling loop
patterns (`lsblk -rno NAME,TYPE`, `findmnt -no SOURCE /`, `_t=90`, `sleep 5`). The new
`r!="" &&` text does not break any of these assertions.

Verify the byte count:

```bash
wc -c scripts/bootstrap.sh   # must be < 16384
```

Then regenerate the hash:

```bash
go generate ./internal/bootstrap/...
```

Verify `internal/bootstrap/hash_generated.go` changed (confirms the embedded hash matches
the modified script).

### 4.2 Live test on m6i.xlarge (requires AWS credentials)

1. Launch an m6i.xlarge instance using `mint up` (or `mint up --instance-type m6i.xlarge`
   if the config is not already set to m6i.xlarge).
2. Poll `mint list` until the instance shows `bootstrap: complete`.
3. SSH into the instance (`mint ssh`) and verify:

```bash
# /mint/projects should be mounted on the project EBS, NOT the root disk
mount | grep /mint/projects
# Expected: /dev/nvme1n1 on /mint/projects type ext4 ...

# Confirm root disk is NOT /dev/nvme1n1
lsblk
# Expected: nvme0n1 (root, 200GB), nvme1n1 (project, 50GB)

# Confirm UUID in fstab matches project volume
blkid /dev/nvme1n1
grep /mint/projects /etc/fstab
```

4. Stop and start the instance (`aws ec2 stop-instances` + `start-instances`) and verify the
   boot reconciliation service remounts `/mint/projects` correctly (check `journalctl -u mint-reconcile`).

### 4.3 Regression test: Xen instance type

Repeat step 4.2 on a `t3.medium` or other Xen-based instance type (if available in the
account). Verify `/dev/xvdf` is used directly (the loop condition `[ ! -b /dev/xvdf ]`
becomes false immediately, skipping the NVMe fallback entirely).

### 4.4 Edge case: bare-disk root (custom AMI)

This test requires a custom AMI with the root filesystem on a bare NVMe disk (no partition).
Build such an AMI (beyond scope of this spike), launch an instance, and verify that:

- With the fix: bootstrap fails with `mint:bootstrap=failed` tag; no data corruption
- Without the fix: root disk is mounted at `/mint/projects`; instance silently corrupted

---

## 5. Decision

The fix (9 bytes, `r!="" &&` guard) fits within the 53-byte headroom with 44 bytes remaining.
It is safe to apply to `scripts/bootstrap.sh`.

After applying the fix:
1. Run `wc -c scripts/bootstrap.sh` — confirm output is `16340`
2. Run `go generate ./internal/bootstrap/...` — regenerate the embedded hash
3. Run `go test ./internal/bootstrap/... -v -count=1` — confirm tests pass
4. Run `go test ./... -count=1` — full suite green before committing
