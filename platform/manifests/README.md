# manifests

The number prefix is apply order.

| | |
|---|---|
| `01` | namespaces — everything else lands in one |
| `02` | certificates — the Secret the gateway reads |
| `03` | gateway — listeners on :80 and :443 |
| `04` | routes — attach to the gateway above |

Adding something in the middle means renumbering the files after it. That is a
`git mv` and a line in the Makefile, and it keeps the order readable.

The Makefile applies these through its targets, so the numbers describe the
order rather than enforce it.
