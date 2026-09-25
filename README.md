# sshut

A split-pane terminal file manager for transferring files between the local
machine and a remote host over SSH/SFTP.

## Requirements

- Go 1.26 or newer
- An OpenSSH client available as `ssh`
- A remote SSH server with the SFTP subsystem enabled
- Working non-interactive authentication (configured key and/or `ssh-agent`)

Run sshut without arguments to open the SSH destination prompt:

```sh
go run ./cmd/sshut
```

Alternatively, pass an OpenSSH destination directly:

```sh
go run ./cmd/sshut production
go run ./cmd/sshut user@example.com
```

Build a local binary with:

```sh
go build -o sshut ./cmd/sshut
```

sshut invokes the system `ssh` client with `BatchMode=yes`, so configured keys,
`ssh-agent`, `known_hosts`, and `ProxyJump` are used without storing credentials.
The host must already be trusted and the server must provide the SFTP subsystem.

Use `Tab` to switch panes, `↑`/`↓` or `j`/`k` to move, `Enter` to open a
directory, `Space` to select, `←` to download remote selections, and `→` to
upload local selections. `n` creates a directory, `r` renames, `d` deletes,
`g` goes to a path, and `F5` refreshes. `Ctrl+X` cancels the active transfer;
`q` quits.

Transfers are sequential and queued. Existing destinations open a conflict
prompt with `o` overwrite, `s` skip, `k` keep both, and `x` cancel. Capitalized
`O`, `S`, or `K` applies that decision to all remaining conflicts.

> **Status:** local/remote browsing, file operations, queued transfers,
> progress, cancellation, and conflict handling are implemented incrementally.
