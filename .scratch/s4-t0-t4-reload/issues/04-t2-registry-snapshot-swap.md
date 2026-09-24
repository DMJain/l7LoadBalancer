# 04: S4.T2 — Registry snapshot swap, removed-backend suppression, ring rebuild

**What to build:** The registry's backend set becomes replaceable while
traffic flows. Every selector sees a new backend set on its next Select;
unchanged backends keep their instance and all their state; removed backends
stop being selected and — from the instant they are removed — report nothing to
any round-trip observer; the consistent-hash ring follows the new set. The
application keeps an atomic record of the loaded config. Verified through tests;
nothing in production calls apply yet. Spec: stories 4, 7, 20, 21, 35, 39–43,
53–55, decisions D8–D12. Design per ADR-0015.

**Blocked by:** 03.

**Status:** done

- [x] The registry holds one atomic pointer to an immutable snapshot of a
      monotonically increasing version and the ordered backend slice. All,
      Selectable, Allow, and SetCircuitGate keep their contracts. An exported
      version accessor exists; version and backend set can be read from one
      load.
- [x] **Apply** takes a diff, builds the next snapshot in the new file's order
      — reusing the existing instance for each unchanged identity, constructing
      a fresh instance for each added one — swaps it in, and returns the added
      and removed instances. Single-writer; readers never lock.
- [x] Apply marks each removed backend removed **before** the swap. `Backend`
      gains an unexported atomic removed flag with a read accessor (method-only,
      amending ADR-0002 decision 5 via ADR-0015).
- [x] The proxy skips the whole observer fan-out when the request's backend is
      removed; the active-connection slot and gauge accounting are unaffected.
      `proxy.New(reg, sel)` unchanged.
- [x] The consistent-hash bounded-loads selector caches (version, ring) and
      rebuilds from the snapshot's full backend list on a version mismatch
      (CAS; redundant concurrent builds are fine). The naive comparator stays
      static.
- [x] The application holds an atomic pointer to the loaded config, set by
      build, with a read accessor.
- [x] The sprint-1 contracts doc's concurrency-ownership rows for the registry
      backend set and loaded config describe the as-built swap.
- [x] Tests (Red first): unchanged instances keep pointer identity and state
      (health, active conns, EWMA, circuit); added are fresh; removed are
      marked and absent from All/Selectable; new order; version increases; a
      re-added identity gets a fresh instance. A proxy test: request selected
      before removal, completing after it, reaches no observer yet releases its
      slot. A consistent-hash test: ring reflects the new set after a version
      change; concurrent selects across a swap race-free under `-race`.
      Loaded-config accessor test.
- [x] Hand-off recorded in the PROGRESS entry: apply is test-only until T3;
      fresh backends start healthy until T3.
- [x] `make test`, `make test-race`, vet, fmt clean.
