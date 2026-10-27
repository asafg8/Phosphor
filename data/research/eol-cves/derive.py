#!/usr/bin/env python3
"""Re-derive the unpatched-ID sets in data/eol-cves.lock (method is in the lock's "method" field).

Inputs (this dir): cl8.html, cl7.html (php.net ChangeLog-8/-7 pages), ghsa.tsv (php-src security advisories:
ghsa, cve, published_at, ...), and the vendor changelogs named in BUILDS. Fetch the php.net pages with
  curl -sL https://www.php.net/ChangeLog-8.php -o cl8.html ; curl -sL https://www.php.net/ChangeLog-7.php -o cl7.html
Prints one line per build: id, count. Compare with data/eol-cves.lock.
"""
import re

PHP2SHELL = {"CVE-2026-109618", "CVE-2026-109619"}
BASE_DATES = {"7.4.33": "2022-11-03", "8.0.30": "2023-08-03", "8.1.34": "2025-12-18"}
# id, base, vendor changelog (None = upstream), drop rpm changelog entries, minus php2shell
BUILDS = [
    ("upstream:7.4.33", "7.4.33", None, (), False),
    ("phosphor:7.4.33-phosphor1", "7.4.33", None, (), True),
    ("ondrej:7.4.33-31", "7.4.33", "ondrej-7.4.33-31.changelog", (), False),
    ("debian:7.4.33-1+deb11u12", "7.4.33", "debian-7.4.33-1+deb11u12.changelog", (), False),
    ("phosphor:7.4.33-1+deb11u12+phosphor1", "7.4.33", "debian-7.4.33-1+deb11u12.changelog", (), True),
    ("almalinux8:7.4.33-8", "7.4.33", "alma8-7.4.33-8.changelog", (), False),
    ("almalinux8:7.4.33-6", "7.4.33", "alma8-7.4.33-8.changelog", ("7.4.33-8", "7.4.33-7"), False),
    ("phosphor:7.4.33-6.phosphor1 (alma8)", "7.4.33", "alma8-7.4.33-8.changelog", ("7.4.33-8", "7.4.33-7"), True),
    ("upstream:8.0.30", "8.0.30", None, (), False),
    ("ondrej:8.0.30-25", "8.0.30", "ondrej-8.0.30-25.changelog", (), False),
    ("upstream:8.1.34", "8.1.34", None, (), False),
    ("ondrej:8.1.34-9", "8.1.34", "ondrej-8.1.34-9.changelog", (), False),
]


def sections(path):
    t = open(path).read()
    out = []
    for m in re.finditer(r'<section class="version" id="(\d+\.\d+\.\d+)">(.*?)</section>', t, re.S):
        d = re.search(r"datetime=['\"](\d{4}-\d{2}-\d{2})", m.group(2))
        out.append((m.group(1), d.group(1) if d else None, set(re.findall(r"CVE-\d{4}-\d{4,}", m.group(2)))))
    return out


def vt(v):
    return tuple(int(x) for x in v.split("."))


s7, s8 = sections("cl7.html"), sections("cl8.html")
gh = [l.rstrip("\n").split("\t") for l in open("ghsa.tsv")]
g2c = {g[0]: g[1] for g in gh if g[1]}


def upstream(base):
    date = BASE_DATES[base]
    ids = set().union(*[c for v, d, c in s8 if d and d > date])
    ids |= {g[1] or g[0] for g in gh if g[2][:10] > date}
    branch = base.rsplit(".", 1)[0] + "."
    already = set().union(*[c for v, d, c in (s7 if base.startswith("7.") else s8)
                            if v.startswith(branch) and vt(v) <= vt(base)])
    return {i for i in ids if i not in already and not (i.startswith("CVE-") and int(i[4:8]) < 2022)}


def vendor(path, drop):
    txt = open(path).read()
    if drop:
        parts = re.split(r"(?m)^(?=\* )", txt)
        txt = "".join(p for p in parts if not any(x in p.split("\n")[0] for x in drop))
    ids = set(re.findall(r"CVE-\d{4}-\d{4,}", txt))
    for g in re.findall(r"(?i)GHSA-[a-z0-9]{4}-[a-z0-9]{4}-[a-z0-9]{4}", txt):
        g = "GHSA" + g[4:].lower()
        ids.add(g2c.get(g, g))
    return ids


for bid, base, cl, drop, minus in BUILDS:
    ids = upstream(base)
    if cl:
        ids -= vendor(cl, drop)
    if minus:
        ids -= PHP2SHELL
    print(f"{bid:32} {len(ids)}")
