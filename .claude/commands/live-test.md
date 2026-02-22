# /live-test — Project-Scoped Live E2E Test Command

Run a full end-to-end test of the Mint CLI against a real AWS tenant. Discovers the
current CLI surface by reading the codebase, builds a fresh test binary, executes
tests against an isolated test VM, files GitHub issues on failures, and tears down.

**Role of this command**: surface and record bugs — not fix them. When a test fails,
file an issue and move on. Do not attempt to work around, patch, or retry around
failures. If a failure blocks further testing (e.g. `mint up` fails so VM-lifecycle
tests cannot run), stop that tier, report the blocker to the user, and ask how to
proceed before continuing.

---

## Phase 1: Discovery (read the codebase — no hardcoded list)

Read the following files to build the test plan dynamically:

1. **`cmd/root.go`** — enumerate every `rootCmd.AddCommand(...)` call to get the full
   list of registered subcommands.
2. **Each `cmd/<command>.go`** — for every subcommand discovered, read the file and
   extract:
   - Flags it accepts (from `cmd.Flags()` and `cmd.PersistentFlags()` calls)
   - Whether it is excluded from `commandNeedsAWS` (check `cmd/awsdeps.go`)
   - Whether it SSH-es into the VM (reads `deps.sendKey` / calls `runSSH` /
     `defaultRemoteRunner`)
   - What it creates or deletes (provisioning, attach, destroy, etc.)
3. **`docs/SPEC.md`** — expected behavior per command.

From this reading, derive test tiers:

- **No-AWS**: commands excluded by `commandNeedsAWS` (`version`, `config`, `config get`,
  `config set`, `ssh-config`, `completion`, `help`)
- **Read-only AWS**: commands that call AWS but never create/delete resources
  (`list`, `status`, `doctor`)
- **Idempotent init**: creates shared infra idempotently (`init`)
- **VM lifecycle**: provisions or mutates a specific VM (`up`, `down`, `resize`,
  `recreate`, `destroy`, `extend`, `sessions`, `key add`)
- **SSH-based with args**: automatable non-interactively (`ssh -- <cmd>`,
  `project list`, `project add`, `project rebuild`)
- **Self-update**: operates on the test binary (`update`)
- **Non-automatable**: requires interactive TTY or external tool unavailable here
  (`mosh`, `connect`, `code`)

---

## Phase 2: Build

```bash
go build -o ./mint-live-test .
```

Fail fast if the build fails — nothing else works without it.

---

## Phase 3: Plan (TodoWrite)

After discovery, call `TodoWrite` to load the full test checklist. Generate todos
**from the command set discovered in Phase 1** — not from any hardcoded list here.

Format for each todo:
```
[tier] mint <command> [flags] — <what is being verified>
```

Include one final todo (blocked until user approves):
```
[teardown] mint destroy --vm $TEST_VM --yes — verify test VM is gone
```

The test VM name is generated once and used throughout:
```bash
TEST_VM="e2e-$(date +%Y%m%d-%H%M%S)"
```

---

## Phase 4: Execute

Work through todos in tier order (Tier 1 → Tier 6). For each test:

1. Mark todo `in_progress`
2. Run `./mint-live-test <command> [flags]`, capturing stdout, stderr, and exit code
3. Validate per command type:
   - **Exit code**: 0 for success, non-zero for expected errors
   - **--json output**: must be valid JSON (`jq . <<<output`)
   - **Expected content**: check for key strings per spec (e.g., `mint list` must
     include the test VM name after `mint up`)
   - **State transitions**: e.g., after `mint down`, `mint status` must show
     `stopped`
