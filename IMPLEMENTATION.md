# Implementation notes

How airs works under the hood, why it's built that way, and what to check by hand. For usage, see
[README.md](README.md).

## Layout

| Package | Role |
|---|---|
| `cmd/airs` | Entry point. `airs __serve` runs the background server; everything else is the CLI. |
| `internal/cli` | Commands, output formatting, AirDrop/clipboard fallback. |
| `internal/client` | Talks to the server over the control socket and starts it when needed. |
| `internal/server` | The server: LAN HTTP, control API, access checks, path resolution, live events. |
| `internal/state` | Shared items and trusted networks, saved to `state.json`. |
| `internal/watch` | File watching (FSEvents), turned into per-item change events. |
| `internal/render` | Markdown to HTML (goldmark + chroma). |
| `internal/notes` | The `<doc>.notes.md` file format: read, append, edit. |
| `internal/netinfo` | Identifies the current network (router IP and hardware address). |
| `internal/airdrop`, `internal/qr` | AirDrop picker (cgo, `NSSharingService`) and QR codes. |
| `internal/web` | Embedded templates, CSS and the page script. |

## Background server

- **Two listeners.** The server listens on the LAN port (default 7777) for browsers, and on a
  Unix socket (`~/.local/state/airs/airs.sock`) for the control API: JSON over HTTP. A menu
  bar app can use the same API without any server changes.
- **Started through launchd.** The CLI starts the server as a temporary launchd job
  (`launchctl bootstrap gui/<uid>`, label `dev.airs.server.<hash>`). It isn't a login item, and
  it's gone at logout.
  - **Why:** on recent macOS, a process started by another program can't read the ARP table,
    which the network check needs. A process started by launchd can.
  - **Fallback:** if launchd fails, the CLI starts the server directly. The server then can't
    identify any network, so every network counts as untrusted.
- **Starting and stopping.**
  - A new start never interrupts a server that's already running.
  - `airs stop` waits until the server process has fully exited.
  - The server doesn't unload its own launchd job; a loaded, idle job is harmless.
  - A server that is still exiting counts as running for a moment. While waiting for the socket,
    the CLI asks launchd to start the server again every 250ms, so a command issued just as the
    old server exits still gets a new one.
- **Single instance.** A lock file ensures only one server runs, and a stale socket is removed on
  start.
- **Auto exit.** The server exits about 1s after the last item is removed.
- **Shutdown.** Open connections get 300ms to finish, then they're closed.
- **Log.** `airs.log` is truncated once it passes 1MB.

## Paths and names

- **Symlinks resolved.** Paths are stored with symlinks resolved, so the same file reached two ways
  (for example `/tmp` and `/private/tmp`) matches. This applies on add, remove and lookup.
- **Lookup order.** An argument is matched as an id first, then a path, then a name. `qr` and
  `send` use the same lookup as `remove`, done by the server.
- **URL names.** A URL name is the item's basename; on a clash it gets `-2`, `-3`, and so on.
  `_airs` is reserved for airs' own endpoints.
- **Internal key.** Each item has a key made from its name and a hash of its path. If a name is
  reused for a different file, open tabs of the old one are told it's gone rather than showing
  the new file.

## What gets served

- **Folders.** Markdown files and subfolders, and any non-hidden file inside the folder (so
  images work). Hidden files and folders are never served.
- **Single files.** A shared file serves only the images its markdown references, from its own
  folder. Sharing `~/Desktop/notes.md` doesn't expose the rest of the Desktop.
- **Containment.** Every path is cleaned and must still be inside the item after symlinks are
  resolved. `..`, backslashes and NUL bytes are rejected.
- **Raw HTML.** Raw HTML in markdown is rendered, including scripts. See the security notes.

## Network trust

- **Fingerprint.** A network is recognised by its router's IP and hardware (MAC) address. The
  MAC is read from the routing table through `sysctl`, not the `arp` command. No Location
  permission is needed.
- **Recheck.** The server rechecks the network every 5s.
- **Per address.** Trust applies only to requests arriving on the trusted network's local
  address. Trusting home Wi-Fi doesn't also open airs on a VPN or Tailscale interface.
- **The Mac itself.** Requests from the Mac (loopback or any of its own addresses) are always
  allowed.
- **Host header.** It must be an IP address, `localhost`, or a `.local` name. This blocks DNS
  rebinding: a web page that points its own domain at your Mac to read your documents.

## Notes

- **One record.** `<doc>.notes.md` beside the (symlink-resolved) document is the only record of
  notes. airs reads it back to show notes on the page, so editing the file on the Mac updates open
  pages.
- **Format.** Each note is a hidden marker line followed by readable markdown:

  ```markdown
  <!-- airs:note {"id":"20261123-140200-k3f9","block":"Needs Go 1.27+ and macOS.","index":4,"section":"install-from-source","time":"2026-11-23T14:02:00+11:00"} -->
  ### Install (from source) · 23 Nov 2026, 14:02

  > Needs Go 1.27+

  Check if 1.26 works too
  ```

  A note runs from its marker to the next marker. Lines starting `>` right after the heading are
  the quote; the rest is the text. Anything else in the file (your own headings, text before the
  first marker) is ignored and kept as is. `<!--` typed in a note is stored as `<!‐‐` so it can't
  start a marker.
- **Placement.** `block` is the start (80 characters) of the paragraph the note was added on, and
  `index` is that paragraph's position. The page looks for the paragraph by text first, then by
  position within the same section, then the section heading, then puts the note in a **Notes**
  list at the end. Paragraphs are `p, li, h1–h6, blockquote, pre, table`, nearest first. That rule
  is stored in people's notes files, so changing it would misplace existing notes.
