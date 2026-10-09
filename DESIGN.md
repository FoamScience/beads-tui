# beads-tui design

A keyboard-first TUI for the global task DB (`BEADS_DIR=~/tasks/.beads`). Built around how the DB is actually used: agents write, the human supervises, triages and steers.

## Evidence

Sources: atuin shell history (58 `bd` commands), Claude transcripts under `~/.claude/projects` (~2,000 `bd` calls), the live DB (1,228 issues), and the beads skill / CLAUDE.md rules.

### Human (atuin)

| Pattern | Count | What it means |
|---|---|---|
| `bd list --status in_progress,pinned,deferred,... -w` | 27 | Main loop: a live view of active work. Tried `watch`, `-w`, even `vim` to keep it open. Status mix was edited about 12 times, so the filter set must be quick to change. |
| `bd show <id> [--long]` | 8 | Drill-down, usually from an id seen in the list. |
| `bd ready [--gated] [<epic>]` | 4 | What can start next, sometimes per epic. |
| `bd list --type epic / --level top / --parent <epic>` | 4 | Epic-level overview, then into one epic. |
| `for e in epics; bd swarm validate $e` | 2 | Epic graph health across all epics. |
| `bd list --status open --json \| jq 'no machine: label'` | 2 | Hygiene: open beads with no `machine:` label. |
| `bd update <id> --external-ref <wl-key>` | 2 | Linking root beads to wl worklog keys. |
| `bd dolt pull / push / sync` | 4 | Manual sync between machines. |

### Agents (transcripts)

`update` 543, `show` 312, `create` 188, `close` 173, `note` 126, `list --status` 119, `dep` 52, `search` 20, `label` 18, `comment` 18, `mol` 12. Agents produce a steady stream of status changes and notes; the human needs to see that stream, not repeat it.

### Data shape

- 240 open, 31 in progress, 62 blocked, 16 deferred, 941 closed; 35 epics; 1,194 of 1,228 have a parent.
- Labels: `machine:<host>` on ~790 issues (three hosts), `auto-ok` 32 (nightshift eligibility), `repo:*`, topic labels.
- `estimated_minutes` on 432, `external_ref` on 24 (wl keys, `commit:<sha>`), `metadata.refs` holds code locations (ratchet convention).
- `bd list --json --all` takes ~0.9 s and returns 3.3 MB.

## Assumptions

1. Single DB only (`~/tasks`); no per-repo `.beads`.
2. Read through `bd ... --json`, write through `bd` subcommands. No direct Dolt SQL. This keeps bd's validation and hooks in the write path.
3. Supervisor tool, not an agent replacement: creation is a quick form, bulk authoring stays with agents.
4. ratchet.nvim is not considered; the TUI stands on its own and opens `metadata.refs` in `$EDITOR`.
5. Current host is known (`hostname`), so "my machine" filters work without config.

## Machine filter

One filter, chosen with `M` from any list tab and remembered, applies to Now, Ready, Epics, Triage, Activity, Inbox and Board: beads labelled for the selected machine (this one by default, or any `machine:` host, or all), plus beads with no machine label, which belong to nobody and so show everywhere. Graph keeps the whole dependency graph so no edge loses an end; search, Molecules and `bt triage --json` stay global.

## Information architecture

Five views, one shared detail pane. Number keys switch views.

| Key | View | Replaces |
|---|---|---|
| `1` | **Now**: in_progress, pinned, hooked, grouped by epic, live | `bd list --status ... -w` |
| `2` | **Ready**: unblocked work, filter by machine/label/epic | `bd ready` |
| `3` | **Epics**: tree with progress, blocked count, swarm lint result | `--type epic`, `--parent`, `swarm validate` loop |
| `4` | **Triage**: hygiene violations, fixable inline | the jq one-liner, wl ref chores |
| `5` | **Activity**: recent changes across the DB, newest first | scrolling agent output |
| `6` | **Graph**: dependency DAG of an epic or issue, navigable | `bd graph`, `bd dep`, `bd blocked` |
| `7` | **Molecules**: formulas, protos, live molecules and wisps | `bd formula`, `bd cook`, `bd mol pour/wisp/progress/squash/burn` |
| `8` / `i` | **Inbox**: beads an agent labelled `human`, pending first; `r` responds (comment + close), `X` dismisses; follows the same machine filter as Now and Ready (`M`: this machine by default, all, or one host), and the header badge counts what that filter shows; a human bead with no machine label shows under every filter | `bd human list/respond/dismiss` |
| `9` / `b` | **Board**: kanban lanes Ready, Blocked, In progress, Deferred, Done (last 7 days), non-epic issues only; `h/l` columns, `j/k` cards, `H/L` move the card (or the marked cards) to the next lane, which sets its status; `E` scopes the board to one epic | `bd update --status`, close flow for Done |
| `0` | **Live**: live ledgers (status `pinned`, label `live`, machine-filtered) rendered as ledgers: summary, the Current state table as an aligned grid, the Log newest first with date chips, and the design field as a plain-text Runbook; a ⚠ line names any departure from the ledger format in the beads skill. `+` appends a dated log line and `E` edits the description in `$EDITOR`, both written back with `bd update --body-file -` | `bd update --body-file -` |

