# 03: Sprint 3 Config Schema — Probe and Cooldown Fields

**What to build:** The new global YAML config fields Sprint 3 needs (health
probe interval, health probe timeout, circuit cooldown duration), plus
`Validate()` rules — schema only, no runtime behavior reads these fields
yet. Prefactor, following this project's own S1.T0.5 precedent of freezing
schema before behavior lands on top; unblocks both S3.T1 (active health
checks) and S3.T3 (circuit breaker), which would otherwise both edit
`config.go` in parallel.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] `Config` gains new **global** (not per-backend) fields for: health
      probe interval, health probe timeout, and circuit cooldown duration —
      field grouping/naming at the implementer's discretion (e.g. a nested
      section vs. flat top-level fields), documented at the field
      declaration
- [ ] Fields are optional in YAML: omitting any of them defaults to a
      documented, sane built-in duration, so every currently-committed
      config file (`configs/example.yaml`, existing test fixtures) keeps
      loading and validating unmodified
- [ ] `Validate()` rejects a non-positive (zero or negative) duration for
      any of the three fields when explicitly set
- [ ] `KnownFields(true)` strictness is preserved — a typo'd field name still
      fails to load
- [ ] Consecutive-failure/success thresholds, outlier window size, and
      circuit's failure-to-open threshold are explicitly **not** added
      here — those stay Go constants per ADR-0011 decision 10; this ticket
      is the config-tier knobs only
- [ ] Test: a config omitting all three new fields loads and validates,
      with the documented defaults observable
- [ ] Test: a config setting explicit values for all three loads them
      correctly
- [ ] Test: a config setting a non-positive value for any of the three
      fails `Validate()`
- [ ] `PROGRESS.md`: this ticket added and flipped to `[DONE]` on completion

## Comments
