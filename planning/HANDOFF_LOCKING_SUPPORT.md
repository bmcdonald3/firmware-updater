# HANDOFF_LOCKING_SUPPORT

## 1) Scope and Context

This phase delivers a lock/reservation safety gate for firmware update jobs. It is a
follow-on to group target selection (`GROUP_SELECTION.md` /
`HANDOFF_GROUP_SELECTION.md`) and depends on the target resolution delivered there.

The goal is to prevent firmware dispatch to any node that SMD reports as
administratively locked or reserved, for both single-target and group jobs. This
phase reads lock state only; it does not acquire or release locks/reservations.

The gate slots into the existing reconcile flow after target resolution and before
per-node Redfish dispatch, preserving existing reconciliation patterns.

## 2) Planning Decisions Already Agreed

1. Lock checking is a distinct follow-on phase, layered on group target resolution.
2. Lock state is consumed read-only from SMD; no reservation is acquired or released
   in this phase.
3. Lock source is the SMD locks API (`/hsm/v2/locks/status`).
4. Both `Locked` and `Reserved` are treated as "do not update" by default.
5. The gate applies to both single-`TargetAddress` and group (`GroupRef`) jobs.
6. A break-glass override (`IgnoreLocks`) is provided for admin use.
7. Success criteria for a job are unchanged: all targeted nodes must succeed.
8. Testing target for this phase is minimal unit tests only.

## 3) Proposed API Contract Changes

Extend FirmwareUpdateJob to express lock policy and report lock conflicts while
preserving backward compatibility. No new target selector fields are introduced.

### 3.1 Spec additions

1. ignoreLocks (bool, optional, default false)
- If false, any in-scope target that is locked or reserved fails the job.
- If true, lock/reservation state is recorded but does not block dispatch.
- Intended for break-glass/admin use only.

### 3.2 Status additions

1. lockConflicts ([]string, optional)
- In-scope member xnames found locked or reserved at the pre-dispatch check,
  with the conflicting flag and lock timing detail for operator triage.

### 3.3 Validation rules

1. ignoreLocks is optional and defaults to false.
2. No interaction with selector exclusivity; lock policy is orthogonal to target
   selection.
3. Existing explicit-target and group workflows remain valid.

## 4) Reconciler Execution Plan

Implement in pkg/reconcilers/firmwareupdatejob_reconciler.go with SMD lock helper
logic alongside the existing group-resolution helpers.

### 4.1 State model

Use existing lifecycle states; the lock gate is evaluated within the resolution/
pre-dispatch window and does not add new states.

1. Pending
2. Resolving
3. InProgress
4. Completed
5. Failed

### 4.2 Lock gate sequence

1. Resolve the target set (single TargetAddress, or group members per
   `GROUP_SELECTION.md`).
2. POST /hsm/v2/locks/status with body `{ "ComponentIDs": ["<member_xname>", ...] }`
   (member xnames, not BMC FQDNs).
3. Read Components[]; treat a member as not-safe-to-update when Locked == true OR
   Reserved == true.
4. If any in-scope member is locked or reserved and ignoreLocks is false, set the
   job to Failed with lock conflict detail and record all conflicts in
   lockConflicts.
5. Surface NotFound[] xnames in status for triage.
6. If ignoreLocks is true, record conflicts in lockConflicts and proceed to
   dispatch.

### 4.3 Failure policy

1. Terminal errors
- any in-scope member locked or reserved while ignoreLocks is false

2. Transient errors
- SMD lock-status query timeout/5xx

3. Job result rule
- Lock conflicts are terminal for the current reconcile pass unless policy dictates
  retry (open item).

## 5) SMD Integration Requirements

1. Query lock/reservation state via POST /hsm/v2/locks/status using resolved member
   xnames.
2. Distinguish Locked (administrative lock) from Reserved (ownership reservation).
3. Capture conflicting xnames, flags, and CreationTime/ExpirationTime in
   status.lockConflicts.
4. Surface NotFound[] xnames and SMD query errors with enough detail for operator
   triage.

## 6) Acceptance Criteria

1. API and generated artifacts compile after schema updates.
2. A job whose target set includes a locked or reserved member is set to Failed with
   lock conflict detail when ignoreLocks is false.
3. The same job proceeds, recording lockConflicts, when ignoreLocks is true.
4. Lock gate applies to both single-target and group jobs.
5. NotFound[] xnames are surfaced in status.
6. Existing explicit-target and group jobs continue to function unchanged.
7. Minimal unit tests added for:
- lock conflict detection (locked, reserved, both, neither)
- ignoreLocks true/false behavior
- SMD lock-status query error handling

## 7) Implementation Work Breakdown

1. API/model updates
- apis/hardware.fabrica.dev/v1/firmwareupdatejob_types.go (add ignoreLocks,
  lockConflicts)
- regenerate with fabrica generate

2. Reconciler and service logic
- pkg/reconcilers/firmwareupdatejob_reconciler.go (lock gate after resolution)
- helper module for SMD lock-status query and conflict evaluation

3. Server wiring/config
- cmd/server/main.go for any SMD locks integration config (shares SMD base URL/auth
  with group selection)

4. Tests
- unit tests under pkg/reconcilers and the lock helper package

## 8) Open Items Requiring Additional Information

1. Lock vs. reservation policy
- Confirm both Locked and Reserved block dispatch, or whether reserved-but-unlocked
  members are treated differently.

2. Retry/wait-for-unlock
- Decide whether a lock conflict is strictly terminal or retries with backoff until
  a deadline.

3. Group-level vs. node-level locks
- Confirm whether group-level lock state should be honored in addition to per-member
  node locks.

4. Reservation acquisition
- Decide whether a future follow-on should hold a reservation for the duration of
  the update.

## 9) Output Artifact Requirements (After Implementation)

After implementation, generate a handoff report in this planning directory
containing:

1. Brief summary of implemented lock gate behavior and where it sits in the reconcile
   flow.
2. Exact verified create commands: one job blocked by a lock conflict and one
   proceeding with ignoreLocks=true.
3. Operational notes for troubleshooting lock conflicts (reading lockConflicts,
   querying SMD lock status directly).
4. SMD integration details: exact lock endpoints called and contract assumptions
   verified.
