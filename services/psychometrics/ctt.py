"""Classical test theory for one exam version (docs/09 phase 6).

Pure functions over per-item scores, no database. Only CTT: difficulty
(p-value), corrected point-biserial discrimination, distractor analysis and
KR-20. No calibration claims beyond these (docs/04 risk #2).
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field

# Below this many fully scored attempts the numbers swing too much to act on.
MIN_N = 30
# Upper and lower groups for distractor analysis (Kelley's 27%).
GROUP = 0.27


@dataclass
class Item:
    id: str
    number: int
    kind: str  # "choice", "short" or "essay"
    max_points: int
    choices: list[str] = field(default_factory=list)
    key: list[str] = field(default_factory=list)


@dataclass
class Score:
    attempt: str
    item: str
    points: int | None
    max_points: int
    picked: list[str]


def _mean(xs: list[float]) -> float:
    return sum(xs) / len(xs) if xs else 0.0


def _corr(xs: list[float], ys: list[float]) -> float | None:
    """Pearson's r, or None when either side does not vary."""
    n = len(xs)
    if n < 2:
        return None
    mx, my = _mean(xs), _mean(ys)
    sxy = sum((x - mx) * (y - my) for x, y in zip(xs, ys))
    sxx = sum((x - mx) ** 2 for x in xs)
    syy = sum((y - my) ** 2 for y in ys)
    if sxx == 0 or syy == 0:
        return None
    return sxy / math.sqrt(sxx * syy)


def _r(x: float) -> float:
    return round(x, 3)


def letter(i: int) -> str:
    return "ABCDEFGHIJKLMNOPQRSTUVWXYZ"[i] if i < 26 else str(i + 1)


def analyze(items: list[Item], scores: list[Score], exam_version: int) -> dict:
    """The JSON form of GetItemAnalysisResponse (proto field names, camelCase)."""
    by_attempt: dict[str, dict[str, Score]] = {}
    for s in scores:
        by_attempt.setdefault(s.attempt, {})[s.item] = s
    ids = [it.id for it in items]
    # Only attempts with every item scored: a total with an essay still
    # waiting is not comparable with one without.
    full = {a: row for a, row in by_attempt.items() if all(i in row and row[i].points is not None for i in ids)}
    attempts = sorted(full)
    n = len(attempts)
    frac = {a: {i: full[a][i].points / full[a][i].max_points if full[a][i].max_points else 0.0 for i in ids} for a in attempts}
    total = {a: sum(full[a][i].points for i in ids) for a in attempts}

    ranked = sorted(attempts, key=lambda a: total[a])
    g = max(1, round(n * GROUP)) if n else 0
    lower = set(ranked[:g])
    upper = set(ranked[-g:]) if g else set()

    out_items = []
    for it in items:
        xs = [frac[a][it.id] for a in attempts]
        rest = [total[a] - full[a][it.id].points for a in attempts]
        p = _mean(xs)
        rpb = _corr(xs, rest)
        stat: dict = {"itemId": it.id, "number": it.number, "n": n, "pValue": _r(p), "pointBiserial": _r(rpb or 0.0)}
        notes: list[str] = []
        if n:
            if p >= 0.9:
                notes.append(f"Almost everyone earns full marks here (p = {p:.2f}), so it separates strong and weak candidates only a little.")
            elif p <= 0.2:
                notes.append(f"Few earn full marks here (p = {p:.2f}): check the key and the wording before reading it as hard.")
            if rpb is None:
                notes.append("Everyone scored the same here, so it says nothing about who knows more.")
            elif rpb < 0:
                notes.append(f"Candidates who do worse on the rest do better here (r = {rpb:.2f}): check the key first.")
            elif rpb < 0.2:
                notes.append(f"Only weakly related to the rest of the exam (r = {rpb:.2f}): worth a look at what it measures.")
        if it.kind == "choice" and n:
            choices = []
            for idx, c in enumerate(it.choices):
                picked = lambda group: sum(1 for a in group if c in full[a][it.id].picked)  # noqa: E731
                share = picked(attempts) / n
                su = picked(upper) / len(upper) if upper else 0.0
                sl = picked(lower) / len(lower) if lower else 0.0
                correct = c in it.key
                choices.append({"choiceId": c, "correct": correct, "share": _r(share), "shareUpper": _r(su), "shareLower": _r(sl)})
                if not correct and su > sl and n >= MIN_N:
                    notes.append(f"Option {letter(idx)} draws more of the strongest candidates than the weakest ({su:.0%} against {sl:.0%}): check it is not also defensible.")
                if not correct and share < 0.02 and n >= MIN_N:
                    notes.append(f"Option {letter(idx)} is almost never chosen ({share:.0%}): it is not working as a distractor.")
            stat["choices"] = choices
        if notes:
            stat["notes"] = notes
        out_items.append(stat)

    # KR-20 over the all-or-nothing (machine-scored) items.
    dich = [it for it in items if it.kind != "essay"]
    kr20 = 0.0
    if n >= 2 and len(dich) >= 2:
        k = len(dich)
        tot = [sum(frac[a][it.id] for it in dich) for a in attempts]
        mt = _mean(tot)
        var = sum((t - mt) ** 2 for t in tot) / (n - 1)
        pq = 0.0
        for it in dich:
            p = _mean([frac[a][it.id] for a in attempts])
            pq += p * (1 - p) * n / (n - 1)
        if var > 0:
            kr20 = (k / (k - 1)) * (1 - pq / var)

    return {
        "examVersion": exam_version,
        "n": n,
        "items": out_items,
        "kr20": _r(kr20),
        "tooFew": n < MIN_N,
    }


def items_of(snapshot: dict) -> list[Item]:
    """Scored items, in the order a candidate sees them, from an exam_versions snapshot (protobuf JSON)."""
    out = []
    number = 0
    for section in snapshot.get("sections", []):
        for it in section.get("items", []):
            number += 1
            kind = it.get("type", "")
            if kind in ("ITEM_TYPE_LIKERT", "ITEM_TYPE_RATING"):
                continue
            out.append(Item(
                id=it["id"],
                number=number,
                kind="essay" if kind == "ITEM_TYPE_ESSAY" else "short" if kind == "ITEM_TYPE_SHORT" else "choice",
                max_points=int(it.get("points", 0)),
                choices=[c["id"] for c in it.get("choices", [])],
                key=list(it.get("correct", [])),
            ))
    return out
