# airs

`airs` shares markdown files and folders from a Mac to the person's iPad or iPhone over Wi-Fi.
Pages reload when the file changes, and the reader can leave notes, which airs saves to a
`.notes.md` file. It's a Go CLI that starts its own background server. The person's guide is
[README.md](README.md).

## Installing airs

Needs Go 1.27+ and macOS. From this repository:

```sh
go install ./cmd/airs
command -v airs
```

If `command -v` finds nothing, the Go bin folder isn't on PATH: `$(go env GOBIN)`, or
`$(go env GOPATH)/bin` when GOBIN is empty. Tell the person which folder to add; don't edit their
shell profile unless they ask.

airs shares only on networks the person has trusted. Trusting one is their call, so leave
`airs trust` and `--any` to them. They run `airs trust home` once, on their home Wi-Fi.

## Installing the skill

`skills/airs/SKILL.md` tells an agent when and how to share documents with airs and read the
notes back. Copy it into your own user-level skills folder:

| Agent | Folder |
|---|---|
| Claude Code | `~/.claude/skills/airs/` |
| Codex | `~/.codex/skills/airs/` |
| Pi | `~/.pi/agent/skills/airs/` |

```sh
mkdir -p <folder> && cp skills/airs/SKILL.md <folder>
```

For any other agent, or if your docs now name a different folder, use the folder your docs give.
Copy the skill again whenever airs is updated, so it matches the installed flags.

## Using airs

The skill is the how-to: follow it rather than this file. `airs help` lists every command and
flag of the installed version. In short, `airs add <doc> --json --qr` shares a document and
`airs notes <doc> --json` reads its notes. Both print one JSON value, `{"error": "…"}` on
failure.

## Working on airs

- Read [IMPLEMENTATION.md](IMPLEMENTATION.md) first: layout, the background server, links and
  projects, notes, testing.
- Run `gofmt -l .` (it should print nothing) and `go test -race ./...` before handing work back.
  `go test -short ./...` skips the end-to-end test, which builds the binary and runs it with a
  temporary HOME.
- To try a build by hand without touching the person's own airs, give it its own HOME and port:
  write `port = 7792` to `$HOME/.config/airs/config.toml` under a temporary HOME, and run every
  command with that HOME. Each HOME gets its own launchd job, `dev.airs.server.<hash>`. When
  you're done, run `airs stop` with that HOME and `launchctl bootout gui/$(id -u)/<label>`. Boot
  out only the jobs you started: one of the `dev.airs.server.*` jobs is the person's own.
- Keep `README.md`, `IMPLEMENTATION.md`, `skills/airs/SKILL.md` and `airs help` in step with
  any change to commands, flags or JSON output.
