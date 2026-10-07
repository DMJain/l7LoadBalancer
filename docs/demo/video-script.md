# Demo video: shoot script and shot list

The recording plan for the S5.T17.2 walkthrough: about 6 to 7 minutes, a
screen recording with voiceover in one continuous take, trimmed only at the
ends. It runs on the one-LB demo stack (`demo/`, S5.T20.8) and follows
[`demo/DEMO-SCRIPT.md`](../../demo/DEMO-SCRIPT.md). The centrepiece is the one
you asked for: change the selection algorithm in the config file, send a
reload, and the running load balancer switches with no restart and no failed
requests (ADR-0015 decisions 18–21).

Every spoken number is sourced. Numbers that can only be read off the screen at
record time are written `<pull from dashboard at record time>`. Measured values
are from 2026-10-07 unless marked.

---

## Section 1: Pre-flight checklist

Start 30 minutes before you record. Run commands from the repository root.

1. **Check Docker memory and ports.**
   - `docker info --format '{{.MemTotal}}'` must print at least `8000000000`.
   - `docker compose -f bench/docker-compose.yml down; docker compose down` stops the other stacks.
   - `lsof -nP -iTCP:8080 -iTCP:3000 -iTCP:8095 -sTCP:LISTEN` must print nothing.
2. **Quit other apps**: browsers, chat, IDE indexers, backups. Turn on Do Not Disturb.
3. **Start the stack.** Run `make demo-up`. Then run
   `docker compose -f demo/docker-compose.yml ps --format '{{.Name}}\t{{.Status}}'`.
   Expect 16 rows, all `Up`, and `lb` reading `(healthy)`.
4. **Run the acceptance check.** `demo/acceptance.sh` must end with
   `All checks passed.` Phase 2 is a hot reload with 0 client errors. Do not
   record if it fails.
5. **Confirm the config file.** `grep ^algorithm demo/configs/lb.yaml` must
   print `algorithm: "round_robin"`.
6. **Open the browser tabs** in one window:
   1. `https://github.com/DMJain/l7LoadBalancer`, at the README's "How a request flows" diagram.
   2. `http://127.0.0.1:8095/`, the control page with the embedded dashboard, zoomed to 110 %.
7. **Check the page.**
   - The header reads `Algorithm: Round robin (round_robin on lb)`.
   - The load reads `now: 400 req/s`.
   - Every backend row reads `0 / 0 / 0` and `running`.
   - The dashboard shows 4 bands of about 25 % each.
8. **Set up the terminals** (Section 4). Use 18 pt font. Open `demo/configs/lb.yaml` in the editor, with the cursor on the `algorithm:` line.
9. **Dry run, off camera** (about 4 minutes): do Scenes 4 and 5 once, then reset.
   1. Click **Try a reload with an invalid config**.
   2. Set backend3's delay to `200` and click **Apply**. Change the file to `p2c_ewma`, save, and send SIGHUP.
   3. Wait 45 s. Confirm backend3 is about 0 %.
   4. Change the file back to `round_robin`, save, and send SIGHUP. **Clear** backend3.
   5. **Stop** backend1, wait 20 s, **Start** it, and wait 30 s.
10. **Reset for the take.**
    - Run `docker compose -f demo/docker-compose.yml restart lb`, so the header reads `Last reload: no reload yet` and the client error counter starts from a clean point.
    - Wait 30 s, then confirm `algorithm: "round_robin"` in the file.
    - `restart` is a reset step before the take, not part of the demo.
11. **Warm up.** Let it run at 400 req/s for 10 minutes. Do not raise the rate.
12. **Two minutes before recording:** set backend3's delay to `200` and click
    **Apply**. Expect backend3's p99 bar near 250 ms, and backend3 still at 25 %.
13. **Audio check.** Record this line and play it back: "The load balancer
    sends each request to 1 of 4 backends." Listen for clipping and hum.
14. **Start the recorder** (1080p or 1440p, 30 fps, microphone on). Wait 3 s, then start talking.

---

## Section 2: Shot list and scene breakdown

### Scene 1: Hook (0:00–0:30)

**On screen:** Control page, header and dashboard visible, traffic flowing.

**Voiceover:**
> This is a Layer 7 load balancer that I built in Go.
> Layer 7 means that it reads each HTTP request before it chooses where to send it.
> Now, 8 test clients send traffic through it to 4 backends.
> A backend is a service that does the work for a request.
> Later in this video, I change how it picks a backend while traffic flows.
> I do not restart it.

**Director's notes:** Pointer still. On "4 backends", trace the four bands of the traffic-share chart.

### Scene 2: What it is (0:30–1:15)

**On screen:** GitHub README, scrolling slowly from the top.

