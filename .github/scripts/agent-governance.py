#!/usr/bin/env python3
"""Enforce .github/agent-policy.yaml against the tracked tree.

Stands in for GitLab Duo Agent Platform / Tool Governance and for the LLM
guardrail libraries, none of which have a GitHub-native equivalent. What it does
is real: it reads the policy, walks the files git tracks, and reports where each
governed tool is actually used.

  deny matched  -> exit 1
  ask matched   -> exit 0, annotated, a human decides
  allow matched -> exit 0

Usage: agent-governance.py [--policy PATH] [--root PATH] [--json OUT]
"""

import argparse
import fnmatch
import json
import os
import re
import subprocess
import sys

DECISIONS = ("allow", "ask", "deny")

# Binaries this repo is expected to drive. Anything here that the tree invokes
# without a rule in the policy falls to unlisted_default — the check that keeps
# the policy honest as the repo grows.
GOVERNED_BINARIES = (
    "kubectl", "helm", "terraform", "docker", "gcloud",
    "argocd", "kind", "istioctl", "curl", "wget",
)

# Reading the tree from git rather than walking it: .git, node_modules and any
# build output are excluded for free, and a file nobody committed cannot fail
# somebody else's build.
def tracked_files(root):
    out = subprocess.run(
        ["git", "-C", root, "ls-files", "-z"],
        capture_output=True, text=True, check=True,
    ).stdout
    return [f for f in out.split("\0") if f]


def load_policy(path):
    try:
        import yaml
    except ImportError:
        sys.exit("PyYAML is required: pip install pyyaml")
    with open(path) as fh:
        policy = yaml.safe_load(fh)

    errors = []
    if policy.get("version") != 1:
        errors.append("version must be 1")
    if policy.get("unlisted_default") not in DECISIONS:
        errors.append(f"unlisted_default must be one of {DECISIONS}")
    for section in ("tools", "guardrails"):
        for i, rule in enumerate(policy.get(section) or []):
            where = f"{section}[{i}]"
            if not rule.get("name"):
                errors.append(f"{where}: no name")
            if rule.get("decision") not in DECISIONS:
                errors.append(f"{where}: decision must be one of {DECISIONS}")
            if not rule.get("match"):
                errors.append(f"{where}: no match patterns")
            for pat in rule.get("match") or []:
                try:
                    re.compile(pat)
                except re.error as exc:
                    errors.append(f"{where}: bad regex {pat!r}: {exc}")
    # A malformed policy is not a passing run. Reporting "0 violations" from a
    # file that never compiled is the failure mode this whole job exists to
    # avoid.
    if errors:
        for e in errors:
            print(f"policy error: {e}", file=sys.stderr)
        sys.exit(2)
    return policy


def scan(root, files, patterns, skip):
    hits = []
    compiled = [(p, re.compile(p)) for p in patterns]
    for rel in files:
        if rel in skip:
            continue
        path = os.path.join(root, rel)
        try:
            with open(path, encoding="utf-8", errors="ignore") as fh:
                for lineno, line in enumerate(fh, 1):
                    for raw, rx in compiled:
                        if rx.search(line):
                            hits.append({
                                "file": rel,
                                "line": lineno,
                                "pattern": raw,
                                "text": line.strip()[:160],
                            })
        except (IsADirectoryError, FileNotFoundError, PermissionError):
            continue
    return hits


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--policy", default=".github/agent-policy.yaml")
    ap.add_argument("--root", default=".")
    ap.add_argument("--json", dest="json_out")
    args = ap.parse_args()

    root = os.path.abspath(args.root)
    policy = load_policy(os.path.join(root, args.policy))
    files = tracked_files(root)

    # The policy file quotes every pattern it forbids, so scanning it finds each
    # of them and every run fails on its own rulebook.
    skip = {args.policy, ".github/scripts/agent-governance.py"}
    for pattern in policy.get("scan_exclude") or []:
        skip.update(f for f in files if fnmatch.fnmatch(f, pattern))

    results = []
    for section in ("tools", "guardrails"):
        for rule in policy.get(section) or []:
            hits = scan(root, files, rule["match"], skip)
            results.append({
                "section": section,
                "name": rule["name"],
                "decision": rule["decision"],
                "reason": rule.get("reason", ""),
                "hits": hits,
            })

    named = {p for r in (policy.get("tools") or []) for p in r["match"]}
    unlisted = []
    for binary in GOVERNED_BINARIES:
        if any(binary in p for p in named):
            continue
        hits = scan(root, files, [rf"(?<![\w/-]){re.escape(binary)}\s"], skip)
        if hits:
            unlisted.append({
                "section": "tools",
                "name": f"{binary} (unlisted)",
                "decision": policy["unlisted_default"],
                "reason": "invoked by the repo but not named in the policy",
                "hits": hits,
            })
    results.extend(unlisted)

    width = max(len(r["name"]) for r in results) if results else 10
    lines = []
    denied = 0
    asked = 0
    for r in results:
        n = len(r["hits"])
        mark = {"allow": "ok  ", "ask": "ASK ", "deny": "DENY"}[r["decision"]]
        state = f"{n} use(s)" if n else "unused"
        lines.append(f"{mark} {r['name']:<{width}}  {r['decision']:<5} {state}")
        if n and r["decision"] == "deny":
            denied += n
        if n and r["decision"] == "ask":
            asked += n

    print("\n".join(lines))
    print(f"\n{len(results)} rules · {denied} denied use(s) · {asked} awaiting sign-off")

    for r in results:
        if r["decision"] == "deny" and r["hits"]:
            for h in r["hits"]:
                print(f"::error file={h['file']},line={h['line']}::"
                      f"{r['name']} is denied — {r['reason']}")
        if r["decision"] == "ask" and r["hits"]:
            for h in r["hits"]:
                print(f"::warning file={h['file']},line={h['line']}::"
                      f"{r['name']} needs sign-off — {r['reason']}")

    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a") as fh:
            fh.write("## Agent tool governance\n\n")
            fh.write("| rule | decision | uses found |\n|---|---|---|\n")
            for r in results:
                fh.write(f"| {r['name']} | {r['decision']} | {len(r['hits'])} |\n")

    if args.json_out:
        with open(args.json_out, "w") as fh:
            json.dump(results, fh, indent=2)

    return 1 if denied else 0


if __name__ == "__main__":
    sys.exit(main())
