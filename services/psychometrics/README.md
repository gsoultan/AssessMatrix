# Psychometrics worker

Computes classical test theory statistics for each exam version, outside any
request (docs/09 phase 6): difficulty (p-value), corrected point-biserial
discrimination, distractor analysis by top and bottom 27%, and KR-20. Nothing
beyond CTT.

The core marks an exam version stale (`analysis_queue`) whenever an attempt is
scored, at hand-in or after an essay review, and keeps a per-item index of
scores (`item_scores`). This worker takes the oldest stale version through
`am_take_analysis()` (ids only, the one cross-tenant step), scopes itself to
that tenant, reads under row-level security and writes `item_stats`, which the
studio reads through `ResultService.GetItemAnalysis`. A failure rolls back,
leaving the version queued.

```sh
pip install -r requirements.txt
AM_DATABASE_URL=postgres://am_app:app@localhost/assessmatrix python worker.py
python -m unittest -v      # the statistics, against hand-computed values
```

`AM_WORKER_ONCE=1` drains the queue and exits; `AM_WORKER_IDLE` is the poll
interval in seconds (default 5).