**Voiceover:**
> A selection algorithm decides which backend gets each request.
> This load balancer has 4 selection algorithms.
> Round robin sends requests to each backend in turn.
> Power-of-two-choices compares 2 random backends and picks the faster one.
> The load balancer checks the health of each backend.
> A circuit breaker stops traffic to a backend that keeps failing.
> It gives the backend time to recover.
> I can change the backend list and the algorithm while it runs.
> Each design choice has a decision record: a short document with the choice and the reason.

**Director's notes:** Sources: README; ADR index (25 records).

### Scene 3: How a reload works (1:15–2:30)

**On screen:** README "How a request flows" diagram, browser zoom 150 %.

**Voiceover:**
> Follow one request from the top of this diagram.
> The handler asks the selector for a backend.
> The selector is the part of the code that runs the selection algorithm.
> Each request asks the selector only once, at its start.
> That fact makes a safe reload possible.
> SIGHUP is a signal that I send to the running process.
> It tells the load balancer to read its config file again.
> The load balancer builds the new selector first.
> Then it swaps the new selector in with one atomic step.
> Requests already in flight keep the backend that they have.
> New requests use the new selector.
> The listener stays open, so no connection is reset.
> A bad config is rejected, and the old config keeps serving.

**Director's notes:** Point at the "pick a backend" step, then the "hand over request" step. Sources: ADR-0015 decisions 18–19, ADR-0007.

### Scene 4: Live demo, the hot reload (2:30–5:00), the centrepiece

#### 4A. Current state (2:30–2:50)

**On screen:** Control page.

**Voiceover:**
> The header shows the algorithm that the load balancer reports: round robin.
> Before I started recording, I added a 200 millisecond (ms) delay to backend3.
> Round robin ignores latency, so backend3 still gets a quarter of the requests.
> p99 latency is the time that 99 of every 100 requests stay under.
> Backend3's p99 is about `<pull from dashboard at record time>` ms.

**Director's notes:** Point at the header, backend3's delay `200`, the share chart (4 bands at about 25 %) and the p99 bars. Expected: backend3 about 250 ms, the others about 5 ms.

#### 4B. A reload the load balancer must reject (2:50–3:20)

**On screen:** Control page.

**Voiceover:**
> First, a bad config.
> This button sends a config with an algorithm that does not exist.
> The load balancer rejects it.
> The header shows the error, and the algorithm is still round robin.
> The traffic does not change.

**Director's notes:** Click **Try a reload with an invalid config** on "This button". Expected: `Last reload: REJECTED … unsupported algorithm "p3c_ewma"`; the share chart is unchanged. Source: ADR-0015 decisions 13–16.

#### 4C. The hot reload (3:20–3:50)

**On screen:** Editor (left) and terminal (right) over the browser.

**Voiceover:**
> Now a real change.
> In the config file, I change the algorithm from round robin to p2c_ewma.
> EWMA means exponentially weighted moving average, an average of recent latency.
> I save the file and send SIGHUP.
> The log shows the reload: from round robin to p2c_ewma.

**Director's notes:** Edit the `algorithm:` line and save. Run the SIGHUP command (one up-arrow). Highlight the log line `"previous_algorithm":"round_robin","algorithm":"p2c_ewma"`. Start a stopwatch.

#### 4D. Watch it change (3:50–4:40)

**On screen:** Control page.

**Voiceover:**
> The header now shows P2C-EWMA, and the last reload is a success.
> A marker shows the moment of the change on the chart.
> The process did not restart.
> The algorithm compares 2 backends and sends the request to the one with lower latency.
> Backend3 is slow, so it loses.
> `<wait until 30 s on the stopwatch>`
> Backend3 is now at `<pull from dashboard at record time>` percent.
> The other 3 backends share the traffic.
> The failed count is still 0.
> No client request failed during the change.

**Director's notes:** Point at the header, the marker, backend3's band shrinking, the "Failed" tile, and the clients line (`errors`). Expected: backend3 about 0 %, the others about 33 %, errors unchanged. Measured: 0.00 % and 0 client errors (acceptance); 0 errors in 96,213 requests around a reload. Do not claim the result before 30 s.

#### 4E. Back again (4:40–5:00)

**Voiceover:**
> I change it back to round robin and send SIGHUP again.
> Backend3 gets its quarter again.
> The backends did not change. Only the algorithm did.

**Director's notes:** Edit, save, SIGHUP. Point at the second marker. Then click **Clear** on backend3.

### Scene 5: Failure (5:00–5:45)

**On screen:** Control page, scrolled so the health and circuit tiles show.

