# Local live demo — recording script (S5.T17.1)

The scenario sequence for recording the demo video (S5.T17.2). It gives the
click path, what to narrate with the ADR behind each claim, the expected
on-screen state, and how long to wait. Follow it in order; every "expected"
state below was observed in a full rehearsal against a running stack, and the
figures are that rehearsal's, not aspirational values (see *Rehearsal record* at
the end).

Companion documents: [`README.md`](README.md) (how the stack runs),
[ADR-0023](../docs/adr/0023-local-live-demo.md) (why there is no public
deployment), ADR-0024 (generator), ADR-0025 (control service).

## Before you record

1. Bring the stack up and validate the rig. This is a precondition for every
   recording — it is the guard against the `degraded`-slice failure, where
   injected latency reached every backend (ADR-0023 decision 10).

   ```sh
   make demo-up          # builds ~18 containers; first build takes minutes
   demo/acceptance.sh    # must print "All checks passed."
   ```

   The check sets backend3 to 200 ms, requires backend1/2/4 p50 ≤ 20 ms and
   backend3 p50 ≥ 150 ms, then switches to p2c-ewma and requires backend3's
   share < 15%; its exit trap restores every profile and the generators.

2. Open two browser windows (or two tabs):
   - **Control page** — <http://127.0.0.1:8095/> — the one window you drive:
     switch LB, set rate, set profiles, kill/revive. Its three embedded charts
     are the dashboard's **request rate**, **request latency p50/p99**, and
     **request share** panels, at `var-window=15s&refresh=5s`, following the
     active LB (ADR-0025 decision 5).
   - **Dashboard** — <http://127.0.0.1:3000/d/l7loadbalancer/?var-window=15s&var-lb=lb-roundrobin>
     — the full six-panel view. Open this for the panels the control page does
     **not** embed: **circuit state**, **backend healthy**, and **active
     connections by backend**. Scenarios 2a, 4 and 5 read those, so keep the
     dashboard visible for them (set `var-lb` to the active LB, or the panel
     shows mixed).

3. Confirm the page shows `active: lb-roundrobin` and `now: 400 req/s`, and
   that every backend row reads `sleep_ms 0 jitter_ms 0 fail_rate 0` with
   `container: running`.

## The convergence pause (read before the first switch)

After **every** LB switch, wait ~30 s before making any claim about the
distribution. The charts use a 15 s window, and different selectors have
different settling times:

- **p2c-ewma**: an LB's latency estimate is fed only by responses it proxies
  (ADR-0010). It is **cold on its first activation** — every backend reads an
  EWMA of 0, so the first seconds are a random split — and **stale on every
  later activation**, because nothing updates an idle LB's estimates
  (ADR-0023 decision 5). Give it 30–45 s.
- **consistent-hash-bounded**: each backend's bounded-load "capacity" is
  relative to current in-flight load, so give the 15 s window time to fill.
- **round-robin / least-connections**: no learned state, but the window still
  needs to fill; a slow backend's in-flight count also takes a few seconds to
  rise before least-connections reacts.

Say it on camera — *"the estimate needs samples, so we wait for it to learn"* —
so the pause reads as a property of the algorithm, not a stall.

## Reset (between every scenario)

Return the rig to its baseline before the next scenario. On the control page:

- Each backend row: `sleep_ms 0`, `jitter_ms 0`, `fail_rate 0` → **Apply**.
- **Total load rate**: `400` → **Set rate**.
- **Active LB**: `lb-roundrobin` → **Switch active LB**.

Then wait ~20 s until every backend row is clean, every chart shows all four
backends at ~25 %, and (after the flaky-backend scenario only) `lb_circuit_state`
is closed and `lb_backend_healthy` is 1 for all four. The circuit cooldown is
30 s (ADR-0012), so allow up to ~40 s there.

---

## Scenario 1 — Baseline: round-robin, every backend fast

**Goal:** establish the reference picture — even split, low latency, healthy.

