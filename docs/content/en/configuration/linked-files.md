---
title: "Linked files & dirs"
description: "Files and directories kept in shared/ and symlinked into every release so config and state persist across deploys."
weight: 30
---

`linked_files` and `linked_dirs` are paths kept in `<deploy_to>/shared/` and symlinked into every release, so
configuration and state persist across deploys.

```yaml
linked_files:
  - config/database.yml
  - config/master.key
  - .env
linked_dirs:
  - log
  - tmp/pids
  - storage
```

On each deploy, `deploy:check` prepares `shared/` and the two kinds differ:

- **`linked_dirs`** are app-writable (logs, uploads, caches), so whoosh **creates** them in `shared/` if missing.
- **`linked_files`** are operator-provided config (`database.yml`, `.env`, ...), so whoosh **does not** create them -
  it **verifies** each one exists in `shared/` and fails `deploy:check` with a clear message if one is missing.
  (Symlinking a missing file would otherwise produce a dangling link and break the app at runtime.)

So put the real config/secret files in `shared/` on the host **once before the first deploy**, and every release then
sees them via the symlink. `whoosh <stage> deploy:check` (or `deploy`) tells you up front if any are absent.

## Rewriting the destination

By default, an entry uses the same path on both sides: `config/database.yml` links `shared/config/database.yml` into
each release at `config/database.yml`.

To link a shared file/dir to a **different** path inside the release, write `source:dest`:

```yaml
linked_files:
  - config/database.yml:config/new-database.yml   # shared/config/database.yml -> <release>/config/new-database.yml
linked_dirs:
  - shared-uploads:public/uploads                 # shared/shared-uploads     -> <release>/public/uploads
```

The part before the colon is the path under `shared/` (the source that `deploy:check` verifies / creates), the part
after is where the symlink is created in the release.
The colon is optional - omit it for the common same-path case.
Split is on the **first** colon and there must be **no space after it** (otherwise YAML reads the entry as a mapping),
so paths themselves must not contain a colon.

{{< callout type="tip" title="Sharing public/assets has a rollback caveat" >}}
Sharing `public/assets` is a common Rails choice with a rollback caveat - see
[`examples/07-rails-assets`](https://github.com/YouSysAdmin/whoosh/tree/master/examples/07-rails-assets) for per-release
vs shared assets.
{{< /callout >}}
