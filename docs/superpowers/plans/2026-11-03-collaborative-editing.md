# Collaborative Editing and Ticket Integrity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make dricket a collaborative, whole-file editing tool with safe claim/sync/commit publishing, structural ticket validation, and non-conflicting asynchronous comments.

**Architecture:** Git remains the only shared truth and the `tickets` ref remains the CAS boundary. Users edit a real ticket checkout; `dk commit` compares the edited ticket against its exact base, validates the transition, writes through a clean private store, and updates the user checkout only after a successful push. Comments remain separate add-only files and never change the ticket version.

**Tech Stack:** Go, Git CLI, existing flat front matter, existing TOML configuration, Markdown documentation, static HTML landing page.

**Spec:** `docs/CONCEPT.md`, `docs/protocol.md`, and `docs/architecture.md`; Task 1 brings them into agreement with this plan before product code changes.

## Global Constraints

- Git remote and one fast-forward-only ticket ref are the sole shared coordination mechanism; no server, daemon, event store, or database.
- Every write remains fetch, check on fresh head, commit, push, bounded retry with jitter; never rebase.
- Never overwrite uncommitted user work. A dirty user checkout is not disposable state.
- Repository writers are trusted collaborators. Validation proves structural integrity and legal state succession, not actor truthfulness or authorization.
- Keep stable IDs and paths, `epoch`, and `rev`. `rev` is the only public optimistic ticket-version token; do not add blob SHA as a second public token.
- `dk edit` is not added. `set` remains a targeted convenience mutation, not a permission mechanism.
- Comments remain separate add-only timestamp/author files; anyone may comment on a claimed ticket.
- The body log is human-readable information, not a machine-parsed event log. No event sourcing, general schema DSL, plugin system, batching, artifacts, or multi-store transactions.
- `parent` expresses hierarchy only; `depends` alone controls scheduling.
- All repository text and UI remain English; user chat remains German.
- New public commands and changed validation behavior require the ADR and specification updates in Task 1.
- Work on an approved branch, commit with DCO sign-off, and never commit directly to `main`.
- Full gate: `go build ./... && go vet ./... && golangci-lint run && go test -race -count=1 ./... && python scripts/check_docs.py`.

## Review Focus

- Dirty user checkout: every failure path must leave the edited file and unrelated local changes untouched; covered by Tasks 3 and 4.
- Remote comment during a local ticket edit: it must not cause a ticket-version conflict; covered by Tasks 4 and 6.
- Remote change to the same `ticket.md`: it must produce exit 12 rather than overwrite that change; covered by Task 4.
- Accepted push with lost response: reconciliation must detect the commit or report `outcome-unknown`, never blindly duplicate the mutation; covered by Task 7.
- Hand-edited identity, claim, and epoch fields: immutable identity and claim-transition invariants must be rejected independently of role or command origin; covered by Task 2.

---

### Task 1: Freeze the collaborative protocol in the specification

**Files:**
- Create: `docs/adr/0009-collaborative-whole-ticket-editing.md`
- Modify: `docs/CONCEPT.md`, `docs/protocol.md`, `docs/architecture.md`, `specs/roadmap.md`, `specs/decisions.md`
- Test: `scripts/check_docs.py`

**Interfaces:**
- Produces the normative contracts consumed by all later tasks: `dk sync`, `dk restore <path>`, `dk commit <id>`, structural transition validation, and `outcome-unknown` as an error kind using existing exit 15.

- [ ] **Step 1: Record the roadmap and rejected alternatives**

Add `R-12` for this protocol change and `R-13` for post-acceptance public documentation. Record `dk edit`, per-field authorization, semantic log parsing, and a second blob-SHA version token as rejected in `specs/decisions.md`.

- [ ] **Step 2: Specify consistency, trust, and failure boundaries**

Document total order only for accepted commits on the ticket ref, snapshot reads, no cross-branch/repository atomicity, cooperative fencing, trusted collaborators, and the three push outcomes: retryable rejection, definite failure, unknown outcome.

