# The embedded kit

`make kit` fills this directory with the kit's tracked files and a `MANIFEST`
(mode, SHA-256 and path of each), and the binary embeds it (`internal/kit`,
design.md D-85). Everything here but this file is generated and gitignored:
edit the files at the repository root, never these copies.
