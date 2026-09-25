# sshut

A split-pane terminal file manager for transferring files between the local
machine and a remote host over SSH/SFTP.

Run sshut without arguments to open the SSH destination prompt:

```sh
go run ./cmd/sshut
```

Alternatively, pass an OpenSSH destination directly:

```sh
go run ./cmd/sshut production
go run ./cmd/sshut user@example.com
```

sshut invokes the system `ssh` client with `BatchMode=yes`, so configured keys,
`ssh-agent`, `known_hosts`, and `ProxyJump` are used without storing credentials.
The host must already be trusted and the server must provide the SFTP subsystem.

Use `Tab` to switch panes, `↑`/`↓` or `j`/`k` to move, `Enter` to open a
directory, `Space` to select, `F5` to refresh, and `q` to quit.

> **Status:** remote browsing is implemented. File mutations and transfers are
> being added in subsequent commits.
