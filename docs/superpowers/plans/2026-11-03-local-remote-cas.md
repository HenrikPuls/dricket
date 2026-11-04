# Local and Remote CAS Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a local dricket store one ordinary repository with its ticket branch checked out after init, while preserving atomic local writes and the existing remote push protocol.

**Architecture:** Discovery reads protected configuration from the `main` ref. A non-bare filesystem store shares one object database with a private detached linked worktree and publishes through local fast-forward CAS; bare and URL stores retain fetch/push CAS. Whole-ticket commit retains guarded adoption for dirty user edits.

**Tech Stack:** Go, Git CLI, standard library, existing store/service/CLI packages.

**Spec:** `docs/superpowers/specs/2026-11-03-local-remote-cas-design.md`

## Global Constraints

- Git remains the only shared truth; no server, database, dependency or new CLI command.
- Never rebase or force-push. Every retry restarts from the fresh ticket head and rechecks preconditions.
- Never clean or hard-reset a user worktree implicitly; only the existing guarded `Workspace.Adopt` may do so.
- Local dirty-checkout publication conflicts map to JSON kind `conflict`, exit 12.
- Bare repositories, including path and `file://` targets, retain push CAS.
- The committed `main` config is authoritative for discovery; no new public flags, JSON schema or ticket format.
- All repository content and copy are English; source files carry GPL-3.0-or-later and commits use DCO sign-off.

## Review Focus

- A subdirectory must not be mistaken for the store root merely because Git can read `main` from there; Task 1 pins root equality.
- A local ordinary write must not move HEAD without updating a checked-out working tree; Task 4 pins visible files and HEAD together.
- A conflicting dirty path must produce exit 12 without retrying or changing the ref; Task 5 pins error mapping and non-loss.
- Whole-ticket commit intentionally publishes a dirty path and therefore cannot use ordinary merge publication; Task 6 pins CAS followed by guarded adoption.
- A bare filesystem path must not enter linked-worktree mode; Task 3 pins compatibility with existing bare-local workflows.

---

### Task 1: Discover configuration from `main` at the actual repository root

**Files:**
- Modify: `internal/store/discover.go`
- Test: `internal/store/discover_test.go`
- Test: `cmd/dk/discover_cli_test.go`

**Interfaces:**
- Consumes: existing `DiscoverFrom`, `InspectRoot`, `Discovery.Spec`.
- Produces: unchanged public discovery types; `InspectRoot` recognizes a working-tree root whose committed `main:dricket.toml` exists even when the current checkout lacks the file.

- [ ] **Step 1: Write failing discovery tests**

Add tests that initialize a store, check out `tickets`, call `DiscoverFrom` from `tickets/app/0000`, and assert the root and `tickets` branch. Add the same coverage for custom branch `work`. Assert a plain Git repository and a nested directory are not roots, and that a differing uncommitted/ticket-branch `dricket.toml` cannot override committed `main`.

- [ ] **Step 2: Run the tests and verify RED**

Run: `go test -count=1 ./internal/store ./cmd/dk -run 'Discovery|Discover'`
Expected: ticket-branch cases report no store; existing main-checkout cases remain green.

- [ ] **Step 3: Implement root-aware config lookup**

In `InspectRoot`, require `git rev-parse --is-inside-work-tree` and compare the cleaned directory with `git rev-parse --show-toplevel`. Read committed `refs/heads/main:dricket.toml` first through an argument-list Git call. Only if that blob is absent, retain the current-checkout file fallback. Parse the selected bytes for `branch`; retain the existing origin-or-directory remote rule and malformed-config discovery default.

- [ ] **Step 4: Run focused tests and commit**

Run: `go test -count=1 ./internal/store ./cmd/dk -run 'Discovery|Discover'`
Expected: PASS.

```bash
git add internal/store/discover.go internal/store/discover_test.go cmd/dk/discover_cli_test.go
git commit -s -m "fix: discover stores from the ticket branch"
```

### Task 2: Leave local init on the ticket branch

