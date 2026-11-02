# Windows

The toolkit runs on Windows through WSL2. This page is the parts that are
genuinely different, not a second copy of the documentation.

## Use WSL2, and keep files inside it

Install Docker Desktop with the WSL2 backend, then clone **into the Linux
filesystem**:

```bash
cd ~                                  # /home/you, NOT /mnt/c/...
git clone https://github.com/simtabi/dbpantry.git
```

This is the single most important thing on this page. A repository under
`/mnt/c/` is reached through a filesystem translation layer, and database
workloads are exactly the pattern it handles worst: builds take several times
longer, and file permissions do not behave, which breaks the secret files
(`chmod 0600` silently does nothing meaningful on a DrvFs mount).

Symptoms of getting this wrong: everything is slow, and `make init` produces
secrets the engines then refuse to read.

## Line endings

Git on Windows converts LF to CRLF by default. A shell script with CRLF endings
fails inside a Linux container with a message that names neither the file nor
the cause — usually `bad interpreter: /usr/bin/env bash^M: no such file`.

The repository ships `.gitattributes` and `.editorconfig` that prevent this. If
you cloned before that, or through a tool that ignores them:

```bash
git config core.autocrlf false
git rm --cached -r . && git reset --hard
```

## Give Docker enough memory

Docker Desktop's WSL2 backend takes what Windows gives it, which is often less
than a database wants. Create `%UserProfile%\.wslconfig`:

```ini
[wsl2]
memory=8GB
processors=4
```

Then `wsl --shutdown` and start Docker Desktop again.

8 GB is the number if you intend to run Cassandra, which alone wants about 2 GB.
For PostgreSQL and MySQL, 4 GB is fine.

## Connecting from Windows

Nothing is published by default (`MDB_PUBLISH=none`), which is usually what you
want — but a Windows-side GUI cannot join the Docker network, so set
`MDB_PUBLISH=direct` and connect to `localhost:54000` for PostgreSQL
(`MDB_PORT_BASE + 0`).

Ports published to `127.0.0.1` inside WSL2 are reachable from Windows at
`localhost`, so DBeaver, DataGrip, TablePlus and pgAdmin on the Windows side
need no configuration beyond the port.

If that stops working after a reboot, it is usually Docker Desktop starting
before WSL2 is ready. Restart Docker Desktop.

## Git Bash and PowerShell

`make` and the scripts here need a real bash. Git Bash is close but not close
enough — it lacks the process substitution and the docker socket behaviour
several scripts rely on. Run everything from a WSL2 shell.

There is no PowerShell path, and adding one is not planned: it would be a second
implementation of every script, drifting from the first.

## Performance expectations

Inside WSL2, on the Linux filesystem, performance is close to native Linux. If
yours is not, check in this order:

1. the repository is not under `/mnt/c/`
2. `.wslconfig` gives WSL2 enough memory
3. Windows Defender is not scanning the WSL2 virtual disk — exclude
   `%LocalAppData%\Docker\wsl` and your distribution's `ext4.vhdx`

That third one is frequently the whole problem.

---

[← Docs index](../../README.md#documentation)