4. On failure: **immediately** file a GitHub issue (don't wait for the end), add
   the issue URL to a running failure list
5. Mark todo `completed` (pass or fail-with-issue) and continue — **never abort
   early** due to a single failure
6. **Blocking failures**: if a failure makes the remaining tests in a tier
   impossible to run (e.g. `mint up` fails so the VM never exists, or `mint init`
   fails so no shared infra exists), stop that tier immediately, report the blocker
   to the user with the filed issue URL, and ask: "This failure blocks the remaining
   Tier N tests. Skip them and continue to Tier N+1, or stop here?" Do **not**
   attempt to fix or work around the failure.

### Safety rules (enforced throughout execution)

- `$TEST_VM` is set once and never changes: `e2e-<timestamp>`
- All write operations (`mint up`, `mint down`, `mint resize`, `mint recreate`,
  `mint destroy`, `mint project add`, `mint extend`, `mint key add`) **must**
  pass `--vm $TEST_VM`
- Before any destructive command (`mint destroy`, `mint recreate`, `mint down`),
  assert `[[ $TEST_VM == e2e-* ]]` — abort if not
- `mint init`-created shared resources (security group, EFS access point) are
  **never destroyed** — they are prerequisites for CLI operation and are
  idempotent to re-create
- `mint update` targets `./mint-live-test` (the test binary built above), not
  the user's installed `mint`

---

## Automatable Commands

### Tier 1 — No AWS

| Invocation | Verify |
|---|---|
| `./mint-live-test version` | exit 0, prints version string |
| `./mint-live-test --version` | exit 0, prints `mint version <ver>` |
| `./mint-live-test config` | exit 0, prints current config |
| `./mint-live-test config get region` | exit 0, prints region or `(not set)` |
| `./mint-live-test config get region --json` | exit 0, valid JSON, no `(not set)` sentinel |
| `./mint-live-test config set idle-timeout 120` (save/restore original) | exit 0 |
| `./mint-live-test config get idle-timeout` | exit 0, prints `120` |
| Restore original idle-timeout | exit 0 |
| `./mint-live-test completion bash` | exit 0, output contains `bash` completion boilerplate |

### Tier 2 — Read-only AWS

| Invocation | Verify |
|---|---|
| `./mint-live-test list` | exit 0, table output |
| `./mint-live-test list --json` | exit 0, valid JSON array |
| `./mint-live-test doctor` | exit 0, prints check results |
| `./mint-live-test doctor --json` | exit 0, valid JSON |
| `./mint-live-test status --vm $TEST_VM` (no VM yet) | non-zero exit or "not found" message |
| `./mint-live-test status --vm $TEST_VM --json` (no VM yet) | valid JSON with error field |

### Tier 3 — Init

| Invocation | Verify |
|---|---|
| `./mint-live-test init` | exit 0, idempotent — shared infra exists or was created |

### Tier 4 — VM Lifecycle (all use `--vm $TEST_VM`)

| Invocation | Verify |
|---|---|
| `./mint-live-test up --vm $TEST_VM` | exit 0, VM created and running |
| `./mint-live-test status --vm $TEST_VM` | exit 0, state is `running` |
| `./mint-live-test list` | exit 0, test VM appears in output |
| `./mint-live-test list --json` | exit 0, valid JSON, test VM in array |
| `./mint-live-test ssh-config --vm $TEST_VM --yes` | exit 0, SSH config written |
| `./mint-live-test ssh --vm $TEST_VM -- echo "hello mint"` | exit 0, stdout contains `hello mint` |
| `./mint-live-test extend 30 --vm $TEST_VM` | exit 0 |
| `./mint-live-test sessions --vm $TEST_VM` | exit 0, lists tmux sessions |
| Generate test key, `./mint-live-test key add --vm $TEST_VM` | exit 0 |
| `./mint-live-test down --vm $TEST_VM` | exit 0, VM stopped |
| `./mint-live-test status --vm $TEST_VM` | exit 0, state is `stopped` |
| `./mint-live-test up --vm $TEST_VM` (restart from stopped) | exit 0, VM running again |
| `./mint-live-test project list --vm $TEST_VM` | exit 0, lists `/mint/projects/` |
| `./mint-live-test project add https://github.com/nicholasgasior/gsfmt --vm $TEST_VM` (small public repo, **slow — devcontainer build**) | exit 0, project cloned |
| `./mint-live-test project list --vm $TEST_VM` | exit 0, project `gsfmt` appears |
| `./mint-live-test project rebuild gsfmt --vm $TEST_VM --yes` | exit 0, rebuilt |
| `./mint-live-test resize m6i.large --vm $TEST_VM` (or next tier from current default) | exit 0, instance type changed |
| `./mint-live-test recreate --vm $TEST_VM --yes` | exit 0, VM recreated and running |

### Tier 5 — Self-update

| Invocation | Verify |
|---|---|
| `./mint-live-test update` | exit 0 or "already up to date"; binary path is `./mint-live-test` (not the user's installed `mint`) |

### Tier 6 — Teardown (requires explicit user confirmation — see Phase 7)

| Invocation | Verify |
|---|---|
| `./mint-live-test destroy --vm $TEST_VM --yes` | exit 0 |
| `./mint-live-test list` | exit 0, test VM no longer appears |

---

## Non-Automatable (explicitly skipped — not silently absent)

| Command | Reason |
|---|---|
| `mint mosh` | Opens interactive mosh session (UDP + TTY required) |
| `mint connect` | `mosh` + `tmux attach` — interactive, requires TTY |
| `mint code` | Launches VS Code process — not available in devcontainer |

Mark these as `SKIP (interactive)` in the summary table.

---

## Bug Report Template

When a test fails, immediately file a GitHub issue:

```bash
gh issue create \
  --repo SpiceLabsHQ/Mint \
  --title "[e2e] mint <command>: <one-line description>" \
  --label bug \
  --body "$(cat <<'EOF'
## Bug

**Command**: \`mint <full invocation>\`
**Expected**: <per spec / docs>
**Actual**: exit code N
\`\`\`
stdout: <captured stdout>
stderr: <captured stderr>
\`\`\`

## Environment
- Region: <from ./mint-live-test config get region>
- Instance type: <from ./mint-live-test config get instance-type>
- Test VM: <$TEST_VM>
- Binary: \`$(./mint-live-test version)\`
- Date: <ISO timestamp>
EOF
)"
```

If the `--label bug` flag fails (label not found), retry without it. Add the filed
issue URL to the running failure list.

---

## Phase 5: Brief

Print a summary table:

```
COMMAND                                   RESULT   ISSUE
mint version                              PASS     —
mint --version                            PASS     —
mint config get region --json             PASS     —
mint list --json                          FAIL     https://github.com/SpiceLabsHQ/Mint/issues/42
mint project add ...                      SKIP     slow (devcontainer build)
mint mosh                                 SKIP     interactive
mint connect                              SKIP     interactive
mint code                                 SKIP     interactive
...
```

Then list all filed issue URLs together at the bottom.

---

## Phase 6: Debug

Ask: "Do you want to investigate any failures before teardown?"

Work through any debugging interactively — re-running commands, inspecting AWS
state, SSHing into the VM — until the user is satisfied or wants to proceed.

---

## Phase 7: Teardown

Ask for **explicit confirmation** before destroying:

> "Ready to tear down `$TEST_VM`. This will run `mint destroy --vm $TEST_VM --yes`
> and permanently delete the VM and its project EBS volume. Confirm? (yes/no)"

Only proceed on `yes`. Then:

```bash
./mint-live-test destroy --vm $TEST_VM --yes
./mint-live-test list   # verify test VM is gone
```

If `$TEST_VM` does not start with `e2e-`, abort and print an error — never destroy
a non-test VM.
