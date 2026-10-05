# bt

A keyboard-first terminal UI for a [beads](https://github.com/gastownhall/beads) task DB. It reads through `bd --json` and writes through `bd` subcommands, in a very opninionated way; suitable most for my personal workflows.

> [!NOTE]
> Consider this AI-slop, and not a polished product. Kind of works fine for general-purpose beads, but is really tuned for my own workflows.

## Requirements

- `bd` on `PATH`.
- `BEADS_DIR` pointing at the beads directory. When it is unset, bt uses `~/tasks/.beads`.
- Go, at the version `go.mod` declares. With the default `GOTOOLCHAIN=auto`, an older local Go downloads the right toolchain on first build.
- Optional: `wl` on `PATH` for the external-ref picker, and `$EDITOR` for opening code refs and formulas.

## Install

```sh
make install
```

This runs `go vet` and the tests, builds `bt` with its version stamped in (`git describe`, or `dev` outside a git checkout), and installs it to `~/.local/bin/bt`, next to `bd`. Pick another location with `PREFIX` or `BINDIR`:

```sh
make install PREFIX=/usr/local
make install BINDIR=~/bin
```

Check it with `bt --version`.

To update, pull or copy the new source and run `make install` again. On another machine, copy the repo over (`rsync -a ~/repo/beads-tui/ host:repo/beads-tui/`) and run `make install` there.

To remove the binary, run `make uninstall`, with the same `PREFIX` or `BINDIR` you installed with. bt leaves two small files behind, which can be deleted by hand:

- `~/.config/bt/state.json`: filters and other UI state
- `~/.cache/bt/`: the last snapshot, used for instant startup

## Use

```sh
bt                  # open the TUI
bt triage           # hygiene violations as a plain list
bt triage --json    # the same, for a cleanup agent
```

Inside the TUI, `?` lists every key. Views: `1` Now, `2` Ready, `3` Epics, `4` Triage, `5` Activity, `6` Graph, `7` Molecules. `S` runs `bd sync`.

## Develop

```sh
make build   # ./bt
make test
```

Tests that need a real DB are skipped unless their `BT_*` variable is set; see `internal/ui/live_test.go`.