- [ ] **Step 3: Specify the editing and comment workflows**

Normative editing flow:

```console
dk claim app-6842
cd <existing-ticket-checkout>
dk sync
$EDITOR tickets/app/0006/app-6842/ticket.md
dk commit app-6842
```

`sync` is clean-only and fast-forward-only; `restore` discards local changes without fetching; `commit` validates and publishes one claimed ticket. A new comment file does not change `rev` and does not conflict with an otherwise unchanged `ticket.md`.

- [ ] **Step 4: Define the structural transition contract**

Define immutable identity, `rev + 1` for each `ticket.md` successor, monotonic `epoch`, owner/lease consistency, and epoch increments on claim/takeover. Remove role-based transition checks and command-origin checks from the integrity contract. Keep stage values and all existing field types.

- [ ] **Step 5: Verify and commit the specification**

Run: `python scripts/check_docs.py`
Expected: PASS. Commit the docs and ADR with `git commit -s -m "docs: define collaborative editing protocol"`.

### Task 2: Replace field authorization with structural transition validation

**Files:**
- Modify: `internal/ticket/ticket.go`, `internal/validate/validate.go`
- Test: `internal/ticket/ticket_test.go`, `internal/validate/validate_test.go`

**Interfaces:**
- Produces: `func ValidateTransition(previous, current Ticket) []Finding` for identity, revision, epoch, and owner/lease invariants.
- `ClassOf` may remain for `set` command ergonomics, but field classes no longer imply repository authorization.

- [ ] **Step 1: Write failing transition tests**

Add table cases asserting: arbitrary title/body/stage edits pass; changed `id`, `created_at`, `created_by`, or `schema_version` fail; `rev` not increasing by one fails; epoch decrease fails; free-to-owned and owner-to-different-owner require `epoch + 1`; same-owner edits keep epoch; release keeps epoch.

```go
func TestValidateTransitionAllowsWholeTicketContentEdit(t *testing.T) {
    previous := validClaimedTicket()
    current := previous
    current.Title = "new title"
    current.Body = "\n## Description\n\nrewritten\n"
    current.Stage = StageReview
    current.Rev++
    if got := ValidateTransition(previous, current); len(got) != 0 {
        t.Fatalf("findings = %v", got)
    }
}
```

- [ ] **Step 2: Run the focused tests and verify the failure**

Run: `go test ./internal/ticket ./internal/validate -run 'TestValidateTransition|TestHistory' -count=1`
Expected: FAIL because `ValidateTransition` does not exist and role-based history checks still reject valid collaborative edits.

- [ ] **Step 3: Implement the shared transition gate**

Implement `ValidateTransition` in `internal/ticket` and call it from history replay. Remove role-derived stage authorization from history validation. Do not parse the human log to decide validity. Keep creation checks and existing comment deletion/modification findings.

- [ ] **Step 4: Preserve unknown front-matter fields across tool writes**

Add a failing round-trip test proving that unknown fields accepted with warnings are rendered again unchanged. Store their original key/value/order in `Ticket.Extra []frontmatter.Field`, populate it in `FromDoc`, and append nonduplicate extras in `ToDoc`.

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/ticket ./internal/validate ./internal/service -count=1`
Expected: PASS, except service tests that intentionally assert old role policy must be updated to the new collaborative contract. Commit with `git commit -s -m "refactor: validate structural ticket transitions"`.

### Task 3: Add a safe user-checkout workspace with sync and restore

**Files:**
- Create: `internal/store/workspace.go`
- Modify: `internal/store/store.go`
- Test: `internal/store/workspace_test.go`

**Interfaces:**
- Produces:
  - `func OpenWorkspace(ctx context.Context, dir string) (*Workspace, error)`
  - `func (w *Workspace) Sync(ctx context.Context) error`
  - `func (w *Workspace) Restore(ctx context.Context, path string) error`
  - `func (w *Workspace) ReadPair(ctx context.Context, path string) (base, edited string, err error)`
  - `func (w *Workspace) Adopt(ctx context.Context, commit, path, expected string) error`

- [ ] **Step 1: Write failing workspace safety tests**

Cover: sync fast-forwards a clean checkout; sync refuses a dirty checkout; sync refuses non-fast-forward local history; restore changes only the named safe path and never fetches; `ReadPair` returns exact `HEAD` and working-tree content; `Adopt` accepts a checkout whose only dirty path still has the expected edited content, moves it to the named commit, and refuses every other dirty state.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/store -run 'TestWorkspace' -count=1`
Expected: FAIL because `Workspace` does not exist.