**Files:**
- Modify: `internal/store/scaffold.go`
- Modify: `internal/store/scaffold_test.go`
- Modify: `cmd/dk/init.go`
- Test: `cmd/dk/init_cli_test.go`

**Interfaces:**
- Consumes: existing `store.Init` and `Options.Branch`.
- Produces: unchanged `Init` signature; local directories finish on `Options.Branch`, remote URL initialization has no checkout behavior.

- [ ] **Step 1: Write failing init tests**

Assert `git symbolic-ref --short HEAD` equals `tickets` after local init and `work` after `init --branch work`; assert `dricket.toml` is absent from the ticket working tree but present in `main`. Keep remote scaffold and refusal tests unchanged.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test -count=1 ./internal/store ./cmd/dk -run 'Init'`
Expected: current branch is `main`.

- [ ] **Step 3: Keep the seeded ticket branch checked out**

Replace the final symbolic-ref back to `main` with a hard reset of the just-seeded ticket branch during this new, empty repository's initialization. Update function comments and human init output; do not change the JSON result.

- [ ] **Step 4: Run focused tests and commit**

Run: `go test -count=1 ./internal/store ./cmd/dk -run 'Init'`
Expected: PASS; tests that attempt immediate writes may still await Task 3 and are not changed to hide that dependency.

```bash
git add internal/store/scaffold.go internal/store/scaffold_test.go cmd/dk/init.go cmd/dk/init_cli_test.go
git commit -s -m "feat: check out the ticket branch on init"
```

### Task 3: Add a non-bare local store with shared Git objects

**Files:**
- Create: `internal/store/local.go`
- Modify: `internal/store/store.go`
- Test: `internal/store/local_test.go`
- Test: `internal/store/config_test.go`

**Interfaces:**
- Consumes: `Options{Remote, Branch, DataDir, Profile}` and existing `Store` read methods.
- Produces: `Store.localDir string` (non-empty only for a non-bare filesystem store), `Store.headRev string` (`HEAD` locally, `FETCH_HEAD` otherwise), `Store.baseSHA string` (the immutable local head captured for the current attempt), and unchanged `Open`, `Refresh`, `ConfigFile`, `HeadSHA` signatures.

- [ ] **Step 1: Write failing local preparation tests**

Initialize a non-bare store, open it with `Options.Remote` set to its path, and assert its private directory is a detached linked worktree whose common Git directory belongs to that store. Assert local `Refresh` reads the current local ticket ref and `ConfigFile` reads committed `main` without an origin. Advance the public ticket ref after refresh and assert reads still see the captured private HEAD snapshot. Assert a bare path still creates the existing origin-based private clone.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test -count=1 ./internal/store -run 'Local|Bare|Config'`
Expected: non-bare local mode is not implemented.

- [ ] **Step 3: Implement local detection and linked-worktree preparation**

Detect repository shape with Git, not string suffixes. For a non-bare filesystem target, create or reuse a disposable detached linked worktree under the existing cache key; recreate only that private cache entry when stale. Never remove or prune a user worktree. Use a fixed empty hooks path for internal local Git calls. Keep bare paths and URLs on the existing clone/origin path.

- [ ] **Step 4: Abstract the fresh-head revision and immutable base**

Replace read-side `FETCH_HEAD` uses with `s.headRev`. Local refresh resolves the ticket ref to a commit SHA, stores it as `baseSHA` and resets the private detached worktree to that SHA; all checks and mutations for the attempt read private `HEAD`, never the moving public ref. Remote refresh remains fetch/reset. Local `ConfigFile` reads `refs/heads/main:<name>` directly. Keep absent-branch and absent-file behavior unchanged.

- [ ] **Step 5: Run store tests and commit**

Run: `go test -count=1 ./internal/store`
Expected: PASS, including existing remote and bare-store tests.

```bash
git add internal/store/local.go internal/store/local_test.go internal/store/store.go internal/store/config_test.go
git commit -s -m "feat: add single-repository local store preparation"
```

