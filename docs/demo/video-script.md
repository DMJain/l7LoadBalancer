# Demo video script

The narration and shot list for the demo video: a screen recording with voiceover, about 6 minutes, in one take. The main scene changes the algorithm in the config file and reloads it. The LB switches with no restart and no failed requests (ADR-0015 decisions 18–21). Setup, timings and expected values are in [`demo/DEMO-SCRIPT.md`](../../demo/DEMO-SCRIPT.md). Read the values marked `<read at record time>` from the dashboard during the take.

## Before you record

Start 30 minutes before the take, from the repository root.

1. Stop the other stacks (`docker compose -f bench/docker-compose.yml down; docker compose down`). `lsof -nP -iTCP:8080 -iTCP:3000 -iTCP:8095 -sTCP:LISTEN` must print nothing. Docker needs at least 8 GB of memory.
2. Quit other apps and turn on Do Not Disturb.
3. Do the "Set up" steps in `DEMO-SCRIPT.md`. `docker compose -f demo/docker-compose.yml ps` must show 16 containers `Up`, with `lb` `(healthy)`. Do not record if the acceptance check fails.
4. Open two browser tabs: `https://github.com/DMJain/l7LoadBalancer` at the "How a request flows" diagram, and `http://127.0.0.1:8095/` at 110% zoom.
5. Arrange the screen as in [Screen layout](#screen-layout), with an 18 pt terminal font.
6. Dry run off camera: scenarios 4, 2, 3 and 7 from `DEMO-SCRIPT.md`.
7. Reset: check `algorithm: "round_robin"`, then run `docker compose -f demo/docker-compose.yml restart lb`. The header then reads `Last reload: no reload yet`, and the error counter starts clean. This restart is not part of the demo.
8. Let it run at 400 req/s for 10 minutes.
9. Two minutes before the take, set backend3's delay to `200` and click **Apply**.
10. Record one test line and check it for clipping and hum. Then start the recorder (1080p or 1440p, 30 fps). Wait 3 s before you speak.

## Scenes

Rules for the voiceover: every sentence has 20 words or fewer. "Backend" is the only word for a backend. Say "load balancer" in full. Each term is defined at first use.

### Scene 1: Hook (0:00–0:30)

**Screen:** control page with traffic. **Cue:** at "4 backends", trace the four bands of the share chart.

> This is a Layer 7 load balancer that I built in Go.
> Layer 7 means that it reads each HTTP request before it chooses where to send it.
> Now, 8 test clients send traffic through it to 4 backends.
> A backend is a service that does the work for a request.
> Later in this video, I change how it picks a backend while traffic flows.
> I do not restart it.

### Scene 2: What it is (0:30–1:15)

**Screen:** GitHub README, scroll slowly from the top.

> A selection algorithm decides which backend gets each request.
> This load balancer has 4 selection algorithms.
> Round robin sends requests to each backend in turn.
> Power-of-two-choices compares 2 random backends and picks the faster one.
> The load balancer checks the health of each backend.
> A circuit breaker stops traffic to a backend that keeps failing.
> It gives the backend time to recover.
> I can change the backend list and the algorithm while it runs.
> Each design choice has a decision record: a short document with the choice and the reason.

### Scene 3: How a reload works (1:15–2:30)

**Screen:** README "How a request flows" diagram at 150% zoom. **Cue:** point at the "pick a backend" step, then the "hand over request" step. Sources: ADR-0015 decisions 18–19, ADR-0007.

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

### Scene 4: The hot reload (2:30–5:00)

#### 4A. Current state (2:30–2:50)

**Screen:** control page. **Cue:** point at the header, backend3's delay `200`, the share chart and the p99 bars. Expect backend3 about 250 ms, the others about 5 ms.

> The header shows the algorithm that the load balancer reports: round robin.
> Before I started recording, I added a 200 millisecond (ms) delay to backend3.
> Round robin ignores latency, so backend3 still gets a quarter of the requests.
> p99 latency is the time that 99 of every 100 requests stay under.
> Backend3's p99 is about `<read at record time>` ms.

#### 4B. A rejected reload (2:50–3:20)

**Cue:** click **Try a reload with an invalid config** at "This button". Expect `REJECTED … unsupported algorithm "p3c_ewma"` and no change to the chart. Source: ADR-0015 decisions 13–16.

> First, a bad config.
> This button sends a config with an algorithm that does not exist.
> The load balancer rejects it.
> The header shows the error, and the algorithm is still round robin.
> The traffic does not change.

#### 4C. The reload (3:20–3:50)

**Screen:** editor and terminal over the browser. **Cue:** edit the `algorithm:` line and save. Send SIGHUP (one up-arrow). Highlight `"previous_algorithm":"round_robin","algorithm":"p2c_ewma"`. Start a stopwatch.

> Now a real change.
> In the config file, I change the algorithm from round robin to p2c_ewma.
> EWMA means exponentially weighted moving average, an average of recent latency.
> I save the file and send SIGHUP.
> The log shows the reload: from round robin to p2c_ewma.

#### 4D. The result (3:50–4:40)

**Screen:** control page. **Cue:** point at the header, the marker, backend3's band, the "Failed" tile and the client `errors`. Do not state the result before 30 s on the stopwatch. Expect backend3 about 0% and the others about 33%.

> The header now shows P2C-EWMA, and the last reload is a success.
> A marker shows the moment of the change on the chart.
> The process did not restart.
> The algorithm compares 2 backends and sends the request to the one with lower latency.
> Backend3 is slow, so it loses.
> `<wait until 30 s on the stopwatch>`
> Backend3 is now at `<read at record time>` percent.
> The other 3 backends share the traffic.
> The failed count is still 0.
> No client request failed during the change.

#### 4E. Back again (4:40–5:00)

**Cue:** edit, save, SIGHUP. Point at the second marker. Then click **Clear** on backend3.

> I change it back to round robin and send SIGHUP again.
> Backend3 gets its quarter again.
> The backends did not change. Only the algorithm did.

### Scene 5: Failure (5:00–5:45)

**Screen:** control page, with the health and circuit panels visible. **Cue:** **Stop** backend1. Wait for red (about 5 s) and circuit Open. Read the failed count. **Start** it. Wait for green (about 10 s) and 25% (about 20 s). Sources: ADR-0011, ADR-0018.

> Next, a failure.
> Now I stop backend1.
> The health check fails, and the load balancer marks backend1 red.
> Its circuit breaker opens, and the other 3 backends take the traffic.
> A few requests that were in flight fail.
> The load balancer never retries a request, because a retry can run it twice.
> Now I start backend1 again.
> After 2 good health checks, it gets traffic again.

### Scene 6: Close (5:45–6:20)

**Screen:** README benchmark table. **Cue:** point at the 200 B row. Source: `RESULTS.md` protocol table, HTTP/1.1, single run. State the order only, not a ratio.

> I also measured it against Nginx, a widely used open-source proxy.
> Over HTTP/1.1 with small responses, Nginx reached 11,500 requests per second.
> My load balancer reached 8,000.
> The results page states this, with the method and its limits.
> The code and the decision records are on GitHub, at DMJain slash l7LoadBalancer.
> This project shows how I work: I write down each decision and test each claim.

The voiceover has 656 words, about 4:22 at 150 words per minute. The waits in Scenes 4D and 5 add about 1 minute.

## Screen layout

```
+---------------------------------------------+---------------------------+
|                                             |  Editor:                  |
|   Browser                                   |  demo/configs/lb.yaml     |
|   1. GitHub README                          |  (cursor on algorithm:)   |
|   2. Control page 127.0.0.1:8095            +---------------------------+
|                                             |  Terminal: lb log tail    |
|                                             |  + SIGHUP command ready   |
+---------------------------------------------+---------------------------+
```

The terminal commands are in the "Set up" section of `DEMO-SCRIPT.md`.

## If something goes wrong

| Symptom | Fix |
|---|---|
| No log line after SIGHUP. | Check the save with `grep ^algorithm demo/configs/lb.yaml`, then send SIGHUP again. |
| `config_reload_failed` in the log. | A typo. The old config keeps serving. Say so on camera, fix the file and send SIGHUP again. |
| The header shows the old algorithm. | The page refreshes every 2 s and Prometheus scrapes every 1 s. Wait 3 s. |
| backend3 above 10% after 45 s on P2C. | Say "The average is still learning" and continue. |
| The dashboard shows "No data". | `docker compose -f demo/docker-compose.yml restart prometheus`, wait 20 s, reload the page. |
| backend1 stays down after **Start**. | Click **Start** again, or `docker compose -f demo/docker-compose.yml up -d backend1`. |
| An audio fault. | Under 3 s and not on a number: keep the take. Otherwise record again. |

## After you record

1. Do the "Teardown" steps in `DEMO-SCRIPT.md`.
2. Trim only the first and last 2–3 s. Listen to the audio at 1:00, 3:30, 5:00 and the end.
3. Export H.264 at 1080p (12 Mbit/s) or 1440p (20 Mbit/s). Check that the log line in 4C is readable.
4. Upload to YouTube as **Unlisted**, and add the link to the demo section of `README.md`. Do not put the raw file in git.

The failure-scene figures come from the earlier four-LB rig. The dry run checks them on this stack. There is no public deployment URL (ADR-0023 decision 1).