- [ ] **Step 3: Implement workspace operations without rebase or dirty reset**

Use Git argument lists and the existing safe-path rules. `Sync` uses fetch plus `merge --ff-only`; `Restore` uses `git restore --worktree -- <path>`; `ReadPair` uses `git show HEAD:<path>` plus file read. `Adopt` verifies that `path` is the only dirty path and still equals `expected`, then resets to the accepted commit; it never discards any other change. No operation runs `git clean` on a user checkout.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/store -count=1`
Expected: PASS, including existing private-store tests. Commit with `git commit -s -m "feat: add safe ticket checkout operations"`.

### Task 4: Publish whole-ticket edits with exact-base CAS

**Files:**
- Create: `internal/service/commit.go`
- Modify: `internal/store/write.go`, `internal/store/store.go`
- Test: `internal/service/commit_test.go`, `internal/store/store_test.go`

**Interfaces:**
- Consumes `Workspace.ReadPair`, `Workspace.Adopt`, and `ticket.ValidateTransition`.
- Produces:
  - `type CommitResult struct { ID string; Rev int; Commit string; Synced bool }` with JSON tags `id`, `rev`, `commit`, `synced`.
  - `func (s *Service) Commit(ctx context.Context, ws *store.Workspace, id string) (CommitResult, error)`
  - `func (s *Store) HeadSHA(ctx context.Context) (string, error)`

- [ ] **Step 1: Write the failing exact-base and preservation tests**

Cover: successful whole-file edit; malformed edit returns exit-14 service error; identity or claim-field tampering fails; stale `ticket.md` returns `KindConflict`; unrelated remote ticket change retries; local edits remain byte-identical after every failure; successful remote push followed by local adopt failure returns `Synced=false` rather than pretending the push failed.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/service -run 'TestCommit' -count=1`
Expected: FAIL because `Service.Commit` does not exist.

- [ ] **Step 3: Implement commit using content comparison rather than patch merging**

Read the exact base from user `HEAD` and edited content from the workspace. On every `Store.Write` attempt, require the fresh remote `ticket.md` to equal the base byte-for-byte and require current owner to be `s.Owner`. Parse the edited document, reject identity and claim-field changes, set `rev` from the fresh ticket plus one, run lint and `ValidateTransition`, then write the resulting content. Comments and unrelated tickets may advance without invalidating this base.

- [ ] **Step 4: Update the user checkout only after accepted push**

Expose the accepted private-store commit through `HeadSHA`. Call `Workspace.Adopt(ctx, commit, path, edited)` only after `Store.Write` succeeds, so the exact published edit is still the only dirty path. On adopt failure return the accepted commit with `Synced=false`; the caller can run `dk sync` after fixing the local checkout.

- [ ] **Step 5: Run tests and commit**

Run: `go test ./internal/service ./internal/store -count=1`
Expected: PASS. Commit with `git commit -s -m "feat: commit edited tickets with exact-base CAS"`.

### Task 5: Expose sync, restore, and commit in the CLI and guide

**Files:**
- Create: `cmd/dk/workspace.go`
- Modify: `cmd/dk/commands.go`, `cmd/dk/main.go`, `internal/store/scaffold.go`
- Test: `cmd/dk/workspace_cli_test.go`, `cmd/dk/main_test.go`, `internal/store/scaffold_test.go`

