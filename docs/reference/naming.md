---
description: The naming rules systemd's report framework enforces for sources, families, fields and labels, and how the provider, scraper and bridges apply them.
---

# systemd naming rules

`systemd-report` enforces a few naming rules, and silently drops what breaks
them. sdmetrics applies the same rules everywhere, so what the exporter shows
is what systemd would accept.

## Sources

A source is an entry in the report directory that is a socket, or a symlink
to one, and whose file name is a valid **Varlink interface name**:

- dot-separated labels, at least one dot is customary but not required;
- each label made of letters, digits and single inner dashes;
- starting with a letter.

| Name | Valid |
| --- | --- |
| `io.systemd.Manager` | yes |
| `io.example.my-app` | yes |
| `io.example.Queue2` | yes |
| `2io.example` | no: starts with a digit |
| `io..example` | no: empty label |
| `io.example.my--app` | no: double dash |
| `io.example.Queue.` | no: trailing dot |
| `io.example_queue` | no: underscore |

`provider.New` returns an error for an invalid name. The scraper skips
entries with invalid names, entries that are not sockets, and symlinks that
don't point to a socket.

## Families

A family name is the **source name, a dot, and a Varlink field name**:

```text
io.example.Queue.JobsProcessed
```

A field name is a letter, then letters, digits and single underscores, not
ending in an underscore. systemd's own families use PascalCase.

| Field name | Valid |
| --- | --- |
| `NRestarts` | yes |
| `jobs_processed_total` | yes |
| `IPv4AddressState` | yes |
| `queue__depth` | no: double underscore |
| `depth_` | no: trailing underscore |
| `2xx` | no: starts with a digit |
| `queue-depth` | no: dash |

!!! danger "Families must be named after their source"

    `systemd-report` drops any family whose name does not start with the name
    of the socket it came from, followed by a dot. A source at
    `/run/systemd/report/io.example.Queue` may publish
    `io.example.Queue.Depth`, but not `io.example.Depth` or
    `io.example.QueueDepth`.

## Labels (fields)

Field keys in a value's `fields` object follow the field-name rules above.
The provider enforces them for `Labels`. Field **values** are strings; the
scraper keeps any non-string value as its JSON text.

## What each component does

| Component | Invalid source name | Invalid metric or label name | Family not named after its source |
| --- | --- | --- | --- |
| `provider.New` | returns an error | — | — |
| `Gauge`, `Counter`, `String`, `...Func`, `Labels` | — | **panics** at registration | impossible: the provider adds its name |
| `Collector` families | — | dropped, logged once | impossible: the provider adds its name |
| `prombridge`, `otelbridge` | — | **sanitized** (see below) | impossible |
| `scrape.Scraper` | entry skipped | value counted `Invalid` | value counted `Invalid`, logged once |

### Sanitizing

The bridges turn foreign names into valid field names:

| Rule | Example |
| --- | --- |
| characters other than letters and digits become `_` | `http.server.duration` → `http_server_duration` |
| runs of `_` collapse | `a__b` → `a_b` |
| leading and trailing `_` are dropped | `_x_` → `x` |
| a name starting with a digit gets an `m` prefix | `5xx` → `m5xx` |
| an empty result becomes `m` | `...` → `m` |

`otelbridge` also appends unit suffixes (`s` → `_seconds`, `ms` →
`_milliseconds`, `By` → `_bytes`).

## Recommendations

- Name the provider after your project in reverse-domain style, such as
  `com.example.Billing`. Avoid `io.systemd.*`, which is systemd's.
- Use PascalCase metric names, as systemd does: `JobsProcessed`,
  `QueueDepth`.
- Put the unit in the name: `LatencySeconds`, `CacheBytes`, `CPURatio`.
  The [OTel naming mode](mapping.md#naming-modes) turns it into a unit.
- End counters in `Total` only if you like the Prometheus look; the OTel
  naming mode drops it, and in that mode the Prometheus endpoint adds
  `_total` to every counter.