`/` searches everywhere (title, id, labels). `Enter` opens detail. `g` on any row opens Graph rooted at its epic. `?` shows all keys.

## Screens

### 1 Now (default)

```
 beads  ~/tasks          Now  Ready  Epics  Triage  Activity      ● synced 2m  MS-7D15
 ──────────────────────────────────────────────────────────────────────────────────────
  status: in_progress pinned hooked   machine: any   (f to edit)          31 active

  tasks-8wk  Harvester: gather reputable sources                   ▰▰▰▱▱  3/5
  ▸ ◐ 8wk.20.1  P1  Literature adapters: OpenAlex, Crossref    ThinkPad  4h   45m
    ◐ 8wk.20.2  P1  Credibility gate: deterministic metadata   ThinkPad  4h   30m
    ◐ 8wk.10.1  P1  Fetch fallback: Unpaywall + S2             MS-7D15   1d    —

  tasks-e4u  Lean witness                                        ▰▰▰▰▱  233/251
    ◐ e4u.233   P1  bug  Witness conjunction reads proved      MS-7D15  12m  20m
    ◐ e4u.236   P1  bug  NaN particles drop out of reduction   MS-7D15   3m  20m

  (no epic)
    ◐ h1w.12    P0  First real CFD optimization study          MS-7D15   2d   2h
 ──────────────────────────────────────────────────────────────────────────────────────
  enter detail  s status  n note  c close  / search  f filter  r refresh  ? keys
```

- Columns: id (prefix trimmed), priority, type only when not `task`, title, machine, age since last update, estimate. Age turns amber past a threshold (stale agent).
- Claimed beads show their agent instead of the machine: bt polls `herdr pane list` every 3 s and matches each pane's live Claude session against the `claude_session.*` metadata the herdr-bd-sessions hook writes on `--claim` or `--status in_progress`. `▶ title` means working, `◦ title` idle, `○ gone` that the session has ended. Board cards and the detail pane show the same, and `R` jumps to that pane.
- Rows changed since the last refresh flash once, then show a dim dot for a minute. This answers "what are the agents doing" without leaving the view.
- Filter chips are the status set the history shows being edited by hand; `f` toggles them, the choice persists.

### Detail pane (right split ≥140 cols, full screen below)

```
  ◐ tasks-e4u.233  bug  P1  in_progress            MS-7D15 · Claude · started 12m
  Lean witness conjunction: partially decided conjunction reads proved
  parent  tasks-e4u  Lean witness                 ext  LOCAL-27   est 20m
  labels  lean  machine:elwardi-MS-7D15

  Description ───────────────────────────────────────────────
  ...markdown rendered...
  Acceptance ────────────────────────────────────────────────
  Notes  (3) ────────────────────────────────────────────────
  12m  decision: chose X over Y because Z
  Deps   blocks 2  ·  blocked by 0  ·  discovered-from e4u.200
  Refs   src/witness.lean  WitnessReduce.reduce  :142        o open
```

Children, dependencies and dependents listed in the pane are links: `tab`/`shift+tab` move a cursor over them, `enter` opens one in the same pane, and `esc` walks back through the issues opened this way before closing the pane. Sections collapse with `Z`. Notes come newest first, since that is where agents leave their reasoning. Under the labels, a blocked issue shows its blocker chain (`waits on x.5 → x.3`), followed from its own blockers and then its ancestors' down to the first one that is free to move; list rows show the last link as `⊘ x.3`. A `time` line compares elapsed time (started to closed, wall clock) with the estimate, and parents roll this up over their closed descendants. An `Agent sessions` section lists the Claude sessions the herdr-bd-sessions hook recorded when an agent claimed the bead.

### 3 Epics

Tree of epics, each with a progress bar, counts by status, and a lint badge from `bd swarm validate` (✓, or ⚠ with reversed edges, orphans, cycles). Lint runs lazily per epic and is cached until the epic changes. `Enter` expands children; `R` shows `bd ready --mol <epic>`.

### 4 Triage

A checklist of rule violations, each row fixable in place:

| Rule | Fix key |
|---|---|
| open/in_progress without `machine:` label | `m` add this host or pick |
| root bead without `external_ref` (wl key) | `x` pick from `wl list --json` |
| root bead without `estimated_minutes` | `e` |
| task with no parent epic | report only |
| in_progress with no update for 3 days | `s` change status |
| open epic whose children are all closed (same set as `bd epic close-eligible`) | `c` close |
| code-touching task without `metadata.refs` | report only |

Rules come from the CLAUDE.md beads rules, so the TUI enforces the same contract agents are told to follow.

`bt triage --json` prints the same rules without opening the TUI, for handing to a cleanup agent: per rule an id, a count and the offending issues (id, title, status, type, priority, parent, epic, labels, external ref, estimate, updated time) plus a ready-to-edit `bd` fix command. Placeholders such as `<host>` stay unresolved, and `known_machines` lists the existing `machine:` labels so the agent picks the right one instead of defaulting to the current box. `bt triage` without the flag prints a plain list.

## Actions