### Task 4: Publish ordinary local writes with fast-forward CAS

**Files:**
- Modify: `internal/store/local.go`
- Modify: `internal/store/write.go`
- Test: `internal/store/local_test.go`
- Test: `cmd/dk/init_cli_test.go`

**Interfaces:**
- Consumes: `Store.localDir`, `Store.baseSHA`, existing `Store.Write` retry loop; pass the captured base to `publishLocal`.
- Produces: `WriteSpec.PreserveWorktree bool` (true only for whole-ticket commit), and internal `publishLocal(ctx, candidate, base string, preserve bool) (retryable bool, err error)`.

- [ ] **Step 1: Write failing end-to-end local-write tests**

After local `init <dir> app`, run `add -c "first"` without a store flag from the ticket checkout. Assert success, `tickets` HEAD equals the private accepted commit and the new `ticket.md` is visible. Cover an ordinary write while `main` is checked out and a custom ticket branch. Add concurrent local adds and claims with the existing uniqueness/ownership invariants.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test -count=1 ./internal/store ./cmd/dk -run 'Local|Init'`
Expected: local publication is not implemented.

- [ ] **Step 3: Implement the local publisher**

If the ticket ref is not checked out, or `PreserveWorktree` is true, publish with `git update-ref refs/heads/<branch> <candidate> <base>` and treat expected-old mismatch as retryable. Otherwise locate the ticket branch in `git worktree list --porcelain` and run `merge --ff-only --quiet <candidate>` there with hooks disabled. Never create a merge commit. Classify only a concurrent non-fast-forward as retryable; local dirty-path refusal is handled in Task 5.

- [ ] **Step 4: Integrate publication and reconciliation**

Call local publication instead of push only when `localDir` is non-empty. Preserve `WriteSpec` mutation/precondition behavior and accepted-commit identity. Reconcile inconclusive local publication by testing whether the candidate is an ancestor of the local ticket ref; never blindly replay. Leave remote push code unchanged.

- [ ] **Step 5: Run focused tests and commit**

Run: `go test -count=1 ./internal/store ./cmd/dk -run 'Local|Init|Concurrent'`
Expected: PASS.

```bash
git add internal/store/local.go internal/store/local_test.go internal/store/write.go cmd/dk/init_cli_test.go
git commit -s -m "feat: publish local writes with fast-forward CAS"
```

### Task 5: Preserve dirty checkouts and map conflicts

**Files:**
- Modify: `internal/store/local.go`
- Modify: `internal/store/write.go`
- Modify: `internal/service/service.go`
- Test: `internal/store/local_test.go`
- Test: `cmd/dk/workspace_cli_test.go`

**Interfaces:**
- Consumes: `publishLocal` and `WriteError`.
- Produces: `Outcome CheckoutConflict`; service maps it to `KindConflict`/exit 12.

- [ ] **Step 1: Write failing dirty-checkout tests**

With a claimed ticket's `ticket.md` edited locally, publish an add-only comment and assert the comment appears while the edit remains byte-identical. Attempt an ordinary write that changes the same dirty ticket and assert exit 12, kind `conflict`, unchanged ticket ref and preserved edit. Cover staged unrelated changes and assert no retry delay for the dirty conflict.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test -count=1 ./internal/store ./cmd/dk -run 'Dirty|Conflict|Local'`
Expected: dirty conflicts are not yet classified or safely handled.

- [ ] **Step 3: Implement typed conflict handling**

Recognize Git's local-changes-would-be-overwritten refusal separately from non-fast-forward races. Return `CheckoutConflict` immediately with a concise path-preserving error. Add the service exit/kind mapping. Do not broaden retryable remote-push classification.

- [ ] **Step 4: Run tests and commit**

Run: `go test -count=1 ./internal/store ./internal/service ./cmd/dk -run 'Dirty|Conflict|Local|ExitCode'`
Expected: PASS.

