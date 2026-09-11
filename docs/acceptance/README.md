# Acceptance case lists

Each slice from S-014 onward commits its public-safe acceptance cases here, in
a file named for the slice (`S-014.md`). A list is a Markdown table whose first
two columns are the case and its level:

```
| Case | Level | Exercise | Passes when |
|---|---|---|---|
| T-01 | unit | … | … |
| H-01 | host, driven | … | … |
```

A **unit** case is proved by a test that names it on a comment line of its
own in its doc comment, `Covers S-014 T-01.` after the comment marker, or
several at once as `Covers S-014 T-01, T-02.`. The tier-2 test in
`internal/acceptance` fails when a unit case has no such test, and when a test
names a case that no list has. **Host** and **regression** cases are proved by
live host evidence instead, as `CONTRIBUTING.md` describes, and need no test
reference.

Slices before S-014 keep the tests they landed with and are not backfilled.
