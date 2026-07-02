# 07 - Rails assets: per-release vs shared

Two strategies for where compiled assets live across releases, shipped as two stages of one app so you can compare
them. **Pick one per app** - they're mutually exclusive.

## Files

```
Deployfile.yml                       # shared base (app, bundle/migrate/restart, restart hook)
deploy/per-release.yml               # Variant A - assets compiled into each release (recommended)
deploy/shared.yml                    # Variant B - public/assets shared across releases
deploy/scripts/backup-manifest.sh    # (shared only) save the manifest into each release on deploy
deploy/scripts/restore-manifest.sh   # (shared only) restore it after a rollback
```

## The decision in one paragraph

Rails compiles `application.js` -> `application-<digest>.js` and writes a **manifest** mapping logical names ->
digests. The asset helpers read it at runtime.
The only real question is whether `public/assets` is **shared across releases** (a `linked_dir`) or **lives inside
each release**.
Sharing keeps old digests around so a stale browser tab can still fetch them after a deploy - but it means one mutable
manifest gets overwritten every deploy, which breaks rollback unless you back the manifest up and restore it.
Not sharing makes rollback automatic and correct, at the cost of that old-digest-survival window.

## Variant A - per-release (recommended default)

```sh
whoosh per-release deploy
whoosh per-release deploy:rollback     # assets + manifest restored automatically
```

`public/assets` is **not** a `linked_dir`, so `assets:precompile` writes into the release's own dir.
The atomic `current` swap means a rollback restores the previous release's assets *and* manifest for free - nothing to
back up.

- **Pro:** simplest, rollback Just Works, release is fully self-contained.
- **Con:** at the instant `current` flips, only the new digests exist under `current/public/assets`.
  A browser holding the old page can 404 on old digests during the swap window.
  Fix at the CDN/`asset_host` layer (which retains objects), not by sharing the dir.
- **Don't** add `assets:clean[N]` here - it prunes old digests from a *shared* dir, and in a per-release dir there's
  nothing old to clean (no-op). `keep_releases` prunes old assets when it drops the release.

## Variant B - shared `public/assets`

```sh
whoosh shared deploy
whoosh shared deploy:rollback          # manifest restored automatically (hook)
```

`public/assets` is a `linked_dir`, so every release points at one shared dir.
Old digests accumulate and survive deploys, and `assets:clean[5]` prunes them.

The single shared manifest is overwritten by every precompile, so:

1. **On deploy**, `backup-manifest.sh` (run as part of the `assets` task) copies the fresh manifest into
   `<release>/assets_manifest_backup/` - immutable and per-release.
2. **On rollback**, the matching manifest is put back automatically: the `restore-manifest` task is wired to the
   `deploy:rollback` **after-hook**, which runs once `current` has been repointed at the previous release.
   So a plain `whoosh shared deploy:rollback` is all you need.
   (`restore-manifest` works because hook tasks run inside `current`, now the rolled-back release.
   It's marked `hidden: true` so it stays out of the CLI listing, but is still runnable by hand if ever needed.)

- **Pro:** old fingerprinted assets survive a deploy (no 404 window for stale tabs).
- **Con:** more moving parts - backup on deploy, restore on rollback, plus `assets:clean` to bound the shared dir.

> whoosh fires `before`/`after` hooks around `deploy:rollback`, and `after` tasks see `current` already pointing at
> the restored release - so the manifest restore happens at exactly the right moment.

## Why the manifest backup exists

The manifest backup/restore mechanism exists **only** to fix rollback when `public/assets` is shared: the single
shared manifest is overwritten every deploy, so a rollback must put the matching one back.
If you don't share the dir (Variant A), the whole mechanism is unnecessary.
Both variants are valid - per-release is simpler and is the better default unless you specifically need old digests to
outlive a deploy.

## See also

`02-rails-multistage` uses the per-release model (Variant A) in a fuller multi-stage, multi-role deploy.
