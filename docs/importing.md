# Importing Email into Bongo-Go

The `bongo-import` tool migrates email from various sources into a running
bongo-go server via IMAP APPEND.

## Build

```bash
go build -o bongo-import ./cmd/bongo-import
```

## Common Flags

All subcommands share these flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--dst-host` | `localhost` | Destination IMAP hostname |
| `--dst-port` | `1143` | Destination IMAP port |
| `--dst-tls` | `false` | Use TLS for destination |
| `--dst-user` | (required) | Destination username |
| `--dst-pass` | (required) | Destination password |
| `--state` | | Path to JSON state file for resume |
| `--dry-run` | `false` | List what would be imported without writing |

The `--state` flag enables resumable imports. If the import is interrupted,
re-running with the same state file skips already-imported messages.

## Sources

### Thunderbird Local Folders

Auto-discovers Thunderbird's profile directory and imports all local folders,
preserving the folder hierarchy.

```bash
# Auto-discover (finds Thunderbird profile automatically)
./bongo-import thunderbird --dst-user admin@localhost --dst-pass admin

# Specify a profile directory
./bongo-import thunderbird --profile ~/Library/Thunderbird/Profiles/abc.default-release \
  --dst-user admin@localhost --dst-pass admin

# Specify the Local Folders directory directly
./bongo-import thunderbird ~/Library/Thunderbird/Profiles/abc.default-release/Mail/Local\ Folders \
  --dst-user admin@localhost --dst-pass admin
```

Profile auto-discovery looks in:
- **macOS:** `~/Library/Thunderbird/Profiles/`
- **Linux:** `~/.thunderbird/`
- **Windows:** `%APPDATA%\Thunderbird\Profiles\`

Thunderbird folder mapping:
- `Inbox` becomes `INBOX`
- `Sent`, `Drafts`, `Trash`, `Junk` keep their names
- `Archives` becomes `Archive`
- Nested folders (`.sbd` directories) are preserved as `Parent/Child`
- `.msf` summary files are skipped

### IMAP (Server-to-Server)

Import from another IMAP server. All folders are migrated.

```bash
./bongo-import imap \
  --src-host mail.example.com --src-port 993 --src-tls \
  --src-user user@example.com --src-pass oldpassword \
  --dst-user admin@localhost --dst-pass admin
```

### Mbox Files

Import one or more mbox files into a single folder.

```bash
# Import into INBOX (default)
./bongo-import mbox ~/mail/inbox.mbox --dst-user admin@localhost --dst-pass admin

# Import into a specific folder
./bongo-import mbox --folder "Archive/2024" ~/mail/archive.mbox \
  --dst-user admin@localhost --dst-pass admin

# Import multiple files
./bongo-import mbox ~/mail/*.mbox --dst-user admin@localhost --dst-pass admin
```

### Maildir

Import from a Maildir directory. Maildir++ subfolder conventions are
supported (e.g., `.Sent`, `.Drafts`).

```bash
./bongo-import maildir ~/Maildir --dst-user admin@localhost --dst-pass admin
```

### EML Files

Import individual `.eml` files.

```bash
./bongo-import eml --folder INBOX ~/exports/*.eml \
  --dst-user admin@localhost --dst-pass admin
```

## Resumable Imports

For large migrations, use the `--state` flag to track progress:

```bash
./bongo-import thunderbird --state ~/.bongo-import-state.json \
  --dst-user admin@localhost --dst-pass admin
```

If interrupted, re-run the same command. Already-imported messages are skipped.

## Dry Run

Preview what would be imported without writing anything:

```bash
./bongo-import thunderbird --dry-run --dst-user admin@localhost --dst-pass admin
```
