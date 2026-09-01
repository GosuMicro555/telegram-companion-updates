# Restore a Verified Scout Backup

Scout backups contain the SQLite application database and configured gotd
session/peer files. Archives are chunk-encrypted with XChaCha20-Poly1305 and
are useful only with the recovery key held by the application's secret store.

## Routine schedule and retention

- The next daily run is 03:00 in the machine's local timezone.
- A monthly archive is created after the first successful daily backup in a
  month. A failed monthly attempt is retried after the next successful daily.
- Rotation occurs only after a new archive has been decrypted and verified.
  The application retains 14 verified daily and 3 verified monthly archives.
- SQLite schema upgrades must run through `BackupService.RunMigration`. The
  migration callback is not called unless its pre-migration backup reports a
  verified status.

## Export the recovery key

Export is never automatic. In the desktop recovery action, select an absolute
path on encrypted removable storage and invoke **Export backup recovery key**.
The application creates that exact path as a new owner-only (`0600`) file and
refuses to overwrite an existing path or follow a symlink. It never writes the
key to application logs.

Store the recovery-key file separately from backup archives. Do not paste its
contents into a terminal, issue tracker, chat, or diagnostic bundle.

## Restore procedure

1. Stop Scout automation and any process that could open restored gotd session
   files.
2. In the desktop restore action, choose the encrypted archive and a new,
   absolute destination path. The destination must not exist and must not be
   the live database path. Do not choose a path below a symlinked directory.
3. Start **Restore backup**. The application decrypts into a private temporary
   directory, rejects absolute/traversing/duplicate/symlink archive entries,
   validates every manifest size and SHA-256, runs `PRAGMA integrity_check`,
   and compares the archived SQLite schema with the live compatible schema.
4. The destination appears only after every check succeeds. A failed restore
   removes temporary plaintext and leaves both the live database and any
   existing destination untouched.
5. Inspect `database.sqlite`, `manifest.json`, and the `sessions/` directory in
   the new destination. Keep the current live data unchanged until application
   startup and account validation succeed against the restored copy.

## Promoting restored data

Promotion is an explicit maintenance operation outside the running desktop
process. Stop the application, preserve the current live directory under a new
owner-only path, and move the already verified restore into the configured
data location using a same-filesystem atomic rename. Never copy a restored
database over an open live database and never point restore directly at the
live destination.

The restored archive names its SQLite payload `database.sqlite`. After stopping
the desktop and preserving the current data directory, promote that verified
file to `data/app.db` with a same-filesystem atomic rename. Restore the verified
session files to their configured owner-only paths, then start the application
and confirm account validation before deleting the preserved directory. Do not
promote `application-state.bolt`: that name is legacy input only, and Task 12
stores all live application state in SQLite `data/app.db`.

If the recovery key is unavailable, do not alter the archive. Restore cannot
bypass authenticated decryption; recover the separately exported key through
the application's approved secret-store recovery procedure.
