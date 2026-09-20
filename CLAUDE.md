# Working agreement for this repo

This is a learning build. The write-up matters as much as the code.

## Maintain docs/ as we go

- After any working change, append to `docs/build-log.md`: what we tried,
  what broke, the exact error, and what fixed it. Include dead ends —
  those are the most valuable entries. Never delete a past entry.
- When I say something surprised me, or when a result contradicts what we
  expected, record it under **Surprised me:** in the current session entry.
- Every measurement goes in `docs/measurements.md` with the command that
  produced it, the sample size, and the variance.
- Record tool and image versions in `docs/environment.md` the first time
  each one is used.
- Put raw output in `docs/raw/` unedited.

## Rules

- Never write a number into docs/ that we did not actually measure.
- Never tidy the build log into a clean narrative. It's a log, not a story.
- No secrets, no company config, no internal hostnames — this repo is public.
- Synthetic data only.

## Reproducibility

Every experiment gets a Makefile target. If I can't rerun it with one
command, it isn't finished.
