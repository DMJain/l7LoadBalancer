# Local live demo — scenario script (S5.T17.1, reshaped by S5.T20.8)

The scenarios to rehearse and record, in order. Each one gives what to do, how
long to wait, what the screen should show, and what to say, with the ADR
behind each claim. The demo runs **one** load balancer, and its algorithm is
changed by **hot reload**: edit `demo/configs/lb.yaml`, then send SIGHUP. The
process is never restarted (ADR-0015 decisions 18–21, ADR-0023 decision 14).

Where the expected numbers come from:

- **Measured 2026-10-07 on this shape:** the acceptance check, the hot reloads
  (scenarios 2–3) and the invalid reload (scenario 4).
- **Rehearsed 2026-10-02 on the previous four-LB shape** (marked *rehearsed
  earlier*): scenarios 5–7. Each LB ran the same code over the same backends
  and clients, so the behaviour carries over, but re-check those numbers in
  your dry run.

The video's shot list and voiceover are in
[`docs/demo/video-script.md`](../docs/demo/video-script.md).

## Before you record

1. Bring the stack up and prove the rig:

   ```sh
   make demo-up
   demo/acceptance.sh    # must print "All checks passed."
   ```

   Phase 1 shows a 200 ms delay on backend3 stays on backend3. Phase 2
   hot-reloads the LB to `p2c_ewma` and requires a successful reload, no
   restart, **zero client errors**, and backend3's share < 15 %. Measured:
   backend1/2/4 p50 2.5 ms, backend3 p50 175 ms; after the reload, backend3
   0.00 %, 0 client errors.

2. Open **http://127.0.0.1:8095**. The header reads
   `Algorithm: Round robin (round_robin on lb)`. The load reads `now: 400 req/s`,
   and every backend row reads `0 / 0 / 0` and `running`.

3. Open a terminal on the repository root with two commands ready:

   ```sh
   docker compose -f demo/docker-compose.yml logs -f --since 1s lb | grep --line-buffered config_reload
   docker compose -f demo/docker-compose.yml kill -s HUP lb
   ```

   Open `demo/configs/lb.yaml` in your editor. Any editor works: the
   directory is mounted, so a save by rename is still seen (ADR-0023
   decision 15).

## How a hot reload works (say this once)

- The LB re-reads `lb.yaml` on SIGHUP. It builds the new algorithm, then swaps
  it in with one atomic step.
- Each request picks its backend once, at its start. Requests already in
  flight keep their backend; the next request uses the new algorithm. The
  listener never closes, so no connection is reset (ADR-0015 decision 19).
- The new algorithm starts warm. The latency averages and in-flight counts it
  reads live on the backends and are kept up to date under every algorithm
  (decision 20).
- A config the LB cannot accept (unknown algorithm, duplicate backend, bad URL,
  any other field changed) is rejected whole, and the old config keeps serving.

## The convergence pause

After a reload, wait about 30 s before claiming a distribution. The charts use
a 15 s window, and P2C's averages need fresh samples from the new pattern. Say
it on camera: *"the chart needs a few seconds of new traffic"*.

## Reset (between scenarios)

On the control page, click **Clear** on every backend and set the rate to
`400`. Put `algorithm: "round_robin"` back in `lb.yaml` and send SIGHUP. Wait
about 20 s, until every backend shows about 25 %. After the flaky-backend
scenario, wait up to 40 s for the circuit to close (30 s cooldown,
ADR-0012).

---

## Scenario 1 — Baseline: round robin, every backend fast

| | |
|---|---|
| **Do** | Nothing; this is the reset state. |
| **Wait** | 20 s |
| **Expected** | Share 25 / 25 / 25 / 25 %. p99 about 5 ms on every backend. Health all green, circuit all closed. The client totals show `errors 0`. |
| **Narrate** | "Round robin sends requests to each backend in turn. Four equal backends get a quarter each." |

## Scenario 2 — Hot reload to P2C-EWMA with one slow backend (the centrepiece)

| | |
|---|---|
| **Do** | backend3 row: delay `200` → **Apply**. Wait 25 s. Then in `lb.yaml` set `algorithm: "p2c_ewma"`, save, and send SIGHUP. |
| **Before the reload** | backend3 still 25 % (round robin ignores latency). Its p99 bar reads about 250 ms (bucket effect: 200 ms injected), the others about 5 ms. |
| **Right after the reload** | The log prints `"config reloaded" … "previous_algorithm":"round_robin","algorithm":"p2c_ewma"`. The header shows `Algorithm: P2C-EWMA` and `Last reload: SUCCESS`. A dashed marker appears on the share chart. |
| **Wait** | 30–45 s |
| **Expected** | backend3's share falls to about 0 % and the other three settle near 33 % each. The "Failed" tile and the clients' `errors` do not move. Measured: backend3 0.00 % (acceptance), 0.0 % and 32.8 / 35.0 / 32.3 % in the S5.T20.7 run, with 0 client errors across the reload. |
| **Narrate** | "I change one line in the config and send a reload signal. The same process keeps serving. Requests already in flight finish where they are; new requests use power-of-two-choices, which compares two backends and takes the one with lower recent latency. Backend3 is slow, so it stops getting traffic. No request failed." |
| **ADRs** | ADR-0015 decisions 18–21; ADR-0010 (P2C-EWMA). |

