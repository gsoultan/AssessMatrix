"""Run: python3 -m unittest -v (from services/psychometrics)."""

import unittest

import ctt

ITEMS = [
    ctt.Item(id="q1", number=1, kind="choice", max_points=1, choices=["a", "b", "c", "d"], key=["b"]),
    ctt.Item(id="q2", number=2, kind="choice", max_points=1, choices=["a", "b", "c"], key=["a"]),
    ctt.Item(id="q3", number=3, kind="essay", max_points=4),
]


def cohort(rows):
    """rows: (attempt, q1 pick, q2 pick, q3 points)"""
    out = []
    for a, p1, p2, e in rows:
        out.append(ctt.Score(a, "q1", 1 if p1 == "b" else 0, 1, [p1]))
        out.append(ctt.Score(a, "q2", 1 if p2 == "a" else 0, 1, [p2]))
        out.append(ctt.Score(a, "q3", e, 4, []))
    return out


class Analyze(unittest.TestCase):
    def test_hand_computed(self):
        # Four candidates. q1 right for the two strongest; q2 right for one
        # strong, one weak (no discrimination); the essay orders them.
        rows = [("w1", "a", "a", 0), ("w2", "c", "b", 1), ("s1", "b", "a", 3), ("s2", "b", "b", 4)]
        r = ctt.analyze(ITEMS, cohort(rows), 3)
        self.assertEqual(r["n"], 4)
        self.assertTrue(r["tooFew"])
        q1, q2, q3 = r["items"]
        self.assertEqual(q1["pValue"], 0.5)
        self.assertEqual(q2["pValue"], 0.5)
        self.assertEqual(q3["pValue"], 0.5)  # (0 + .25 + .75 + 1) / 4
        # q1 vs rest-of-total (q2 + q3): rest = [1, 1, 4, 4], q1 = [0, 0, 1, 1] -> r = 1.
        self.assertEqual(q1["pointBiserial"], 1.0)
        # q2 vs rest (q1 + q3): rest = [0, 1, 4, 5], q2 = [1, 0, 1, 0].
        # Pearson by hand: mx=.5, my=2.5, sxy=(.5*-2.5)+(-.5*-1.5)+(.5*1.5)+(-.5*2.5)=-1.0,
        # sxx=1, syy=6.25+2.25+2.25+6.25=17 -> r = -1/sqrt(17) = -0.243.
        self.assertEqual(q2["pointBiserial"], -0.243)
        self.assertTrue(any("check the key" in n for n in q2["notes"]))
        # Distractors: upper/lower groups are one candidate each (27% of 4 rounds to 1).
        c = {x["choiceId"]: x for x in q1["choices"]}
        self.assertTrue(c["b"]["correct"])
        self.assertEqual(c["b"]["share"], 0.5)
        self.assertEqual((c["b"]["shareUpper"], c["b"]["shareLower"]), (1.0, 0.0))
        self.assertEqual(c["d"]["share"], 0.0)

    def test_pending_essays_are_left_out(self):
        rows = cohort([("a", "b", "a", 2), ("b", "a", "b", 0)])
        rows[-1] = ctt.Score("b", "q3", None, 4, [])  # still waiting for a person
        r = ctt.analyze(ITEMS, rows, 1)
        self.assertEqual(r["n"], 1)

    def test_no_variation_says_so(self):
        rows = [(f"x{i}", "b", "a", 4) for i in range(40)]
        r = ctt.analyze(ITEMS, cohort(rows), 1)
        self.assertFalse(r["tooFew"])
        q1 = r["items"][0]
        self.assertEqual(q1["pValue"], 1.0)
        self.assertTrue(any("Everyone scored the same" in n for n in q1["notes"]))
        self.assertTrue(q1["noSpread"], "an undefined point-biserial must not read as a weak 0")
        self.assertTrue(any("almost never chosen" in n for n in q1["notes"]))
        self.assertEqual(r["kr20"], 0.0)

    def test_kr20(self):
        # Two dichotomous items, perfectly consistent: KR-20 = 1.
        rows = [(f"r{i}", "b", "a", 0) for i in range(5)] + [(f"w{i}", "a", "b", 0) for i in range(5)]
        r = ctt.analyze(ITEMS, cohort(rows), 1)
        self.assertEqual(r["kr20"], 1.0)

    def test_empty(self):
        r = ctt.analyze(ITEMS, [], 1)
        self.assertEqual((r["n"], r["tooFew"]), (0, True))
        self.assertEqual(r["items"][0]["pValue"], 0.0)


class Snapshot(unittest.TestCase):
    def test_items_of(self):
        snap = {"sections": [{"items": [
            {"id": "a", "type": "ITEM_TYPE_SINGLE", "points": 2, "choices": [{"id": "x"}, {"id": "y"}], "correct": ["y"]},
            {"id": "l", "type": "ITEM_TYPE_LIKERT", "points": 1},
            {"id": "e", "type": "ITEM_TYPE_ESSAY", "points": 5},
        ]}]}
        got = ctt.items_of(snap)
        self.assertEqual([(i.id, i.number, i.kind) for i in got], [("a", 1, "choice"), ("e", 3, "essay")])
        self.assertEqual(got[0].key, ["y"])


if __name__ == "__main__":
    unittest.main()