- **Edits.** Only the edited note's text and its marker (`edited` time) change; every other byte
  of the file is kept. The page sends the text it started from, and if the file now says
  something else the server answers 409 with the current note instead of overwriting.
  Writes go through one lock and edits replace the file atomically.
- **Limits.** Note text up to 10 000 bytes, quote up to 2 000, request body up to 16 KiB.
- **Who can write.** A write needs a request from the Mac itself, one on the trusted network, or
  `any_network = true`. Per-item `--any` only widens reading. The page gets `notesEnabled` per
  request, so the same page offers notes at home and is read-only elsewhere.
- **Other websites can't write.** Writes must be `application/json` (a cross-site form can't send
  that without a preflight) and any `Origin` header must be this server. With the Host check,
  that rules out drive-by writes from other web pages.
- **Turned off** by `notes = false`, by `--no-notes` on the item, and always for `*.notes.md`
  pages themselves.
- **Watching.** A shared file's watcher also watches its notes file; a folder's notes files are
  inside the folder and already watched.

## File watching

- **FSEvents.** Watching uses one recursive stream per item. Unlike kqueue, it needs no file
  descriptor per file, so large folders (repos with `node_modules`) work. It also handles editors
  that save by rename, and folders that are deleted and recreated.
- **Stale events.** FSEvents can report changes made just before watching started. Those are
  filtered out with file timestamps (change time for files, creation time for folders).
- **Debounce.** Changes are debounced by 100ms. A quick burst of saves can still cause two
  refreshes instead of one, which is harmless.
- **Hidden files.** Changes to hidden files (`.git`, `.DS_Store`) are ignored.

## Pages in the browser

- **Live updates.** Pages get them over Server-Sent Events, with a ping every 25s. While a page
  is open, the server rechecks access every 2s.
- **Reconnect.** After a dropped connection (for example the iPad slept), the page reconnects and
  fetches the latest version to catch up on missed edits.
- **Folder listings.** They update in place rather than reloading.
- **Live / Offline pill.** Shows whether the live-update connection is open. Notes can only be
  added or edited while Live. A note box that's open when the page goes Offline keeps its text
  and saves once it reconnects. A box with unsaved text is never replaced by opening another.
- **Taps.** Only finger and Pencil taps open a note box (`pointerType` touch or pen); mouse clicks
  on the Mac don't, so reading there doesn't pop boxes open. Links, buttons and selections are
  left alone.
- **Keep reading.** An open page never replaces content with an error. It keeps the last version
  and shows a banner: no longer shared, offline (retrying), or file missing on disk.
- **Downloads.** Both are built in the browser, so they work with the server gone: a
  self-contained `.html` (inline CSS, images as data URIs, no scripts, with note cards) and the
  raw `.md` (the document only, without notes).

## Security notes (accepted for now)

- **Raw HTML runs.** A malicious `.md` inside a shared folder (a cloned repo, say) could read your
  other shared documents.
- **Copied fingerprint.** Someone who copies your router's IP and MAC address could pass as your
  trusted network.
- **No size limit.** There's no limit on file size.
- **No sign-in.** There are no auth tokens; anyone on a trusted network can open shared items
  and add notes to them.
- **Notes write to your folders.** Anyone who can add notes can create or append to
  `<doc>.notes.md` beside a shared document. Nothing else is written.

## Other behaviour worth knowing

- **AirDrop picker.** `airs send` waits while the picker is open. Cancelling prints `cancelled`.
  If AirDrop isn't available, the link is copied to the clipboard.
- **Broken text encoding.** Files with invalid UTF-8 still render.
- **Accented names.** URLs typed by hand with accented characters may not match. Links airs
  generates are fine.

## Testing

```
go test ./...          # includes an end-to-end test that builds and runs the binary
go test -short ./...   # skips it
go test -race ./...
```

- **End-to-end test.** It runs the real binary with a temporary HOME and its own port, then cleans
  up its launchd job.
- **Network detection.** `internal/netinfo` has a manual test (build tag `manual`) that reads the
  real router address. Run it as a compiled test binary rather than through `go test`, because a
  program started by `go test` can't read the ARP table:
  `go test -c -tags manual ./internal/netinfo && ./netinfo.test`.

### Manual checks

These need real devices and can't be automated:

- [ ] **AirDrop.** `airs send <name>` shows the AirDrop picker, and the iPad opens the link.
- [ ] **Trusted network.** On home Wi-Fi, run `airs trust home`, scan the QR code on the iPad,
  edit the file on the Mac, and watch the page update.
- [ ] **Untrusted network.** Join an iPhone hotspot. Within about 5s the iPad loses access,
  `airs list` shows `blocked`, and an open page keeps its content with a banner.
- [ ] **Notes by tap.** Tap a paragraph with a finger, then with the Pencil: a note box opens
  under it. Scrolling, following a link or selecting text doesn't open one.
- [ ] **Scribble.** Write in the note box with the Pencil; it turns into text. Save it and check
  `<doc>.notes.md` on the Mac.
- [ ] **Quote a selection.** Select text, tap **Add note**; the box shows the quote. iOS's own
  selection menu doesn't cover the button.
- [ ] **Edit on both sides.** Edit a note on the iPad; then edit the same note on the Mac while
  the iPad's box is open, and save on the iPad: it shows "Changed on the Mac" and keeps your text.
- [ ] **Offline notes.** Turn Wi-Fi off on the iPad with a note box open: the pill shows Offline,
  Save is disabled, the text stays. Turn it back on: Live, and Save works.
- [ ] **Remove with a page open.** Run `airs remove <name>` while the page is open on the iPad.
  The banner appears, and both downloads open offline.
