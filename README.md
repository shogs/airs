# airs

Read your markdown on your iPad or iPhone. `airs` shares markdown files and folders from your Mac
over Wi-Fi. Pages refresh when you save, you get a QR code for every link, you can AirDrop
any link to your devices, and you can jot notes on a page with your finger or Apple Pencil.

```
$ airs notes.md
serving notes.md → http://192.168.1.20:7777/notes.md
█▀▀▀▀▀█ ▄▀ ▄ █▀▀▀▀▀█
...
```

## Install (from source)

Needs Go 1.27+ and macOS.

```
go install ./cmd/airs
```

## Commands

| Command | What it does |
|---|---|
| `airs <file\|folder> [--any] [--no-notes]` | Share a markdown file or a folder. Prints the link and a QR code. `--no-notes` stops readers adding notes to it. |
| `airs add <file\|folder> [--any] [--no-notes]` | Same as above, spelled out. Use it, or `./`, for a file whose name matches a command (`airs add list`). |
| `airs list` | Show what's shared, with ids, links and status. |
| `airs remove <id\|name>` | Stop sharing. Takes an id, a name (`notes.md`, `docs`), or a path. |
| `airs qr [id\|name]` | Print a QR code. With no argument it prints the code for the index page. |
| `airs send <id\|name>` | AirDrop the link. If AirDrop isn't available, the link is copied to the clipboard. |
| `airs trust [label]` | Trust the network you're on, e.g. `airs trust home`. |
| `airs untrust` | Stop trusting the current network. |
| `airs stop` | Stop the background server. Your list is kept and comes back on the next command. |

Links use names: `/notes.md` for a file, and `/docs/` or `/docs/sub/page.md` for a folder. When two
shared items have the same name, the second one gets `-2`, as in `/README-2.md`.

## How it works

- **Background server.** The first command starts a small server through a transient launchd
  job. It isn't a login item and is gone when you log out. The server stops on its own once
  nothing is shared.
- **Live reload.** Open pages update as soon as the file changes on disk, and your scroll
  position is kept.
- **Keep reading.** If the server stops, the item is removed, or the file is deleted, the open
  page keeps showing the last version, with a banner explaining why. **⤓ Page** saves a
  self-contained `.html` file for offline reading. **⤓ .md** saves the source.
- **Trusted networks only.** airs only shares on networks you've trusted with `airs trust`. On
  any other network, items can be opened on the Mac itself and nowhere else. A network is
  recognised by its router's IP and hardware address, so airs needs no Location permission. To
  share one item on any network, add it with `--any`. To turn the check off everywhere, set
  `any_network = true` in the config.

## Notes

Tap a paragraph on the iPad (finger or Apple Pencil) to add a note under it, or select some text
and tap **Add note** to quote it. Type, or write with the Pencil: iPadOS Scribble turns
handwriting into text. Tap a note to edit it.

- **Saved next to the document.** `README.md` gets `README.notes.md` in the same folder. It's
  plain markdown with one section per note, so you can read it in any editor. Editing or deleting
  a note there updates open pages live.
- **Stays with its paragraph.** If the document changes, each note stays under the paragraph it
  was written on. If that paragraph is gone, it moves to a **Notes** list at the end.
- **Edits don't clash.** If a note was changed on the Mac while you edited it on the iPad, airs
  shows the Mac's version and keeps what you typed, so nothing is overwritten.
- **Live / Offline.** The pill in the top-right corner shows whether the page is connected.
  Offline, notes are read-only: an open note keeps your text and saves once you're back.
- **Who can write.** Notes can be added from your trusted network or the Mac itself. Items shared
  with `--any` are read-only on other networks.
- **Turning notes off.** Use `--no-notes` for one item, or `notes = false` in the config for all of
  them. Mouse clicks on the Mac don't open a note box; select text and use **Add note** instead.

## Configuration

`~/.config/airs/config.toml` (or `$XDG_CONFIG_HOME/airs/config.toml`):

```toml
port = 7777          # LAN port; airs fails with a clear message if it's taken
any_network = false  # true = share on every network
notes = true         # false = readers can't add notes anywhere
```

## Files

| Path | Contents |
|---|---|
| `~/.local/state/airs/state.json` | Shared items and trusted networks |
| `~/.local/state/airs/airs.log` | Server log |
| `~/.local/state/airs/airs.sock` | Control socket (JSON over HTTP), also meant for a future menu bar app |
| `<doc>.notes.md` next to each document | Reader notes for that document |

## Known issues

- **Name vs. local file:** `airs remove notes.md` fails if a *different* `notes.md` is in the
  current folder, because a path is matched before a name. `qr` and `send` have the same issue.
  Use the id instead.
- **Colons:** listing links break for file names containing `:`.
- **No reconnect after stop:** open pages show "No longer shared" after `airs stop` and don't
  resume when the server starts again. Reload the page.
- **Restart timing:** a link printed at the exact moment the idle server shuts down only works after the next `airs` command.
- **Failed saves:** if saving the list fails, the running server and `state.json` can disagree.
  If removing fails, open pages aren't told the item is gone.
- **Folder refreshes:** a change anywhere in a shared folder refreshes every open page from that
  folder, which closes any open `<details>` sections.
- **Raw HTML images:** `<img src="x.png">` written as raw HTML in a single shared file doesn't
  load. Markdown image syntax works.
- **Refresh order:** two quick refreshes aren't ordered, so a slow older one can overwrite a
  newer one.
- **Many tabs:** each tab holds one live-update connection, so six or more airs tabs in one
  desktop browser can stall.
- **`untrust` message:** `airs untrust` reports success even when the network wasn't trusted.
- **Notes under list items** are placed inside the list, which is fine in browsers but isn't
  strictly valid HTML.
- **Pencil and Scribble** are only tested in an iPad-sized desktop browser so far; see the manual
  checks in IMPLEMENTATION.md.

## Development

```
go test ./...          # includes an end-to-end test that builds and runs the binary
go test -short ./...   # skips the end-to-end test
```

See [IMPLEMENTATION.md](IMPLEMENTATION.md) for how airs works inside, security notes, and manual
checks on real devices.
