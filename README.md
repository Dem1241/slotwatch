<div align="center">

# ≋ slotwatch

**watch your postgres replication slots in the terminal.**<br>
catch the one quietly filling your disk with WAL before it takes the database down.

[![ci](https://img.shields.io/github/actions/workflow/status/Dem1241/slotwatch/ci.yml?branch=main&style=flat-square&label=ci)](https://github.com/Dem1241/slotwatch/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev)
[![PostgreSQL](https://img.shields.io/badge/postgres-14%20to%2018-313244?style=flat-square&logo=postgresql&logoColor=f5c2e7)](#what-it-understands)
[![License](https://img.shields.io/badge/license-MIT-f5c2e7?style=flat-square)](LICENSE)

<img src="assets/demo.gif" alt="slotwatch running its demo, showing five replication slots, with a debezium slot that nobody reads holding 6.7 GB of WAL, growing 43 MB a minute and about five hours away from max_slot_wal_keep_size" width="860">

</div>

## why

a replication slot makes postgres keep every bit of WAL its consumer hasn't
confirmed yet. that's the whole point, until the consumer stops. a debezium
connector crashes on friday, a standby gets deleted, a test subscription is
forgotten, and the slot keeps holding on to WAL. nothing errors. the disk
just fills up, and a few days later the database stops accepting writes.

slotwatch shows every slot with how much WAL it holds, how fast that grows,
who is reading it and how long it has been idle. the worst ones go to the
top, the detail pane says why a slot was flagged, and it suggests what to do
about it. it only reads, so it never drops or changes anything for you.

it goes well with [connectwatch](https://github.com/Dem1241/connectwatch), for
the debezium connector that stopped reading the slot, and
[puddle](https://github.com/Dem1241/puddle), for the change events it writes.

## try it

```sh
go install github.com/Dem1241/slotwatch@latest
slotwatch --demo
```

or grab a binary for linux, macos or windows from the
[releases](https://github.com/Dem1241/slotwatch/releases).

`--demo` watches a pretend server with a debezium slot that stopped being
read hours ago and keeps piling up WAL, next to a few healthy ones, so you
can look around without a database.

## use it

```sh
slotwatch                                    # PGHOST, PGUSER and friends, like psql
slotwatch -d postgres://me@db:5432/shop      # or set DATABASE_URL
slotwatch --warn 2GB --danger 20GB           # when to start worrying
slotwatch --inactive 30m                     # how long a slot may sit unused
```

| key | does |
|:--|:--|
| `↑` `↓` or `j` `k` | move through the slots |
| `s` | sort by health, retained WAL or name |
| `space` | pause or resume refreshing |
| `r` | refresh now |
| `ctrl+d` `ctrl+u` | scroll the detail pane |
| `/` | filter by name, plugin, database or client |
| `esc` | clear the filter |
| `q` | quit |

### in cron, ci or kubernetes

```sh
slotwatch --check            # a table, and an exit code
slotwatch --check --json     # the same for scripts
```

`--check` looks at the slots once and exits with `0` when everything is fine,
`1` when something needs watching, `2` when something is in danger and `3`
when it couldn't check at all, like the usual monitoring plugins expect.

```text
LEVEL   SLOT             TYPE              STATE        RETAINED  WAL STATUS
danger  analytics_test   logical/wal2json  lost         -         lost
warn    debezium_shop    logical/pgoutput  idle 2h 41m  6.5 GB    extended
ok      airbyte_orders   logical/pgoutput  active       38.0 MB   reserved
```

## what it flags

- slots holding more WAL than `--warn` or `--danger` (1GB and 10GB by default)
- slots nobody has used for longer than `--inactive` (10 minutes by default)
- `wal_status` of `extended`, `unreserved` or `lost`, and slots the server
  invalidated
- slots that will reach `max_slot_wal_keep_size` within the hour at the rate
  they grow
- servers where `max_slot_wal_keep_size` is unlimited, so a stuck slot grows
  until the disk is full

## what it understands

- logical and physical slots, temporary ones too, on primaries and standbys
- postgres 14 to 18 are tested. 12 and 13 should work too, without the
  columns they don't have yet
- on postgres 17 and newer, how long a slot has been idle comes from the
  server (`inactive_since`). on older ones slotwatch counts from when it
  first saw the slot idle
- who holds a slot, from `pg_stat_replication`

any role that can log in will do, no special grants needed. without
`pg_read_all_stats` (part of `pg_monitor`) postgres hides the address of a
client, but its application name still shows.

## how it works

slotwatch reads `pg_replication_slots` with [pgx](https://github.com/jackc/pgx)
every few seconds (`--interval`) and works out how much WAL each slot holds
from its `restart_lsn`. it keeps a few minutes of history per slot for the
growth rate and the sparklines. the interface is
[bubble tea](https://github.com/charmbracelet/bubbletea).

```sh
go test ./...
SLOTWATCH_TEST_DSN=postgres://localhost/postgres go test ./internal/pg  # against a real server with wal_level=logical
golangci-lint run
```

## license

[MIT](LICENSE)
