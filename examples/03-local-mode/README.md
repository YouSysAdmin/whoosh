# 03 - local mode (no SSH)

Run the complete release lifecycle on your own machine.
A `local: true` server uses `/bin/sh` instead of an SSH connection.
Everything else (timestamped releases, shared symlinks, `current` swap, rollback) is identical.

This example is runnable as-is: it clones a small public repo into `/tmp`.

## Files

```
Deployfile.yml      # app points at a public repo and /tmp/localapp
deploy/local.yml    # one server with local: true
```

## Run for real

```sh
whoosh local deploy --dry-run   # see the plan
whoosh local deploy             # clone + release + swap current, locally
whoosh local releases           # list releases
ls -l /tmp/localapp               # current -> releases/<timestamp>
whoosh local deploy:rollback    # (after a second deploy) go back one
```

The `show` task is wired to run after `deploy:finishing`, printing the release path, where `current` points, and the
deployed revision - handy for seeing the layout that gets built.

## When to use local mode

- Kicking the tires without any servers.
- A build-and-release step inside CI on the runner itself.
- Mixed inventories: a `local: true` server and SSH servers can coexist in the same stage and run the same tasks.