| | |
|---|---|
| **Click path** | Active LB = `lb-roundrobin` → *Switch active LB*. (This is the default; switch only if you are not already on it.) |
| **Wait** | 20 s |
| **Expected** | Generator totals ~`offered = sent`, `dropped 0`. Request rate ~370 req/s delivered of the 400 configured (the generator's counters are the honest check — ADR-0024). Request-share panel: **25 / 25 / 25 / 25 %**. p50 ~2.5 ms on all four. Every backend `healthy 1`, circuit `closed`. |
| **Narrate** | "Round-robin ignores latency and load: four equal backends get an even quarter each. The generator is open-loop and Zipf-skewed across eight clients, so the *offered* rate is honest even when a backend slows (ADR-0024). The dashboard's *Active connections by backend* panel stays near zero because each request lasts ~2.5 ms." |
| **ADRs** | ADR-0013 (metrics); ADR-0024/ADR-0021/ADR-0022 (generator). |

## Scenario 2 — One far backend under each selector

**Goal:** inject 200 ms on one backend and watch each selector react — or fail
to. This is the property the published `degraded` slice could not show.

Set it up: on the **backend3** row set `sleep_ms 200`, `jitter_ms 0`,
`fail_rate 0` → **Apply**. Wait 25 s. Then step through the four LBs in order;
**switch, wait 30 s, claim, repeat**.

### 2a — round-robin (latency-blind)

| | |
|---|---|
| **Click path** | Active LB = `lb-roundrobin` → *Switch active LB*; wait 30 s |
| **Expected** | Shares **still 25 / 25 / 25 / 25**. backend3 p50 ~175 ms (the p50 lands in the histogram bucket just under the 200 ms injection — see the note below), backends 1/2/4 ~2.5 ms. The dashboard's *Active connections by backend* panel shows ~17 on backend3. |
| **Narrate** | "Round-robin does not look at latency at all: backend3 still gets its quarter, and its in-flight count piles up while the others stay idle. This is exactly the leakage the `degraded` slice had to rule out — here it is *isolated*, which the acceptance check proved." |
| **ADRs** | ADR-0023 decision 10; ADR-0013. |

> The injected 200 ms shows up as p50 ≈ 175 ms, not 200 ms: percentiles are
> interpolated from the fixed histogram buckets (`.1`, `.25`), so a p50 near the
> top of a bucket reads a little low. The acceptance check uses a ≥ 150 ms
> threshold for this reason. Narrate the *injection* as 200 ms and the *chart* as
> "about 175 ms by the buckets".

### 2b — least-connections (moves off, first-in-order tie-break)

| | |
|---|---|
| **Click path** | Active LB = `lb-leastconn` → *Switch active LB*; wait 30 s |
| **Expected** | backend3 drops to ~1 %. The remaining traffic is **not** even: backend1 ~78 %, backend2 ~18 %, backend4 ~2 %. |
| **Narrate** | "Least-connections picks the backend with the fewest in-flight requests. backend3 accumulates in-flight because it is slow, so it is chosen least — but the three fast backends sit at the *same* count, and the tie breaks first-in-registry-order, so backend1 takes most of it. At this rate the in-flight counts hover at 0–1, which is the low-concurrency regime where the tie-break dominates; it does not spread out until in-flight counts are reliably above zero." |
| **ADRs** | The tie-break is documented in the selector and the bundle spec; load is in-flight (`lb_active_requests`, ADR-0013). |
| **Finding** | Raising the rate to 800 did **not** even out the split (measured 69 / 24 / 0.7 / 5.5 %) because a request lasts ~2.5 ms, so in-flight stays ~0. Do not promise a spread-out split at the demo rate. |

### 2c — p2c-ewma (moves off; rank split only with distinct latencies)

| | |
|---|---|
| **Click path** | Active LB = `lb-p2c-ewma` → *Switch active LB*; wait 30–45 s |
| **Expected** | backend3 → **0 %**. The three fast backends split **~33 / 33 / 33 %** (measured 32 / 34 / 35), because their EWMA values are equal, so p2c is choosing at random among three. |
| **Narrate** | "Two random backends are compared and the lower EWMA wins, so backend3 — the slow one — is never chosen once its estimate has risen. The other three are identical, so they split evenly. The famous 50 / 33 / 17 / 0 split appears only when the four estimates are *different*; with one slow backend and three identical ones you get an even split over the three." |
| **ADRs** | ADR-0010; ADR-0023 decision 5. |

**Now show the rank split.** Set `backend1 0`, `backend2 50`, `backend3 100`,
`backend4 200` (all jitter 0, fail_rate 0) → **Apply** each. Wait 45 s.

| | |
|---|---|
| **Expected** | Shares ≈ **50 / 33 / 17 / 0 %** — measured **49.7 / 33.3 / 16.7 / 0.0**; p50 = 2.5 / 75 / 175 / — ms. |
| **Narrate** | "With four *distinct* estimates the probability of each rank being chosen is exactly 1/2, 1/3, 1/6, 0 — half the traffic to the fastest, a third to the next, a sixth to the third, nothing to the slowest. That is the p2c signature." |

**Now show starvation (the honest limitation).** Leave the LBs as they are and
set **backend4** back to `sleep_ms 0` → **Apply**. Wait 30 s.

| | |
|---|---|
| **Expected** | backend4 **stays at 0 %** even though it is now the joint-fastest. Its estimate is the stale 200 ms value and p2c never sends it traffic, so the estimate never updates. |
| **Narrate** | "This is the limitation ADR-0023 warns about: EWMA has no decay and is fed only by proxied responses, so a backend that was slow can stay starved after it recovers. The algorithm does not self-heal; recovery is a restart or a different selector." |
| **Recovery (pick one, say which)** | (a) *Rehearsed:* switch to `lb-roundrobin` — backend4 gets ~25 % again at once; the traffic moved, the p2c estimate did not. (b) *Not rehearsed (ADR-0010 cold start):* in a terminal `docker compose -f demo/docker-compose.yml restart lb-p2c-ewma`; a fresh process has cold EWMA and re-learns from scratch. |
| **ADRs** | ADR-0010; ADR-0023 decision 5 and its Consequences. |

### 2d — consistent-hash-bounded (spills by load, not latency)

| | |
|---|---|
| **Click path** | With backend3 back at 200 ms (reset backend4 to 0; set backend3 to 200), Active LB = `lb-consistent-hash` → *Switch active LB*; wait 35 s. |
| **Expected** | backend3 is **not** pinned at its fast share: it drops to ~3 %; backend1 rises to ~68 %, backend2 ~24 %, backend4 ~4.5 %. |
| **Narrate** | "Consistent hashing normally pins each client to a backend forever — it is latency-blind by design. This is the *bounded-loads* variant (ADR-0009): a backend whose load exceeds capacity spills its keys to the next backend on the ring. A slow backend accumulates in-flight load, so it spills and stops receiving most of its keys. Note it is reacting to *load*, not to latency — the mechanism is different from p2c even though both move traffic." |
| **ADRs** | ADR-0008 (ring pipeline); ADR-0009 (bounded loads; decision 2 defines the load signal as `ActiveConns`, decision 3 the capacity, ε = 0.25). |

## Scenario 3 — Zipf hot key under consistent-hash-bounded

**Goal:** show per-client stickiness and the skewed key distribution.

Reset first (all backends `0/0/0`, rate 400, LB `lb-roundrobin`). Then:

| | |
|---|---|
| **Click path** | Active LB = `lb-consistent-hash` → *Switch active LB* |
| **Wait** | 30 s |
| **Expected** | A clearly skewed but **stable** split — measured **52.8 / 5.6 / 41.9 / 0.1 %**. It does not drift while you watch: each of the eight clients is pinned to one backend for as long as the ring is unchanged. |
| **Narrate** | "The hash key is the client's IP, and there are eight client containers with distinct IPs and Zipf-skewed rates. So the hot client's key owns one backend, the next few keys own another, and some backends own almost no keys. Because it is consistent hashing, those pins do not move — unlike p2c, which reshuffles as estimates change." |
| **Bounded-spill note** | The split is not the raw key ownership you would get from a plain consistent hash: the *bounded* variant admits a backend only while its in-flight load is within `max(1, ceil(avg × (1 + ε)))` and spills the excess down the ring (ADR-0009 decisions 2–3, ε = 0.25), which is why the hot owner sits at ~53 % rather than everything the hot key would give it. Spill is relative to load, so changing the total rate does **not** change the split (measured near-identical at 400 and 2000 req/s). |
| **ADRs** | ADR-0008; ADR-0009; ADR-0023 decision 6; ADR-0024. |

## Scenario 4 — Flaky backend: outlier detection and the circuit breaker

**Goal:** show failure handled without taking the backend's `/health` down.

Reset first (LB `lb-roundrobin`, rate 400). Then:

| | |
|---|---|
| **Wait** | 15 s |
| **Click path** | On the **backend2** row set `sleep_ms 0`, `jitter_ms 0`, `fail_rate 0.5` → **Apply**. Watch the control page's request-share chart and the dashboard's *Backend healthy* and *Circuit state* panels. |
| **Expected** | Within ~3 s backend2's request share collapses to ~0 and a small `5xx` rate appears (measured 0.07/s residual); *Backend healthy* goes to **0** (passive outlier ejection — ADR-0011). *Circuit state* goes **open** ~6 s after injection and stays open. Meanwhile backend2's own `/health` still returns `200 ok` (the dummy backend's `/health` is a chaos-free control path — S5.T4-infra). The other three backends split evenly ~33 % each and the generators report no errors or drops. |
| **Narrate** | "The backend now fails half its requests. Passive outlier detection sees the failures on the live request path and ejects it — traffic moves away within a couple of seconds, before the active probe would even have run three times. The circuit breaker then opens and gates the backend for its 30 s cooldown. The `/health` endpoint is a separate, chaos-free control path, so an orchestrator still sees the process as alive: the backend is *failing* without being *down*." |
| **ADRs** | ADR-0011 (outlier + circuit composition); ADR-0012 (circuit gate); ADR-0013 (the gauges); S5.T4-infra (the backend's `/health` path). |
| **Recovery check** | Set `fail_rate 0` → **Apply**. Health returns in ~10 s (two good probes); wait up to 40 s for the circuit cooldown to close it, then all four backends read `healthy 1` / circuit closed / ~25 %. |

## Scenario 5 — Kill and revive a backend

**Goal:** show abrupt death, ejection on **every** LB, clean 502s, reinstatement.
Kill is the Docker Engine's abrupt kill, matching the Sprint 3/4 chaos tests
(ADR-0025 amendment decision 9), and the demo backends deliberately carry no
restart policy, so a killed backend stays down (`demo/docker-compose.yml`).

| | |
|---|---|
| **Click path** | LB `lb-roundrobin` active. On the **backend1** row click **Kill**. Watch the row's `container:` label (control page) and the dashboard's *Backend healthy* panel. |
| **Wait** | 15 s to eject |
| **Expected** | backend1's `container` goes **`exited`**; *Backend healthy* → 0 on every LB (all four LBs point at the same backends) and its request share decays to **0** within ~15 s. A handful of requests in flight at the moment of the kill get a clean **502** (measured 5). The other three backends take the whole rate and the generators report no errors. |
| **Narrate** | "The kill is abrupt. Requests already in flight get a 502 — the failure is classified and returned, never retried (ADR-0018); the load balancer never replays a request it cannot know is idempotent. Within the health threshold every LB ejects backend1, and the other three absorb the traffic with zero dropped arrivals." |
| **ADRs** | ADR-0018 (no retry); ADR-0011/ADR-0012 (ejection and gating); ADR-0025 amendment (kill semantics). The clean 502 is prior work S4.T6 (backend death), not an ADR. |

Then:

| | |
|---|---|
| **Click path** | On the same row click **Revive**. |
| **Wait** | 15 s |
| **Expected** | `container` → **`running`**; `healthy` → 1 at ~10 s (two consecutive successful probes); request share ramps back to **25 %** by ~20 s. |
| **Narrate** | "Revive starts the same container. The active checker reinstates it after two good probes — active probing is the only path that recovers a backend passive detection ejected (ADR-0011). It rejoins the rotation at an even quarter." |

## Scenario 6 — Drain reload under load

**Goal:** show what SIGHUP actually does — change the backend list, draining
in-flight requests — without implying the algorithm is reloadable. This is the
one step that needs a terminal; the algorithm itself is **never** reloaded
(ADR-0015 decision 4). Use a *different* LB than the one you are driving the
story on if you prefer, but `lb-roundrobin` is the default.

Run with the stack under load on `lb-roundrobin`:

```sh
cd <repo>
cfg=demo/configs/roundrobin.yaml
cp "$cfg" /tmp/roundrobin.orig.yaml                 # keep the original
grep -v backend4 "$cfg" > /tmp/roundrobin-nob4.yaml # drop backend4
cp /tmp/roundrobin-nob4.yaml "$cfg"                 # cp preserves the inode:
                                                    # the bind mount sees it
docker compose -f demo/docker-compose.yml kill -s HUP lb-roundrobin
```

| | |
|---|---|
| **Wait** | ~15 s (the reload is immediate; the 15 s chart window then refills) |
| **Expected** | The LB stays up (RPS does not dip). Its log prints `"msg":"config reloaded","event":"config_reloaded","added":0,"removed":1,"unchanged":3`. The remaining three backends pick up the traffic and settle at ~33 % each; **zero** errors and **zero** drops at the generator. backend4's own `/stats` counter still ticks slowly — the *other three LBs' health checkers* keep probing it; the reload removed it from this LB only. |
| **Narrate** | "SIGHUP changed the backend list and nothing else — the algorithm is not reloadable, only the list is (ADR-0015). Removed backends stop being selected immediately and any in-flight request is allowed to drain inside the 30 s window before it would be cancelled (ADR-0016). At these 2.5 ms latencies drain is instant; the bench proved the zero-drop property with a slow backend. Because the config is a single-file bind mount, the file is rewritten **in place** — a rename would leave the mount pointing at the old file." |

Restore it when done:

```sh
cp /tmp/roundrobin.orig.yaml demo/configs/roundrobin.yaml
docker compose -f demo/docker-compose.yml kill -s HUP lb-roundrobin
```

| | |
|---|---|
| **Wait** | ~15 s |
| **Expected** | `added:1 removed:0 unchanged:3` and backend4 returns to ~25 %. |
| **ADRs** | ADR-0015 (reload architecture); ADR-0016 (drain lifecycle); ADR-0018 (no retry). |

## Known selector behaviours — state these, never contradict

Keep these on the narration sheet. They were all verified against the code and
observed in rehearsal.

- **p2c-ewma** chooses between two random **selectable** backends and takes the
  lower **EWMA latency** (ADR-0010). With four backends of *distinct* estimates
  the split settles near **50 / 33 / 17 / 0 %** whatever the size of the gaps.
  With one slow backend and three equal ones, the three split *evenly*. The
  slowest backend gets no traffic from this LB.
- **EWMA has no decay** and is fed only by proxied responses. A backend restored
  to fast latency may stay starved. Narrate as a known limitation; recovery is a
  restart or another algorithm (ADR-0023 decision 5).
- **least-connections** breaks ties in registry order, so at low concurrency it
  concentrates on `backend1`. At the demo's 400 req/s the in-flight counts hover
  at 0–1 (a request lasts ~2.5 ms), so the tie-break, not the balance, is what
  the viewer sees; the slow backend is still avoided because it accumulates
  in-flight.
- **consistent-hash-bounded** pins each client container (one hash key per
  `RANK`) to a backend and spills when the owner's **load** exceeds **capacity**;
  with Zipf-skewed clients the hottest key's owner is the one that spills
  (ADR-0008, ADR-0009).
- **A flaky backend** is ejected by passive outlier detection and gated by the
  circuit breaker while its `/health` keeps returning 200 — failing, not down
  (ADR-0011; the `/health` chaos-free path is S5.T4-infra).
- **Never promise an algorithm switch by SIGHUP.** Only the backend list
  reloads; a reload that changes `algorithm` is rejected whole (ADR-0015
  decision 4). "Switching algorithm" here means pointing the eight clients at a
  different LB.

## Teardown

```sh
make demo-down
```

Nothing is left running, and nothing was exposed beyond `127.0.0.1` (ADR-0023
decision 2). The demo charts are not benchmark results; `RESULTS.md` remains the
only published performance evidence (ADR-0023, Consequences).

## Rehearsal record (2026-10-02, this ticket)

Every expected state above was observed in one full pass against a live
`demo/` stack after `demo/acceptance.sh` passed (phases: backend1/2/4 p50
2.5 ms, backend3 p50 175.0 ms; p2c share 0.00 %). Measured figures used above:

| Scenario | Observation |
|---|---|
| 1 baseline RR | ~370 req/s delivered of 400; 25.0 / 25.0 / 25.0 / 25.0 %; p50 all 2.5 ms; healthy 1; circuit closed; `offered = sent`, dropped 0 |
| 2a RR + 200 ms | 25 / 25 / 25 / 25 %; backend3 p50 175 ms, active ~17 |
| 2b least-conn | 78.4 / 18.3 / 0.9 / 2.0 % |
| 2c p2c, 1 slow | 32.2 / 34.2 / 0.0 / 35.3 % |
| 2c p2c, 0/50/100/200 ms | **49.7 / 33.3 / 16.7 / 0.0 %** |
| 2c starvation | backend4 restored to 0 ms still 0.0 % |
| 2d consistent-hash + 200 ms | backend3 spills to 3.0 %; 68.0 / 24.4 / 3.0 / 4.5 % |
| 3 consistent-hash baseline | 52.8 / 5.6 / 41.9 / 0.1 %; near-identical at 2000 req/s |
| 4 flaky `fail_rate 0.5` | healthy 0, share ~0, circuit open by t+6 s, `/health` 200 `ok`; recovered in ~40 s |
| 5 kill backend1 | `exited`; healthy 0 within 5 s; share → 0 by 15 s; 5 clean 502s; revive → healthy 1 at ~10 s, 25 % by ~20 s |
| 6 drain reload | `config_reloaded added=0 removed=1 unchanged=3`; ~33 % each; zero errors; restored cleanly |

### Findings recorded during the rehearsal

1. **The 50 / 33 / 17 / 0 % split needs four distinct estimates.** With one slow
   backend it does *not* appear (the three fast backends tie and split evenly),
   so the script adds an explicit staggered-latency step to produce it. This is
   a rehearsal finding, not a defect: it follows from p2c's definition.
2. **The generator delivers slightly under its configured total** on this
   machine at the default 400 req/s (~370 measured) and noticeably under at
   2000 (~1530), because Poisson inter-arrival sleeps below ~1 ms hit timer
   granularity. The generator's `offered`/`sent`/`dropped` counters are the
   honest check (ADR-0024); the script narrates the configured rate and points
   at those counters rather than claiming the delivered rate exactly. The demo
   default 400 is the comfortable point; do not run the demo at high rates.
3. **A removed backend's `/stats` counter does not freeze completely** during
   Scenario 6: the *other three LBs* keep actively probing it. The reload's
   effect is visible on the driving LB's share, not on the backend's raw
   counter. Narrate accordingly (the script does).
4. **No `wget` in the dummy-backend image** (distroless): to curl a backend's
   `/health` by hand, exec from the Prometheus container, e.g.
   `docker compose -f demo/docker-compose.yml exec -T prometheus wget -qO- http://backend2:8080/health`.
