# Dricket Agent Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make correct dricket usage discoverable by AI coding tools through a tested, cross-runtime Agent Skill installed by the existing `dk setup-agent` command.

**Architecture:** One canonical `skills/dricket/SKILL.md` is embedded in the binary and installed at the project-scope `.agents/skills/dricket/SKILL.md`. The skill is deliberately compact and points to the installed binary's `dk guide`; setup-agent preflights all outputs and never overwrites a differing user-owned skill.

**Tech Stack:** Markdown Agent Skills, Go `embed`, existing CLI setup command and integration tests, fresh-agent baseline/green scenarios.

**Spec:** `docs/superpowers/specs/2026-11-03-cli-and-agent-skill-design.md`

## Global Constraints

- Use the R-15 CLI spellings; do not reintroduce removed flags or `update`.
- The skill description starts with `Use when...`, is third person and contains concrete dricket/`dk` triggers.
- No network access, tool-specific installer, new command, new flag or dependency.
- Never overwrite a differing skill or truncate an existing user file.
- Run and document baseline agent behavior before writing the skill; test again with the skill.
- All content is English; commits carry DCO sign-off.

## Review Focus

- The skill must be discovered without first running `dk guide`; Task 1 records the no-skill baseline and Task 4 repeats the scenario with only installed skill guidance.
- A differing existing skill must cause refusal before AGENTS.md is modified; Task 3 tests preflight ordering.
- Re-running setup-agent must not rewrite identical files; Task 3 compares content and reported written flags.
- Installation must work offline and from the standalone binary; Task 3 embeds the canonical file rather than reading the source checkout.
- Skill examples must match the R-15 CLI; Task 4 checks actual agent commands and exit handling.

---

### Task 1: Establish the no-skill baseline

**Files:**
- Create: `.superpowers/sdd/2026-11-03-dricket-agent-skill/progress.md`
- Test: disposable local store and code repository; no production files

**Interfaces:**
- Consumes: R-14 local store behavior and R-15 CLI.
- Produces: recorded baseline failures that determine the skill's minimal content.

- [ ] **Step 1: Define two concrete application scenarios**

Scenario A asks a fresh agent to claim and complete a ticket from a code repository linked to a disposable store. Scenario B asks another fresh agent to add a comment to a ticket currently claimed by a different owner and then handle a simulated exit 13. Neither scenario mentions `dk guide` or supplies workflow rules.

- [ ] **Step 2: Run both scenarios without a dricket skill**

Use fresh agent contexts with the ordinary repository instructions only. Record exact commands, whether the agent finds the store/guide, whether it edits another owner's ticket, and how it handles exits 12/13/15. Preserve relevant failures verbatim in the progress ledger.

- [ ] **Step 3: Identify only observed guidance gaps**

Classify each failure as discovery, workflow, ownership, comment or exit-handling. If a suspected failure does not occur, do not add special skill prose for it beyond the compact reference required by the approved design.

- [ ] **Step 4: Commit the baseline record**

```bash
git add .superpowers/sdd/2026-11-03-dricket-agent-skill/progress.md
git commit -s -m "test: record dricket skill baseline"
```

### Task 2: Write the minimal canonical Agent Skill

**Files:**
- Create: `skills/dricket/SKILL.md`
- Create: `skills/skills.go`
- Test: `skills/skills_test.go`

**Interfaces:**
- Consumes: Task 1 observed failures and the approved compact-reference structure.
- Produces: `skills.Dricket string` containing the embedded canonical SKILL.md.

- [ ] **Step 1: Write failing asset/frontmatter tests**

Assert the embedded asset contains `name: dricket`, a description beginning `Use when`, the `next`/`claim`/`sync`/`commit`/`release` workflow, unique `DK_OWNER`, comments on others' claimed tickets, exits 12/13/15 and `dk guide`. Assert no removed R-15 spelling appears.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test -count=1 ./skills`
Expected: package/asset does not exist.

- [ ] **Step 3: Create and embed the skill**

Write only the compact guidance justified by the design and baseline. Add `skills/skills.go` with GPL header and `//go:embed dricket/SKILL.md` exposing `Dricket string`. Keep heavy detail in `dk guide`; add no supporting files or placeholders.

- [ ] **Step 4: Run tests and commit**

Run: `go test -count=1 ./skills`
Expected: PASS.

