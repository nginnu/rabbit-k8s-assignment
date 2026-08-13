# manifests

The number prefix is apply order.

| | |
|---|---|
| `01` | namespaces — `web`, `api`, `data`; everything else lands in one |
| `02` | certificates — the Secret the gateway reads, issued into `traefik` |
| `03-gateway.yaml.istio-old` | the old Istio Gateway — not applied, kept to diff against Traefik's |
| `04` | routes — attach to the Gateway that ships with the Traefik release |
| `05` | MariaDB |
| `06` | Redis |
| `07` | observability namespace + the one Service upstream charts don't create |
| `08` | Grafana route |
| `09` | Argo CD route |
| `10` | Argo CD Applications — the bootstrap seam, `dummy` only |
| `11` | NetworkPolicy — data tier |
| `12` | NetworkPolicy — observability tier |

`03` has no live successor file: the Gateway object itself now comes from the
Traefik Helm release (`make traefik`), not from a manifest applied here. The
old file is kept, unnumbered into the sequence, so it can be diffed against
the new setup — see `CLAUDE.md` before deleting it.

Adding something in the middle means renumbering the files after it. That is a
`git mv` and a line in the Makefile, and it keeps the order readable.

The Makefile applies these through its targets, so the numbers describe the
order rather than enforce it.
