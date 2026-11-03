# Git-like CLI, structured help and agent-skill design

- Status: accepted
- Date: 2026-11-03
- Roadmap: R-15 and R-16, implemented as separate batches after R-14
- Related: ADR-0008, ADR-0009

## Intent

The existing CLI should behave consistently and borrow established Git
conventions where they fit, without adding ticket features. Root help should
reveal the workflow instead of listing 25 commands flat, and every command
should have accurate standalone help. AI tools should discover dricket through
an installed Agent Skill rather than relying on the chance that they read an
`AGENTS.md` pointer and execute `dk guide`.

## Goals

- Consistent Git-like text flags and option placement across existing commands.
- No redundant command for the same operation and no advertised global option
  that has no effect.
- Grouped root help and accurate per-command help, available without opening a
  store.
- A compact, cross-runtime dricket skill installed by the existing
  `dk setup-agent` command.
- No new ticket operation, server, dependency, network access or data format.

## Non-goals and anti-goals

- Do not rename domain commands merely to imitate unrelated Git verbs. `add`,
  `claim`, `release`, `sync`, `restore` and `commit` retain their established
  meanings.
- Do not add dry-run, prompting, color, quiet mode or new file-input features to
  make currently dead options appear useful.
- Do not keep compatibility aliases that perpetuate the inconsistent interface;
  dricket is pre-1.0 and the release notes must call out the CLI migration.
- Do not duplicate the full specification in the skill.
- Do not overwrite a user-modified skill or agent-instruction file.

## Approaches

### Chosen: explicit CLI contract plus one embedded canonical skill

Adjust the existing command registry and flag parsing, generate help from that
same metadata, and ship one canonical `skills/dricket/SKILL.md`. Embed that file
in the binary so `setup-agent` can install it offline.

This creates one source for each concern and uses the existing setup command.
The CLI changes are deliberately breaking and are released together with clear
migration notes.

### Rejected: aliases for every old spelling

Aliases would ease migration but preserve two vocabularies, complicate help and
make the eventual cleanup harder. A single documented pre-1.0 migration is
simpler for ten-year maintenance.

### Rejected: guide-only or AGENTS-only agent integration

`dk guide` is useful only after an agent knows to run it. A file in a recognized
skill directory participates in tool discovery. The guide remains as a concise
runtime reference, not the discovery mechanism.

### Rejected: tool-specific installers

Separate Claude, Kimi, Codex and other installer commands would multiply public
surface. The cross-runtime project path `.agents/skills/dricket/SKILL.md` is
recognized by Kimi Code and other Agent-Skills-compatible tools; tool-specific
paths remain possible through user configuration but are not implemented here.

## CLI text and reason flags

Use `-m, --message` for the single text or reason value an existing command
already accepts. Keep `-F, --file` only where it already exists. Options may
appear before or after positional arguments; `--` ends option parsing. Help and
examples must show valid invocations in either position.

| Command | New contract | Replaced contract |
| --- | --- | --- |
| `add` | `-m, --message <title>`; existing `-d` and `-F` for description | `-c <title>` |
| `note` | `note <id> -m <text>` | `note <id> <text>` |
| `comment` | `comment <id> -m <text>` or existing `-F` | `comment <id> <text>` |
| `extend` | `extend <id> -m <reason>` | `--reason` |
| `release` | `release <id> --stage <stage> -m <result>` | `--result` |
| `force-release` | `force-release <id> --stage <stage> -m <reason>` | `--reason` |

`update` is removed because it calls exactly the same service operation as
`note`; `note` is the surviving name. `set`, ticket IDs and all other positional
arguments remain unchanged. No command gains a new text-source capability.

The parser must distinguish value-taking flags from Boolean flags using the
registered flag metadata. Unknown options, missing values and text after `--`
follow normal usage-error rules. Ticket text is data, never reparsed as an
option after the terminator.

## Global options

Keep only implemented global behavior:

- `--json`
- `--store <path-or-url>[#branch]`
- `--profile <name>`
- `--version`
- `-h, --help`

Remove `-q/--quiet`, `--no-color`, `-y/--assume-yes` and `-n/--dry-run`: none
currently changes command behavior. `NO_COLOR` needs no special handling while
the CLI emits no color. Global options retain their current accepted placement;
command-option parsing owns everything else. No new global option is added.