```bash
git add skills/dricket/SKILL.md skills/skills.go skills/skills_test.go
git commit -s -m "feat: add the dricket agent skill"
```

### Task 3: Install the skill through setup-agent without overwriting users

**Files:**
- Modify: `cmd/dk/init.go`
- Modify: `cmd/dk/init_cli_test.go`

**Interfaces:**
- Consumes: `skills.Dricket` and existing `runSetupAgent`.
- Produces: unchanged command signature; JSON retains `file`, `written` and adds `skill_file`, `skill_written`.

- [ ] **Step 1: Write failing installation tests**

In a code repository with a resolved store, run `setup-agent` with the source tree unavailable to the command's working directory. Assert the AGENTS pointer and `.agents/skills/dricket/SKILL.md` exactly match the embedded asset. Re-run and assert both written flags are false and contents unchanged. Pre-create a differing skill and assert general-error refusal, no overwrite and no AGENTS modification. Cover identical AGENTS/skill idempotence and JSON field names.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test -count=1 ./cmd/dk -run 'SetupAgent'`
Expected: no skill installation or new result fields.

- [ ] **Step 3: Implement preflight and safe installation**

Preflight the AGENTS pointer and skill before either write. Create missing parent directories and the skill atomically; append the AGENTS pointer using the existing behavior. If the skill exists with different content, return an error naming its path before any write. Keep human output informative and add only the two approved JSON fields.

- [ ] **Step 4: Run setup and CLI tests**

Run: `go test -count=1 ./cmd/dk`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/dk/init.go cmd/dk/init_cli_test.go
git commit -s -m "feat: install the dricket agent skill"
```

### Task 4: Verify agent behavior and close observed loopholes

**Files:**
- Modify only if a test demonstrates a gap: `skills/dricket/SKILL.md`
- Modify: `.superpowers/sdd/2026-11-03-dricket-agent-skill/progress.md`

**Interfaces:**
- Consumes: installed canonical skill from Tasks 2-3.
- Produces: green application scenarios and any evidence-driven skill refinement.

- [ ] **Step 1: Run equivalent scenarios with the installed skill**

Use fresh agent contexts and project-scope `.agents/skills/dricket/SKILL.md`. Do not supply the guide separately. Record whether agents discover the skill and execute correct store, workflow, comment and exit-code behavior.

- [ ] **Step 2: Refactor only observed failures**

If an agent still fails, add the smallest targeted clarification and repeat that scenario. Do not add hypothetical features or narrative examples. If the skill passes, make no content change.

- [ ] **Step 3: Verify real project-scope discovery**

Start a new supporting-tool session in the disposable code repository and confirm the dricket skill is registered and can be invoked. Record the tool actually tested; do not generalize this to untested tools.

- [ ] **Step 4: Commit verification evidence and any tested refinement**

```bash
git add skills/dricket/SKILL.md .superpowers/sdd/2026-11-03-dricket-agent-skill/progress.md
git commit -s -m "test: verify dricket agent skill discovery"
```

### Task 5: Document agent setup and run the final gate

**Files:**
- Modify: `README.md`, `site/index.html` agent-setup text
- Modify: `docs/CONCEPT.md`, `docs/architecture.md`, `docs/protocol.md` where guide/setup-agent behavior is specified
- Modify: `CHANGELOG.md`, `specs/changelog.md`

**Interfaces:**
- Consumes: Tasks 1-4.
- Produces: verified R-16 documentation and release notes.

- [ ] **Step 1: Update current documentation**

Describe canonical skill installation, `.agents/skills/dricket/SKILL.md`, idempotence/refusal and the continued role of `dk guide`. Add R-16 under Awaiting verification with baseline/green evidence and user-facing Unreleased notes.

- [ ] **Step 2: Run the full project gate**

Run: `go build ./... && go vet ./... && golangci-lint run && go test -count=1 ./... && python scripts/check_docs.py`
Expected: all commands exit 0.

- [ ] **Step 3: Exercise standalone setup**

Build `dk`, run `setup-agent` from a disposable code repository with no network, invoke the discovered skill in a fresh supporting-tool session and inspect the JSON/human output. Expected: correct offline installation and no overwrite on rerun.

- [ ] **Step 4: Commit**

```bash
git add README.md site/index.html CHANGELOG.md docs specs/changelog.md
git commit -s -m "docs: describe dricket agent skill setup"
```