**Interfaces:**
- Consumes `OpenWorkspace`, `Workspace.Sync`, `Workspace.Restore`, and `Service.Commit`.
- Produces public commands `dk sync`, `dk restore <path>`, and `dk commit <id>` with existing JSON envelope and exit-code mapping.

- [ ] **Step 1: Write failing end-to-end CLI tests**

Run the real claim/sync/edit/commit flow against a local bare remote. Assert clean JSON shapes, exit 2 for wrong arguments, exit 12 for stale ticket content, exit 14 for malformed front matter, and no output-channel regressions. Assert `edit` is absent from help.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./cmd/dk -run 'Test(Sync|Restore|Commit|Help)' -count=1`
Expected: FAIL because the commands are not registered.

- [ ] **Step 3: Implement thin CLI adapters and update the guide**

Resolve the workspace from the current directory, never silently from an unrelated repository. Its origin and current branch must match the store used by the service; a mismatch is a usage error. `commit` prints the new revision and commit; if `Synced=false`, print a warning to stderr and keep exit 0 because the remote write succeeded. Update the versioned agent guide with claim → sync → edit → inbox/commit → release.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./cmd/dk ./internal/store -count=1`
Expected: PASS. Commit with `git commit -s -m "feat: add collaborative editing commands"`.

### Task 6: Prove comments remain independent of claimed-ticket edits

**Files:**
- Modify: `internal/service/commands.go` only if a defect is exposed
- Test: `internal/service/commit_test.go`, `internal/service/leases_test.go`

**Interfaces:**
- Consumes existing `Service.Comment`, inbox cursor behavior, and new `Service.Commit`; no new comment format or command.

- [ ] **Step 1: Add the comment-during-edit regression test**

Claim a ticket, capture an edited local ticket, add a remote comment as another actor, then commit. Assert success, both comment and edit present, `rev` incremented exactly once relative to the base, and no owner/epoch change.

- [ ] **Step 2: Add the inbox delivery regression test**

Assert a comment added after the cursor is reported before release and remains available afterward. A comment is asynchronous input; no interruption or exactly-once behavior is added.

- [ ] **Step 3: Run focused tests and fix only demonstrated defects**

Run: `go test ./internal/service -run 'Test(Commit.*Comment|Inbox)' -count=1`
Expected: PASS. Keep timestamp/author names and add-only history unchanged.

- [ ] **Step 4: Commit the regression coverage**

Commit with `git commit -s -m "test: preserve comments during claimed ticket edits"`.

### Task 7: Reconcile unknown push outcomes without duplicate mutations

**Files:**
- Modify: `internal/store/write.go`, `internal/service/service.go`, `docs/protocol.md`
- Test: `internal/store/store_test.go`, `cmd/dk/main_test.go`

**Interfaces:**
- Adds `PushUnknown` to `store.Outcome` and `Commit string` to `WriteError`.
- Adds service error kind `outcome-unknown`, mapped to existing exit 15.

- [ ] **Step 1: Add a fault-injection test at the accepted-push boundary**

