# 03: S4.T1 — Config diffing by backend identity + ADR-0015

**What to build:** The pure comparison a reload is decided on: given the loaded
config and a reloaded one, which backends are **added**, **removed**, and
**unchanged** by **backend identity** (name, URL), and which non-backend fields
differ. Before any code, ADR-0015 records the reload architecture that T1–T3
implement. Nothing calls these functions in production yet (T3 does). Spec:
stories 5, 51–52, decisions D5–D7.

**Blocked by:** 02.

**Status:** done

- [x] **ADR-0015 (reload architecture)** is written and committed in the
      design step, before any code, covering: in-process registry snapshot
      swap vs SO_REUSEPORT process handoff; (name, URL) identity; backends-only
      reject rule; single-load versioned snapshot; pull-based ring rebuild keyed
      on version; removed flag set before the swap; removed-backend observer
      suppression; new-file-order rule; added backends start unhealthy and are
      admitted by one successful probe (and the asymmetry with startup);
      permitted empty-selectable blue/green window with WARN. `AGENTS.md`'s ADR
      table and key-decisions list gain its row.
- [x] A pure diff function over (old, new) configs returns added, removed, and
      unchanged backend configs. Added and unchanged are in the new file's
      order; removed in the old file's order.
- [x] A name kept with a changed URL yields one removed plus one added entry,
      never an "updated" one.
- [x] A pure non-backend-change function returns the names of differing fields
      among listen, algorithm, health, circuit, metrics, health_endpoint,
      comparing resolved values — an omitted field and an explicit default
      compare equal after validation's defaulting.
- [x] Table-driven tests: identical; pure add; pure remove; URL change under
      one name; reorder only (all unchanged, new order); mixed; one case per
      non-backend field; omitted-vs-explicit-default equality. Red first.
- [x] Neither function logs, errors, or touches runtime state.
- [x] `make test`, `make test-race`, vet, fmt clean.
