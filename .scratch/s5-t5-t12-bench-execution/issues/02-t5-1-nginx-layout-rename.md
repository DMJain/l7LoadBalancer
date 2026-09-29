# 02: Nginx configs mirror the LB layout — rename-only prefactor (S5.T5.1)

**What to build:** Move the three existing Nginx competitor configs into a layout organised by protocol and then algorithm, mirroring the LB's bench configs and using the same hyphenated algorithm tokens. After the move, an LB config and its Nginx competitor are found at the same relative path. This is a mechanical prefactor that changes no content, so later tickets add configs without touching history. TDD-exempt.

**Blocked by:** 01

**Status:** ready-for-agent

Spec: `../spec.md` (Implementation Decisions → Competitors)

- [ ] HTTP/1.1 round-robin, h2 round-robin and h2 least-connections are moved with `git mv` to `<proto>/<algo>` positions (`http11/roundrobin`, `h2/roundrobin`, `h2/leastconn`). File contents are byte-identical.
- [ ] The compose default for the Nginx config and the harness's protocol/algorithm → Nginx config mapping are updated to the new paths **in the same commit**, so nothing breaks between commits.
- [ ] The commit contains only the rename and the minimal path updates. No new configs, no directive changes, no removal of the solo path (all of that is ticket 03).
- [ ] Verified: `docker compose config` is clean; `nginx -t` passes on all three configs in the pinned Nginx image; `shellcheck -S style` and `bash -n` pass on the harness; the harness's existing core slice still brings up Nginx on both h2 configs (a reduced-constant local run is enough, noted in the session log).