**Voiceover:**
> Next, a failure.
> Now I stop backend1.
> The health check fails, and the load balancer marks backend1 red.
> Its circuit breaker opens, and the other 3 backends take the traffic.
> A few requests that were in flight fail.
> The load balancer never retries a request, because a retry can run it twice.
> Now I start backend1 again.
> After 2 good health checks, it gets traffic again.

**Director's notes:** **Stop** on backend1; wait for red (about 5 s) and the circuit Open; read the failed count. **Start**; wait for green (about 10 s) and 25 % (about 20 s). Measured 2026-10-07 on the previous shape: circuit Open, 1 failed request. Source: ADR-0011, ADR-0018.

### Scene 6: Close (5:45–6:20)

**On screen:** README "Headline results" table.

**Voiceover:**
> I also measured it against Nginx, a widely used open-source proxy.
> Over HTTP/1.1 with small responses, Nginx reached 11,500 requests per second.
> My load balancer reached 8,000.
> The results page states this, with the method and its limits.
> The code and the decision records are on GitHub, at DMJain slash l7LoadBalancer.
> This project shows how I work: I write down each decision and test each claim.

**Director's notes:** Point at the 200 B row. Source: `RESULTS.md` protocol table. Single run, order only, no ratio.

---

## Section 3: The script (voiceover only)

Read from this. Angle-bracket cues are for you.

This is a Layer 7 load balancer that I built in Go.
Layer 7 means that it reads each HTTP request before it chooses where to send it.
Now, 8 test clients send traffic through it to 4 backends.
A backend is a service that does the work for a request.
Later in this video, I change how it picks a backend while traffic flows.
I do not restart it.

A selection algorithm decides which backend gets each request.
This load balancer has 4 selection algorithms.
Round robin sends requests to each backend in turn.
Power-of-two-choices compares 2 random backends and picks the faster one.
The load balancer checks the health of each backend.
A circuit breaker stops traffic to a backend that keeps failing.
It gives the backend time to recover.
I can change the backend list and the algorithm while it runs.
Each design choice has a decision record: a short document with the choice and the reason.

Follow one request from the top of this diagram.
The handler asks the selector for a backend.
The selector is the part of the code that runs the selection algorithm.
Each request asks the selector only once, at its start.
That fact makes a safe reload possible.
SIGHUP is a signal that I send to the running process.
It tells the load balancer to read its config file again.
The load balancer builds the new selector first.
Then it swaps the new selector in with one atomic step.
Requests already in flight keep the backend that they have.
New requests use the new selector.
The listener stays open, so no connection is reset.
A bad config is rejected, and the old config keeps serving.

The header shows the algorithm that the load balancer reports: round robin.
Before I started recording, I added a 200 millisecond (ms) delay to backend3.
Round robin ignores latency, so backend3 still gets a quarter of the requests.
p99 latency is the time that 99 of every 100 requests stay under.
Backend3's p99 is about ___ ms.

First, a bad config.
This button sends a config with an algorithm that does not exist.
The load balancer rejects it.
The header shows the error, and the algorithm is still round robin.
The traffic does not change.

Now a real change.
In the config file, I change the algorithm from round robin to p2c_ewma.
EWMA means exponentially weighted moving average, an average of recent latency.
I save the file and send SIGHUP.
The log shows the reload: from round robin to p2c_ewma.

The header now shows P2C-EWMA, and the last reload is a success.
A marker shows the moment of the change on the chart.
The process did not restart.
The algorithm compares 2 backends and sends the request to the one with lower latency.
Backend3 is slow, so it loses.
<wait until 30 s on the stopwatch>
Backend3 is now at ___ percent.
The other 3 backends share the traffic.
The failed count is still 0.
No client request failed during the change.

I change it back to round robin and send SIGHUP again.
Backend3 gets its quarter again.
The backends did not change. Only the algorithm did.

Next, a failure.
Now I stop backend1.
The health check fails, and the load balancer marks backend1 red.
Its circuit breaker opens, and the other 3 backends take the traffic.
A few requests that were in flight fail.
The load balancer never retries a request, because a retry can run it twice.
Now I start backend1 again.
After 2 good health checks, it gets traffic again.

I also measured it against Nginx, a widely used open-source proxy.
Over HTTP/1.1 with small responses, Nginx reached 11,500 requests per second.
My load balancer reached 8,000.
The results page states this, with the method and its limits.
The code and the decision records are on GitHub, at DMJain slash l7LoadBalancer.
This project shows how I work: I write down each decision and test each claim.

### Validation summary