Wrap the Git executor so a real push succeeds but the client receives a synthetic transport error. On the following reconciliation fetch, an ancestor check for the known commit must convert the result to success. A second test makes reconciliation fail and expects `PushUnknown` with the local commit SHA; no second mutation is executed.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/store -run 'TestWriteUnknownOutcome' -count=1`
Expected: FAIL because the current store returns `PushFailed` without reconciliation.

- [ ] **Step 3: Implement commit recording and reconciliation**

After commit, record its SHA. After a non-retryable transport error from push, fetch and test whether that commit is an ancestor of the remote ticket head. Return success if present, definite failure if a successful fetch proves absence, and `PushUnknown` if reconciliation is inconclusive. Never automatically replay `add`, `comment`, `extend`, or whole-file mutation after an unknown result.

- [ ] **Step 4: Verify CLI mapping and commit**

Run: `go test ./internal/store ./internal/service ./cmd/dk -count=1`
Expected: PASS; JSON uses `kind: "outcome-unknown"` and `exit_code: 15`. Commit with `git commit -s -m "fix: reconcile unknown push outcomes"`.

### Task 8: Separate parent hierarchy from scheduling dependencies

**Files:**
- Modify: `internal/service/next.go`, `docs/protocol.md`
- Test: `internal/service/next_test.go`

**Interfaces:**
- Keeps `NextOptions` and `NextResult` unchanged; only eligibility changes.

- [ ] **Step 1: Write the failing hierarchy test**

Create an open, free child with an open parent and no `depends`; assert it is eligible. Create another child with an unfinished entry in `depends`; assert it is blocked.

- [ ] **Step 2: Run the test to verify failure**

Run: `go test ./internal/service -run 'TestNextParent' -count=1`
Expected: FAIL because the open parent currently blocks the child.

- [ ] **Step 3: Remove parent completion from eligibility and update the protocol**

Keep parent existence and cycle validation. Do not add a subtask type or new state field.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/service -count=1`
Expected: PASS. Commit with `git commit -s -m "fix: keep parent hierarchy out of scheduling"`.

### Task 9: Verify the complete protocol and record measurable limits

**Files:**
- Modify: `docs/SPIKE.md`, `docs/architecture.md`, `specs/changelog.md`, `specs/roadmap.md`
- Test: existing concurrency and integration suites; no new benchmark framework

**Interfaces:**
- Produces release evidence for the complete user workflow and records remaining host/performance limits without adding caches or services.

- [ ] **Step 1: Run focused protocol verification**

Run: `go test -race -count=1 ./internal/store ./internal/service ./internal/validate ./cmd/dk`
Expected: PASS, including concurrent claims, exact-base commits, comments during edits, and unknown-outcome reconciliation.

- [ ] **Step 2: Run the full project gate**

Run: `go build ./... && go vet ./... && golangci-lint run && go test -race -count=1 ./... && python scripts/check_docs.py`
Expected: every command exits 0.

- [ ] **Step 3: Record scale and real-host verification**

Extend the existing spike report or runbook with p50/p95/p99, retry/final-failure rates, real-host latency, large-ticket-count read latency, full-history validation cost, and clock-drift/fault scenarios. Do not claim performance acceptance from the local correctness spike alone.

- [ ] **Step 4: Update delivery records and commit**

Move completed roadmap work to `specs/changelog.md` under “Awaiting verification”; leave `R-13` pending owner acceptance. Commit with `git commit -s -m "test: verify collaborative editing protocol"`.

### Task 10: Update GitHub documentation and landing page after acceptance

**Files:**
- Modify: `README.md`, `site/index.html`, `CHANGELOG.md`, `specs/roadmap.md`
- Test: `scripts/check_docs.py`, existing site/link checks, and a manual responsive/accessibility pass

**Interfaces:**
- Consumes only verified Task 9 behavior and real CLI output; no new product behavior.

- [ ] **Step 1: Update GitHub documentation**

Replace tool-controlled editing language with trusted collaboration and whole-file editing. Show claim → sync → editor → commit, explain `restore`, and show `comment`/`inbox` as the asynchronous channel during a claim. Keep security claims limited to structural integrity and cooperative fencing.

- [ ] **Step 2: Update the landing page**

Change workflow, examples, and guardrail copy to match the verified CLI. Preserve the existing static-page architecture and visual system; do not add scripts, dependencies, or claims unsupported by the released binary.

- [ ] **Step 3: Verify and commit public documentation**

Run: `python scripts/check_docs.py` and inspect the landing page at desktop and mobile widths with keyboard navigation and reduced motion.
Expected: checks pass and all examples match actual output. Commit with `git commit -s -m "docs: present collaborative editing workflow"` only after owner acceptance of the implementation.
