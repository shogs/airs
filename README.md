# airs

Read your markdown on your iPad or iPhone. `airs` shares markdown files and folders from your Mac
over Wi-Fi. Pages refresh when you save, you get a QR code for every link, and you can AirDrop
any link to your devices.

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
| `airs <file\|folder> [--any]` | Share a markdown file or a folder. Prints the link and a QR code. |
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

- **Background server.** The first command starts a small server through launchd. Nothing is
  installed as a login item. The server stops on its own once nothing is shared.
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

## Configuration

`~/.config/airs/config.toml` (or `$XDG_CONFIG_HOME/airs/config.toml`):

```toml
port = 7777          # LAN port; airs fails with a clear message if it's taken
any_network = false  # true = share on every network
```

## Files

| Path | Contents |
|---|---|
| `~/.local/state/airs/state.json` | Shared items and trusted networks |
| `~/.local/state/airs/airs.log` | Server log |
| `~/.local/state/airs/airs.sock` | Control socket (JSON over HTTP), also meant for a future menu bar app |

## Development

```
go test ./...          # includes an end-to-end test that builds and runs the binary
go test -short ./...   # skips the end-to-end test
```