| Key | Action | bd call |
|---|---|---|
| `s` | status picker | `bd update <id> --status` |
| `c` | close with reason | `bd close <id> --reason` |
| `n` | add note | `bd note <id>` |
| `l` / `m` | label edit / machine label | `bd label add/remove` |
| `x` | external ref (wl picker) | `bd update --external-ref` |
| `e` | estimate | `bd update -e` |
| `d` | defer until | `bd update --defer` |
| `a` | quick create child of current epic | `bd create --parent -t` |
| `o` | open ref in `$EDITOR` | none |
| `y` | yank id(s) | none |
| `R` | resume the agent: focus its herdr pane if still open, else `claude --resume <session>` in the session's directory | `herdr pane get`, `herdr workspace/tab focus` |
| `space` / `esc` | mark rows / clear marks; `s c p l m x e d C y` then act on every marked issue in one bd call | `bd update <ids…>`, `bd close <ids…>` |
| `S` | sync with the Dolt remote | `bd sync` |

Writes are optimistic: the row updates at once, and a failed command reverts it and shows the stderr line in the footer. Close and status changes on epics ask for confirmation.

## Data and refresh

- One full snapshot (`bd list --json --all --limit 0`) held in memory; views are filters over it. Each bd call costs 2 to 7 s on this machine (opening the embedded Dolt DB dominates, so incremental queries are no cheaper).
- The last snapshot is cached in `~/.cache/bt/`, so bt draws in about 0.25 s and refreshes in the background.
- Change detection reads the Dolt manifest once a second and reloads only when its contents change. Its mtime cannot be used: read-only bd calls rewrite the file. A full reload also runs every 2 minutes; a failed load retries after 5 s.
- Loads carry a generation number, and a snapshot that arrives while a write is in flight is dropped, so optimistic edits never flicker back.
- Comments load with `bd comments <id> --json` when the detail opens, and reload when the comment count changes.
- `S` runs `bd sync`; the header shows the time since the last sync.

## Stack

Go + Bubble Tea + Lip Gloss + Bubbles + Glamour (markdown). Same language as bd, single binary, Glamour renders descriptions the way `bd show` already does.

### 6 Graph

Layered DAG computed from the in-memory snapshot with the same layering as `bd graph` (longest path over blocking edges), so it updates live without another bd call. Layer 0 sits on the left; nodes in one column can run in parallel. Long edges pass through placeholder slots in the layers they skip, and a barycenter pass orders each column to keep lines straight. Nodes are compact boxes (status glyph, short title, id, priority, estimate) colored by status. Prerequisites outside the epic appear as dashed boxes. Only blocking edges are drawn; `related`, `discovered-from` and the rest are listed in the detail pane.

```
  tasks-gpj  beads-tui
   layer 0             layer 1           layer 2           layer 3
  ┌──────────┐        ┌──────────┐      ┌──────────┐      ┌──────────┐
  │○ gpj.1   │──┬────▶│○ gpj.3   │─────▶│○ gpj.4   │─────▶│○ gpj.7   │
  │ Scaffold │  │     │ Detail   │      │ Actions  │      │ Triage   │
  └──────────┘  │     └──────────┘      └──────────┘      └──────────┘
                ├────▶ gpj.2 Now   gpj.5 Ready   gpj.6 Epics ...
```

- `h/j/k/l` moves between nodes along edges; `Enter` opens detail; the selected node's upstream and downstream paths are highlighted.
- Edit edges in place: `D` add a dependency (pick target, pick type), `X` remove the selected edge. Each edit runs `bd dep add/remove`, then `bd swarm validate`, and shows a new cycle or reversed edge in the footer before it hits the rest of the graph.
- Wide graphs scroll horizontally; layers wider than the screen collapse to `+N more`, which expands on `Enter`.

### 7 Molecules

Three panes: formulas (from all `bd formula list` search paths), live molecules and wisps (with `bd mol progress`), and a preview.

- **Author:** `n` scaffolds a new `.formula.toml` in `~/tasks/.beads/formulas/`; `E` opens the selected one in `$EDITOR`. When the editor exits, the TUI runs `bd cook <formula>` (compile mode), reports the parse error in the footer if there is one, and reloads the preview.
- **Distill:** `d` on an epic runs `bd mol distill` to turn ad-hoc work into a formula, then opens it for editing.
- **Pour / wisp:** `p` / `w` opens a form with one field per declared variable (defaults filled in), shows the `--dry-run` result, then creates it. Afterwards the same form sets the new root's parent, title and labels. `local-e2e.formula.toml` documents these three follow-up commands as a manual step that leaves a stray root when skipped, so the TUI does them as part of the pour.
- **Lifecycle:** `Q` squash, `B` burn (confirm, irreversible), plus `bd mol stale` results flagged in the list. `S` stays the global sync key.
- **Preview:** a formula shows its vars and a step DAG drawn with the Graph renderer (shared title prefix such as `local-e2e {{change}}: ` is trimmed); `h/l` moves between layers and `J/K` within a layer (`j/k` stay on the list). A molecule or wisp shows its live graph.

## Out of scope for v1

Merge-slot, multi-DB switching, mouse. Add when a real use shows up.
