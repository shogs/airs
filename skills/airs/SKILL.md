---
name: airs
description: Share a markdown document to the user's iPad with airs so they can read it and leave notes, then read those notes back. Use whenever you write, or point the user to, a markdown document meant for them to read or review — plans (including plan-mode plan files), specs, designs, reports, write-ups. Not for files only you read, and not for code.
---

# Sharing documents for review with airs

`airs` serves markdown to the user's iPad over the local network. The user reads it there
and adds notes, which airs saves to a `.notes.md` file. When they tell you they're done, you
read the notes and act on them.

## When

Every time you write or point the user to a markdown document for them to read or review:
plans (plan-mode plan files too), specs, designs, reports, write-ups. Not for code, and not
for files only you read.

If `airs` isn't installed (`command -v airs` finds nothing), skip this skill without
mentioning it.

## Share it

1. Run airs **from the project root** (the git top level, else your working directory). airs
   names the project after the folder it runs in, and the link starts with that name.
2. Is the document inside the project? Resolve symlinks on both paths, then check the
   document is under the project root.
   - Inside:
     ```sh
     cd <project root> && airs add <doc> --json --qr
     ```
   - Outside, for example a plan in `~/.claude/plans/`: keep its notes in the project.
     ```sh
     cd <project root> && airs add <doc> --json --qr --notes-dir <project root>/.claude/notes
     ```
3. Pass `<doc>` as an absolute path. The output is one JSON object; use `url`, `qr` and
   `status`. If it's `{"error": "…"}` instead, tell the user what it says and carry on
   without the share.
4. Reply with the link first. Then, if your interface shows monospace text (Claude Code
   does), copy the `qr` field into a fenced code block under the link: every character
   exactly as it is, with each `\n` as a line break.
   The user scans it with the iPad camera. Leave the QR code out where it can't be shown.
5. If `status` is `"blocked"`, the network isn't trusted, so the iPad can't open the link.
   Say so, and mention that `airs trust` on their home network fixes it. Don't run
   `airs trust` or `--any` yourself unless they ask.
6. Tell the user to add notes on the iPad and to tell you when they're done. Don't poll or
   wait in a loop. Carry on with anything that doesn't depend on their review.

Edits you make to a shared document reload on the iPad by themselves; don't share it again.

## When the user says they're done

1. Read the notes, with the same absolute path you shared:
   ```sh
   airs notes <absolute path of doc> --json
   ```
   You get `{"path", "doc", "notes": [...]}`. Each note has `section` (the heading it's
   under), `quote` (text the user selected, may be empty) and `text` (what they wrote).
   An `{"error": …}` result means the document isn't shared (any more); say so.
2. Work through every note: change the document, answer the question, or ask if it's
   unclear. Say which note each change came from.
3. Leave the share running and the notes file alone. The user may read again, and the
   notes are their record. Edit or delete notes only when asked.

## Plan mode

Share the plan file once it's written and before asking for approval, if running commands
is allowed then. Otherwise share it right after.