```bash
git add internal/store/local.go internal/store/local_test.go internal/store/write.go internal/service/service.go cmd/dk/workspace_cli_test.go
git commit -s -m "fix: preserve dirty checkouts during local writes"
```

### Task 6: Support local whole-ticket commit, adoption and sync

**Files:**
- Modify: `internal/service/commit.go`
- Modify: `internal/store/workspace.go`
- Modify: `internal/store/store.go`
- Modify: `cmd/dk/workspace.go`
- Test: `internal/service/commit_test.go`
- Test: `internal/store/workspace_test.go`
- Test: `cmd/dk/workspace_cli_test.go`

**Interfaces:**
- Consumes: `WriteSpec.PreserveWorktree`, local CAS and existing `Workspace.Adopt`.
- Produces: unchanged public service result; local workspace adoption checks the local ticket ref when no origin exists.

- [ ] **Step 1: Write failing local editing tests**

Run the complete local flow: init, add/claim, sync, edit, comment during edit, commit and release. Assert exact content adoption and revision. Retain existing cases for an edit changed during commit, unrelated dirty files, later accepted commits and unpushed/divergent local history. Assert local `sync` requires no origin and never resets dirty files.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test -count=1 ./internal/service ./internal/store ./cmd/dk -run 'Commit|Workspace|Sync|Local'`
Expected: local commit attempts ordinary checked-out publication or adoption requires origin.

- [ ] **Step 3: Wire the special commit publication**

Set `PreserveWorktree: true` in `service.Commit`. In `Workspace.Adopt`, when the workspace has no origin, skip fetch and verify the accepted candidate is an ancestor of the local current ticket branch; retain all content, dirty-path and ancestry guards before reset. In local `Workspace.Sync`, retain clean/branch checks and avoid a nonexistent-origin fetch. Make the CLI warning say publication rather than remote push where appropriate.

- [ ] **Step 4: Run editing tests and commit**

Run: `go test -count=1 ./internal/service ./internal/store ./cmd/dk -run 'Commit|Workspace|Sync|Local'`
Expected: PASS for local and remote flows.

```bash
git add internal/service/commit.go internal/service/commit_test.go internal/store/workspace.go internal/store/workspace_test.go internal/store/store.go cmd/dk/workspace.go cmd/dk/workspace_cli_test.go
git commit -s -m "feat: support local whole-ticket editing"
```

### Task 7: Record the architecture and run the release-quality gate

**Files:**
- Create: `docs/adr/0010-local-and-remote-cas.md`
- Modify: `docs/CONCEPT.md`, `docs/protocol.md`, `docs/architecture.md`, `docs/SPIKE.md`
- Modify: `README.md`, `site/index.html` where init/discovery examples are affected
- Modify: `CHANGELOG.md`, `specs/changelog.md`
- Modify: `docs/superpowers/specs/2026-11-03-local-remote-cas-design.md` if implementation clarifies behavior

**Interfaces:**
- Consumes: Tasks 1-6.
- Produces: documented R-14 behavior and acceptance evidence; no further product behavior.

- [ ] **Step 1: Update the durable contract**

Record the chosen local/remote CAS split, store-selection precedence, init checkout, conflict mapping and dirty-checkout publication rules. Update old main-checked-out and push-only descriptions. Add R-14 under Awaiting verification with the tests actually run; add user-facing Unreleased notes.

- [ ] **Step 2: Run the complete local gate**

Run: `go build ./... && go vet ./... && golangci-lint run && go test -count=1 ./... && python scripts/check_docs.py`
Expected: all commands exit 0. CI must additionally pass race tests on Linux and macOS.

- [ ] **Step 3: Exercise the original user scenario**

Build `dk`, create a fresh local store, check its current branch is `tickets`, add two tickets without bindings, edit/claim/sync/commit/release from nested ticket directories and run `dk use`. Expected: store and prefix resolve correctly throughout.

- [ ] **Step 4: Commit documentation and verification records**

```bash
git add docs README.md site/index.html CHANGELOG.md specs/changelog.md
git commit -s -m "docs: describe single-repository local operation"
```