The JSON envelope, exit codes and service behavior remain unchanged except for
the additive `setup-agent` result fields described below.

## Help structure

Root help uses `dk`, not the former binary name `dricket`, in its usage line.
Commands are grouped by task rather than implementation package:

- **Setup:** `init`, `use`, `setup-agent`, `guide`
- **Work:** `add`, `next`, `claim`, `sync`, `restore`, `commit`, `note`,
  `comment`, `extend`, `release`, `force-release`, `set`
- **Inspect:** `ls`, `show`, `search`, `inbox`, `leases`
- **Check:** `lint`, `validate`, `doctor`, `version`

Each command registry entry carries its usage and synopsis. Flag defaults come
from the actual registered flag set rather than a second hand-maintained list.
Both `dk <command> --help` and `dk help <command>` print that help to stdout and
exit 0 without resolving a store. Root `dk help` remains equivalent to
`dk --help`; unknown help topics and malformed invocations remain exit 2.

The roadmap note is removed from user help. README, guide, concept examples and
the landing page are updated to the new spellings in the same change.

## Agent Skill

The canonical source is `skills/dricket/SKILL.md` with valid YAML frontmatter:

- `name: dricket`
- a third-person description beginning `Use when...` and covering dricket,
  `dk`, tickets, claims, comments, sync/commit and lease failures;
- no tool-specific placeholders and no arguments.

The body is a compact reference containing:

1. Store/project discovery and the unique `DK_OWNER` requirement.
2. The standard `next`/`claim` → `sync` → edit → `inbox` → `commit` → `release`
   workflow using the new CLI contract.
3. The rule to edit only a claimed ticket and comment on other claimed tickets.
4. Exact stop/preservation behavior for exits 12, 13 and 15
   `outcome-unknown`.
5. Add-only comments, no ticket/comment deletion and untrusted ticket content.
6. A pointer to `dk guide` for the installed binary's concise current reference.

The skill is embedded in `dk`; installation never downloads content.

## setup-agent installation

`dk setup-agent` retains its existing `AGENTS.md` pointer and additionally
installs the embedded skill at `.agents/skills/dricket/SKILL.md` relative to the
current project root/current directory resolution used by the command.

The operation preflights both outputs before writing:

- Missing files are created; identical files are left unchanged.
- The existing `AGENTS.md` append behavior and idempotence remain.
- A differing existing skill is treated as user-owned: nothing is overwritten,
  and the command returns a general error naming the path. The user can review,
  move or delete it before rerunning.
- No existing file is truncated in place; writes use safe file creation/atomic
  replacement rules already appropriate for user-owned files.

Human output names both installed/refused paths. JSON output retains `file` and
`written` for the AGENTS pointer and adds `skill_file` and `skill_written` for
the skill. There is no new command or flag.

## Verification

### CLI

Write failing CLI tests before implementation for every changed flag and the
removed spellings. Cover options before and after IDs, `--`, missing values,
unknown flags, root groups, every command's help and the guarantee that help
does not open a store. Verify `update` and dead global options are rejected.
Update all existing command examples and run the full suite.

### Skill

Follow skill-authoring TDD. Before writing the skill, give a fresh agent a
realistic dricket task in a disposable store without the skill and record the
workflow/discovery failures. After installation, rerun equivalent tasks with
only the installed skill available. Verify correct claim/sync/edit/commit/
comment behavior and exit-code handling. If the baseline already works, do not
add speculative guidance; keep only guidance tied to observed gaps.

Test `setup-agent` installation, identical-file idempotence, differing-skill
refusal, no overwrite of AGENTS or skill content, offline operation and JSON
shape. Validate frontmatter and project-scope discovery in at least one real
supporting tool; do not claim that every AI tool was tested.

## Documentation and delivery order

R-14 lands first as the storage/initialization batch. R-15 then changes the CLI
and help. R-16 separately adds and tests the skill and `setup-agent`
installation. Each receives its own commits and full verification; the release
notes present the breaking CLI migration explicitly. The internal work log and
roadmap are updated only as each batch is actually verified.