## Scenario 3 — Hot reload back to round robin

| | |
|---|---|
| **Do** | Set `algorithm: "round_robin"`, save, SIGHUP. |
| **Wait** | 20 s |
| **Expected** | Marker, `SUCCESS`, header `Round robin`. backend3 back to 25 %, because round robin ignores latency. Measured: 96,213 requests in the 30 s around the reload, 0 client errors. |
| **Narrate** | "Back to round robin, again with no restart. Backend3 gets its quarter again. The algorithm was the only thing that changed." |

Click **Clear** on backend3 afterwards.

## Scenario 4 — A reload the LB must reject

| | |
|---|---|
| **Do** | Click **Try a reload with an invalid config**. |
| **Expected** | Page message: `Rejected by lb as expected: config: unsupported algorithm "p3c_ewma"`. Header: `Last reload: REJECTED at hh:mm:ss — config: unsupported algorithm "p3c_ewma"`. The algorithm and the share chart do not change. Measured 2026-10-07. |
| **Narrate** | "The load balancer checks every config before it uses it. This one names an algorithm that does not exist. It is rejected, and the running config stays." |
| **Terminal variant** | Put `algorithm: "p3c_ewma"` in `lb.yaml` and send SIGHUP. The log shows `config_reload_failed`, `reason: validation_error`. Put the file back afterwards. |
| **ADRs** | ADR-0015 decisions 4, 13–16; ADR-0025 decision 13. |

## Scenario 5 — Other algorithms by hot reload (optional, *rehearsed earlier*)

- **least_conn.** With backend3 at 200 ms, backend3 drops to about 1 %. The
  other three are uneven (rehearsed: 78 / 18 / 2 %), because at this rate the
  in-flight counts sit at 0–1 and ties break in registry order. Do not promise
  an even split.
- **consistent_hash.** All backends fast: a skewed but steady split
  (rehearsed: 52.8 / 5.6 / 41.9 / 0.1 %). Each of the eight clients is pinned
  to one backend, and the bounded variant spills the hottest key's excess
  (ADR-0008, ADR-0009).

## Scenario 6 — Flaky backend: outlier detection and the circuit breaker (*rehearsed earlier*)

| | |
|---|---|
| **Do** | backend2: fail rate `0.5` → **Apply**. |
| **Expected** | Within about 3 s backend2's share collapses toward 0 and it shows Unhealthy (passive outlier detection). Its circuit shows Open about 6 s after the injection. The other three take about 33 % each. |
| **Recovery** | **Clear** backend2. Healthy in about 10 s; the circuit closes within about 40 s (30 s cooldown). |
| **ADRs** | ADR-0011, ADR-0012. |

## Scenario 7 — Stop and start a backend

| | |
|---|---|
| **Do** | backend1: **Stop** (abrupt kill). Wait 20 s. Then **Start**. Wait 30 s. |
| **Expected** | backend1 shows `exited`, Unhealthy within about 5 s, share 0 within about 15 s, circuit Open. A handful of requests in flight at the kill fail (measured 2026-10-07 on the previous shape: 1; earlier rehearsal: 5). After Start: healthy in about 10 s, about 25 % by about 20 s. |
| **Narrate** | "The kill is abrupt. A few requests in flight fail, and the load balancer never retries them, because a retry could run a request twice (ADR-0018). The other backends take the traffic, and backend1 comes back after two good health checks." |

## Scenario 8 — Backend-list reload with drain (optional)

| | |
|---|---|
| **Do** | Delete backend4's two lines from `lb.yaml`, save, SIGHUP. Wait 15 s. Then put them back and SIGHUP again. |
| **Expected** | Log: `added 0, removed 1, unchanged 3`, then `added 1, removed 0, unchanged 3`. backend4 falls to 0 % and the other three take about 33 %. Then backend4 returns after one good probe. Zero client errors. |
| **Narrate** | "The backend list reloads the same way. A removed backend finishes the requests it already has, then stops getting new ones (ADR-0016)." |

## Known behaviours — state these, never contradict

- **P2C-EWMA** picks two random backends and takes the lower latency average.
  With one slow backend and three equal ones, the three split evenly. With four
  *different* latencies the split tends to 50 / 33 / 17 / 0 % (rehearsed
  earlier with 0 / 50 / 100 / 200 ms).
- **EWMA has no decay** and is fed only by proxied responses. While P2C is
  active, a backend that was slow can stay starved after it recovers. A reload
  to another algorithm sends it traffic again, which refreshes its average
  (ADR-0023 decision 5).
- **least_conn** breaks ties in registry order, so at this rate it favours
  `backend1`.
- **A reload never restarts the process.** Only a config the LB cannot accept
  is rejected, and then nothing changes.

## Teardown

```sh
make demo-down
```

Put `demo/configs/lb.yaml` back to `algorithm: "round_robin"` (`git diff
demo/configs/lb.yaml` must be empty). Demo charts are not benchmark results;
`RESULTS.md` remains the only published performance evidence.