- **Word count:** 656 words of voiceover, about 4:22 at 150 words per minute. The waits in Scene 4D (30 s convergence) and Scene 5 (ejection and recovery) add about 1 minute, so the take runs about 5:30–6:00.
- **Longest sentence:** 16 words. Every sentence has 20 words or fewer (script-checked).
- **Terms defined on first use:** Layer 7 and backend (Scene 1); selection algorithm, circuit breaker and decision record (Scene 2); selector and SIGHUP (Scene 3); ms and p99 (4A); EWMA (4C).
- **Checks:** no banned word or phrase (script-checked); active voice; "backend" is the only word for a backend, and "load balancer" is never shortened in speech; Sections 2 and 3 match line for line (script-checked).

---

## Section 4: Terminal setup

```
+---------------------------------------------+---------------------------+
|                                             |  Editor:                  |
|   Browser                                   |  demo/configs/lb.yaml     |
|   1. GitHub README                          |  (cursor on algorithm:)   |
|   2. Control page 127.0.0.1:8095            +---------------------------+
|      (header, controls, dashboard)          |  Terminal: lb log tail    |
|                                             |  + SIGHUP command ready   |
+---------------------------------------------+---------------------------+
```

Terminal: one tab tails the reload log, the other holds the SIGHUP command.

```sh
docker compose -f demo/docker-compose.yml logs -f --since 1s lb | grep --line-buffered -E 'config_reload|health_|circuit_'
docker compose -f demo/docker-compose.yml kill -s HUP lb
```

Expected lines:
- Reload: `"msg":"config reloaded",…,"previous_algorithm":"round_robin","algorithm":"p2c_ewma"`.
- Stopping a backend: `"event":"health_ejected"`, and `circuit_opened` if the circuit opens.
- Starting it again: `"event":"health_reinstated"`.

Any editor works for `lb.yaml`: the directory is mounted, so a save by rename is still seen (ADR-0023 decision 15).

---

## Section 5: Common failure modes and recovery

| Symptom | Fix |
|---|---|
| The log prints nothing after SIGHUP. | Check that the file saved: `grep ^algorithm demo/configs/lb.yaml`. Check that the container sees it: `docker compose -f demo/docker-compose.yml exec -T lb /l7lb probe http://127.0.0.1:8081/livez`, then send SIGHUP again. |
| The log prints `config_reload_failed`. | A typo in the file. The old config keeps serving, so nothing broke on camera. Say "That is a typo; the load balancer rejects it and keeps its old config." Fix the file and send SIGHUP again. |
| The header still shows the old algorithm after a successful reload. | The page refreshes every 2 s and Prometheus every 1 s. Wait 3 s. |
| backend3 is still above 10 % after 45 s on P2C. | Say "The average is still learning" and move on. |
| The dashboard shows "No data". | `docker compose -f demo/docker-compose.yml restart prometheus`, wait 20 s, reload the page. |
| backend1 does not come back after **Start**. | Click **Start** again, or `docker compose -f demo/docker-compose.yml up -d backend1`. |
| Audio problem found afterwards. | Under 3 s and not on a number: keep the take. Otherwise re-shoot the whole take. |

---

## Section 6: Post-recording checklist

1. Put the file back: `git diff demo/configs/lb.yaml` must print nothing. Then `make demo-down`.
2. Trim only the first and last 2–3 s.
3. Listen to the audio at 1:00, 3:30, 5:00 and the end.
4. Export H.264 at 1080p (12 Mbit/s) or 1440p (20 Mbit/s). Check that the log line in Scene 4C is readable at full screen.
5. Upload to YouTube as **Unlisted**. Keep the raw file out of git.
6. Put the link in `README.md` (`VIDEO-LINK-SLOT`) and `demo/README.md` ("Video pending").
7. Mark S5.T17.2 done in `PROGRESS.md`.
8. LinkedIn draft:

   > Shipped the Sprint 5 demo of my portfolio project: a Layer 7 load balancer in Go.
   >
   > In the video I change the selection algorithm in the config file and send a reload, while traffic flows. The same process switches from round robin to power-of-two-choices, moves traffic off a slow backend, and no client request fails. A bad config is rejected and the old one keeps serving.
   >
   > Benchmarked against Nginx with a reproducible harness. Nginx is ahead over HTTP/1.1, and the results page says so.
   >
   > Video: <YouTube link>
   > Code, decision records and results: https://github.com/DMJain/l7LoadBalancer

---

## Warnings

- **Numbers read on screen:** backend3's p99 (4A) and backend3's share after the reload (4D).
- **Rehearsed on the previous four-LB shape, not yet on this one:** the failure scene's circuit-open and failed-request figures. The dry run in step 9 re-checks them.
- **No deployment URL:** there is none, by decision (ADR-0023 decision 1).
- **The benchmark summary page (S5.T20.5) is not built yet**, so Scene 6 uses the README table.
