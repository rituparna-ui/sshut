# sshut

A split-pane terminal file manager for transferring files between the local
machine and a remote host over SSH/SFTP.

The first incremental build provides a runnable local browser. Run it with:

```sh
go run ./cmd/sshut
```

Use `↑`/`↓` or `j`/`k` to move, `Enter` to open a directory, `Space` to select,
`F5` to refresh, and `q` to quit.

> **Status:** early development. The remote pane and transfers are being added
> in subsequent commits.
