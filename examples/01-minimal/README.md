# 01 - minimal

The smallest configuration that still does something useful: deploy one app to a couple of SSH hosts using the core
release lifecycle. No tasks, no hooks.

## Files

```
Deployfile.yml        # app, repo, deploy_to, linked files/dirs, SSH defaults
deploy/production.yml # the hosts for the `production` stage
```

## Run

```sh
whoosh production config            # resolved config
whoosh production deploy --dry-run  # preview - no host is contacted
whoosh production deploy            # build a release and swap `current`
whoosh production releases          # list releases on each host
whoosh production deploy:rollback   # go back to the previous release
```

## What you get on each host

```
/var/www/hello-svc/
+-- current -> releases/20230624120000   # atomically swapped symlink
+-- releases/
|   +-- 20230624120000/                  # timestamped checkout (+ REVISION files)
|   +-- ...                              # last `keep_releases` kept
+-- repo/                                # git mirror cache
+-- shared/                              # .env, log/, tmp/ - linked into each release
```

## Next

Add tasks and wire them into the lifecycle - see [`02-rails-multistage`](../02-rails-multistage/).
