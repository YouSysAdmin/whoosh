# Whoosh print-hosts-table Plugin

Prints the stage's resolved host inventory as a bordered table - address, roles, deploy flag, transport, and source
(Deployfile or the discovering plugin, e.g. `aws:ec2:inventory`).

The plugin is **bundled and on by default**, it contributes two things, both rendering the same table:

- an automatic print at the start of every deploy (before `deploy:starting`),
- the `whoosh <stage> deploy:hosts` CLI command, for printing the table on demand.

```
+-------------------+-------+--------+-----------+-------------------+
| HOST              | ROLES | DEPLOY | TRANSPORT | SOURCE            |
+-------------------+-------+--------+-----------+-------------------+
| web1.example.com  | app   | yes    | ssh       | config            |
| 10.0.1.17         | app   | yes    | ssh       | aws:ec2:inventory |
+-------------------+-------+--------+-----------+-------------------+
```

With `--log-format json` (or `log.format: json`) the hosts are emitted as a structured slog record instead, keeping
the log stream valid JSON.

Disable it per stage like any default-on plugin:

```yaml
plugins:
  - name: print-hosts-table
    enabled: false
```
