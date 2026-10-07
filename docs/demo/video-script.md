# Demo video: shoot script and shot list

The recording plan for the S5.T17.2 walkthrough video: 5 to 7 minutes, a screen recording with voiceover in one continuous take, trimmed only at the ends. It is built on the local demo stack under `demo/` and on [`demo/DEMO-SCRIPT.md`](../../demo/DEMO-SCRIPT.md). Every expected state below comes from that script's rehearsal record (2026-10-02). Every quoted number comes from that record, [`RESULTS.md`](../../RESULTS.md), the README, or an ADR. A number that has to be read off the screen at record time is shown as `<pull from dashboard at record time>`.

## Read first: where this script departs from the brief

The brief described this workflow: edit `algorithm` in a config file, send `SIGHUP`, and watch the traffic move, with `vegeta` or `wrk` making the background load and a public deployment URL at the end. Three parts of that cannot work against this repository. The script keeps as much of the workflow as it can and changes only those parts.

1. **SIGHUP cannot change the algorithm.** If a reload changes `algorithm`, the whole reload is rejected. The process keeps its old config and logs `config_reload_failed` with `reason: non_backend_change` and `fields: ["algorithm"]` ([ADR-0015](../adr/0015-reload-architecture.md) decision 4; `internal/config/diff.go`, `internal/app/app.go`). [ADR-0023](../adr/0023-local-live-demo.md) lists "switch algorithm by config rewrite + SIGHUP" as a rejected option. The demo runs four load balancers instead, one per algorithm, all sharing four backends. A switch moves the eight traffic clients to a different load balancer from the control page.
   **What the script does:** the edit-and-SIGHUP step stays in Scene 4 as a short beat where the rejection is the point being shown ("only the backend list reloads"). The traffic switch that follows uses the control page's **Switch active LB** button.
2. **No vegeta or wrk.** The demo has its own background load: eight traffic-generator containers sending 400 req/s in total ([ADR-0024](../adr/0024-demo-traffic-generator-model.md)). An extra vegeta run would bypass the control-page switch and would distort the shares. Port 8080 is also taken by `lb-roundrobin`.
3. **No deployment URL.** There is no public deployment, by decision ([ADR-0023](../adr/0023-local-live-demo.md) decision 1). Scene 6 points to the GitHub repository and `make demo-up` instead. This is settled, not pending.

Smaller changes: `docker stop backend-A` becomes `docker stop l7loadbalancer-demo-backend1` (the real container name). The backend made slow is `backend3`, the one the acceptance check uses.

---

## Section 1: Pre-flight checklist

Start 30 minutes before you record. Run every command from the repository root.

1. **Docker is running and has enough memory.**
   `docker info --format '{{.MemTotal}}'`
   Expect a number at or above `8000000000` (8 GiB, what the bench rig used). The demo starts 18 containers.

2. **Nothing else holds the demo ports.** The bench rig and the root stack both use `:8080` and `:3000`.
   `docker compose -f bench/docker-compose.yml down; docker compose down`
   `lsof -nP -iTCP:8080 -iTCP:3000 -iTCP:8095 -sTCP:LISTEN`
   Expect no output from `lsof`.

3. **Quit competing apps**: other browsers, Slack, IDE indexers, backup tools, other Docker workloads. Turn on Do Not Disturb (macOS: Control Centre → Focus → Do Not Disturb).

4. **Start the stack.**
   `make demo-up`
   Expect `docker compose ... up -d --build` to end with lines like `✔ Container l7loadbalancer-demo-backend1  Started` and no `Error`. The first build takes a few minutes.

5. **Check that every container is up.**
   `docker compose -f demo/docker-compose.yml ps --format '{{.Name}}\t{{.Status}}'`
   Expect 18 rows, all `Up`. The four `lb-*` rows read `Up ... (healthy)`.

6. **Run the acceptance check. Do not record if it fails.**
   `demo/acceptance.sh`
   Expect it to end with `All checks passed.` The check slows backend3, confirms that only backend3 is slow, and confirms that p2c-ewma moves traffic off it. Its exit trap restores every backend and the generators.

7. **Back up the config file you will edit on camera.**
   `cp demo/configs/roundrobin.yaml /tmp/roundrobin.orig.yaml`

8. **Make sure your editor writes the file in place.** The config is a single-file bind mount. An editor that saves by writing a new file and renaming it leaves the container reading the old file.
   `ls -i demo/configs/roundrobin.yaml` and note the number.
   Open it with `vim -c 'set backupcopy=yes' demo/configs/roundrobin.yaml`, save with `:w`, quit, and run `ls -i` again.
   Expect the same inode number. If it changed, re-run step 7's `cp` in reverse (`cp /tmp/roundrobin.orig.yaml demo/configs/roundrobin.yaml`), then `docker compose -f demo/docker-compose.yml restart lb-roundrobin`, and use `nano` instead.

9. **Open the browser tabs** (one window, in this order):
   1. `https://github.com/DMJain/l7LoadBalancer`, scrolled to the "How a request flows" diagram in the README.
   2. Control page: `http://127.0.0.1:8095/`
   3. Grafana: `http://127.0.0.1:3000/d/l7loadbalancer/?var-window=15s&var-lb=lb-roundrobin&from=now-15m&to=now&refresh=5s`
   Set browser zoom to 125% on the control page and Grafana so the chart labels read at 1080p.

10. **Check the control page baseline.** Expect `active: lb-roundrobin`, `now: 400 req/s`, every backend row at `sleep_ms 0 jitter_ms 0 fail_rate 0`, `container: running`. The generator counters read `offered` ≈ `sent` and `dropped 0`.

11. **Check the Grafana panels.** The dashboard `l7LoadBalancer` has six panels: *Request rate by backend / status_class*, *Request latency p50 / p99 by backend*, *Circuit state*, *Backend healthy*, *Active connections by backend*, *Request share by backend (selected LB)*. Expect all four backends in each panel, *Backend healthy* at 1 for all four, *Circuit state* all closed. Scroll so that *Backend healthy* and *Circuit state* both show without scrolling. Scene 5 uses them.

12. **Set up the terminals** as described in Section 4. Font size 18 pt. Run `clear` in each.

13. **Dry run, off camera** (about 4 minutes). Do Scene 4 and Scene 5 once, with no narration:
    - backend3 row: `sleep_ms 200` → **Apply**. Wait 30 s.
    - In Terminal 2, change `round_robin` to `p2c_ewma`, save, and send SIGHUP (Section 4 commands). Confirm Terminal 1 prints `config_reload_failed` with `"fields":["algorithm"]`.
    - Restore: `cp /tmp/roundrobin.orig.yaml demo/configs/roundrobin.yaml`.
    - Control page: Active LB `lb-p2c-ewma` → **Switch active LB**. Wait 45 s. Confirm backend3's share drops to near 0%.
    - Switch back to `lb-roundrobin`. backend3 row: `sleep_ms 0` → **Apply**.
    - `docker stop l7loadbalancer-demo-backend1`, wait 20 s, `docker start l7loadbalancer-demo-backend1`, wait 30 s.
    - Time each wait with a stopwatch. These waits are where your narration goes.

14. **Reset for the take.** Control page: every backend `0 / 0 / 0` → **Apply**, rate `400` → **Set rate**, Active LB `lb-roundrobin` → **Switch active LB**. Confirm `diff /tmp/roundrobin.orig.yaml demo/configs/roundrobin.yaml` prints nothing. Then press `Ctrl-C` in Terminal 1 and start its command again, so the dry run's log lines are off screen.

15. **Warm up.** Leave the stack running at 400 req/s for 10 to 15 minutes. Do not raise the rate. The generator delivers about 370 of the configured 400 req/s on this machine and falls further behind above about 800 (DEMO-SCRIPT rehearsal finding 2).

16. **Make backend3 slow, 2 minutes before you record.** backend3 row: `sleep_ms 200`, `jitter_ms 0`, `fail_rate 0` → **Apply**. Wait at least 60 s so the 15 s chart window fills. Expect backend3's p50 near 175 ms on the latency chart and the shares still about 25% each. (The chart reads about 175 ms for a 200 ms delay because percentiles come from fixed histogram buckets.)

17. **Audio check.** Record 20 s of yourself reading this line at speaking volume, then play it back:
    "The load balancer sends each request to 1 of 4 backends. Backend3 has a 200 millisecond delay."
    Listen for clipping on "backend" and "delay", fan or air-conditioner hum, and keyboard noise. Keep the peak at about −6 dB.

18. **Final framing.** Show the control page tab. Close notifications. Start the screen recorder (1080p or 1440p, 30 fps, system audio off, microphone on). Wait 3 s, then start talking.

---

## Section 2: Shot list and scene breakdown

Total: about 7:00 to 7:30 (see the validation summary at the end of Section 3 for the trim order). The timestamps below are targets for pacing, and the take will run a little behind them. The waits after each switch are fixed by the algorithms, so let the narration fill them. Never cut a wait short.

### Scene 1: Hook (0:00 to 0:30)

**On screen:** Control page tab. Traffic is running. The three embedded charts show four backends, and backend3's latency line sits far above the others.

**Voiceover:**
> This is a Layer 7 load balancer that I built in Go.
> Layer 7 means that it reads each HTTP request before it chooses where to send it.
> Now, 8 test clients send traffic through it to 4 backends.
> A backend is a service that does the work for a request.
> I built this project to show how I design, test, and measure a backend system.
> Later in this video, I change how it picks a backend while traffic flows.

**Director's notes:** Leave the mouse still. On "4 backends", move the pointer across the four legend entries on the request-share chart once. No other clicks.

### Scene 2: What it is (0:30 to 1:30)

**On screen:** Switch to the GitHub tab, at the top of the README. Scroll slowly through the first paragraph and the "How a request flows" text. Stop above the diagram.

**Voiceover:**
> A load balancer receives requests from clients.
> It sends each request to one of several backends.
> A selection algorithm decides which backend gets each request.
> The selector is the part of the code that runs that algorithm.
> This load balancer has 4 selection algorithms.
> Round-robin sends requests to each backend in turn.
> Least-connections picks the backend with the fewest requests in progress.
> Consistent hashing sends the same client to the same backend each time.
> Power-of-two-choices compares 2 random backends and picks the faster one.
> The load balancer accepts HTTP/1.1 and HTTP/2.
> HTTP/2 sends many requests over one connection.
> It checks each backend in two ways.
> It sends a test request to each backend at a fixed interval.
> It also watches for errors on real traffic.
> Each backend also has a circuit breaker.
> A circuit breaker stops traffic to a backend that keeps failing.
> It gives the backend time to recover.
> The load balancer also exports metrics to Prometheus, a tool that stores metrics.
> I also wrote down what this project leaves out.
> It has no rate limiting, no security review, and no multi-region test.
> Each design choice has a decision record: a short document that states the choice and the reason.
> The project has 25.

**Director's notes:** Scroll at reading pace, not faster. On "decision record", scroll to the "Read more" list at the bottom of the README and hold on the "ADR index" link for 2 s. Sources: four algorithms, HTTP/1.1 and HTTP/2, health checks, circuit breakers and reload come from the README. The scope list comes from [ADR-0005](../adr/0005-scope-of-production-grade.md). "25" is ADR-0001 to ADR-0025 in `docs/adr/INDEX.md`.

### Scene 3: Architecture walkthrough (1:30 to 2:45)

**On screen:** Same GitHub tab, scrolled so the "How a request flows" sequence diagram fills the screen. If the diagram renders small, zoom the browser to 150% for this scene.

**Voiceover:**
> Follow one request from the top of this diagram.
> The client sends a request to the proxy handler.
> The handler asks the selector for a backend.
> If a backend is available, the handler adds 1 to its count of requests in progress.
> The Go reverse proxy then sends the request to the backend.
> Four design decisions matter most here.
> First, the selector is an interface.
> Each algorithm implements it.
> So I can add an algorithm with no change to the request path.
> Second, the count of requests in progress.
> The handler wraps the response body.
> When the body closes, the count goes down by 1.
> A guard in the code makes sure that this decrease happens exactly once.
> If it happened twice, least-connections would read the wrong count.
> Third, the config reload.
> SIGHUP is a signal that I send to the running process.
> It tells the load balancer to read its config file again.
> Then it swaps to a new backend list in one atomic step.
> Requests in progress finish on the old list.
> New requests use the new list.
> In the benchmark, 2 reloads ran during steady traffic.
> Both reloads had 0 errors.
> Fourth, health checks and the circuit breaker.
> A backend gets traffic only if it is healthy and its circuit allows traffic.
> The circuit breaker never changes the health flag.
> So the two parts never overwrite each other's decisions.

**Director's notes:** Move the pointer down the diagram as you name each step: client, proxy handler, selector, the `503` branch, `active count +1`, reverse proxy, backend. On "the count goes down by 1", point at `body closed, count -1`. Sources: request path from `docs/architecture.md` and [ADR-0007](../adr/0007-proxy-request-lifecycle-and-exactly-once-decrement.md) (the guard is a `sync.Once`); reload from [ADR-0015](../adr/0015-reload-architecture.md); "2 reloads, 0 errors" is the no-op and drain reload rows in `RESULTS.md` (both PASS, 0 non-2xx, 0 transport errors); health and circuit from [ADR-0011](../adr/0011-health-passive-outlier-and-circuit-breaker-composition.md) decision 1.

### Scene 4: Live demo, selector switch with traffic flowing (2:45 to 5:00), the centrepiece

Six beats. The control page is the main view: its charts follow the active load balancer automatically, so you never touch the Grafana `$lb` dropdown in this scene.

#### 4A. Current state (2:45 to 3:05)

**On screen:** Control page tab.

**Voiceover:**
> This is the control page for the demo.
> Round-robin is the active algorithm now.
> Before I started recording, I added a 200 millisecond (ms) delay to backend3.
> Round-robin ignores latency.
> So each backend still gets about 25 percent of the requests.
> p99 latency is the time that 99 of every 100 requests stay under.
> `<look at the latency chart — read backend3's p99 and one fast backend's p99>`
> Backend3's p99 is about `<pull from dashboard at record time>` ms.
> The other backends are at about `<pull from dashboard at record time>` ms.

**Director's notes:** Point at the `active: lb-roundrobin` label, then at the backend3 row's `sleep_ms 200`, then at the request-share chart (four lines near 25%), then at the latency chart. Expected (rehearsal 2a): shares 25 / 25 / 25 / 25%; backend3 p50 about 175 ms; the others about 2.5 ms.

#### 4B. Edit and SIGHUP: the rejection (3:05 to 3:30)

**On screen:** Terminal 2 (the editor) on the left half, Terminal 1 (the load balancer log) on the right half.

**Voiceover:**
> I change the algorithm in the config file from round_robin to p2c_ewma.
> Then I send SIGHUP.
> `<wait for the log line in Terminal 1>`
> The load balancer rejects the reload.
> The log names the field that changed: algorithm.
> I designed it to work this way.
> A reload can change the backend list only.
> Other settings feed state that the process already built.
> So the load balancer rejects the whole reload and keeps its old config.
> I put the file back.

**Director's notes:** In Terminal 2, vim is already open on `demo/configs/roundrobin.yaml` with the cursor on the `algorithm:` line. Type `cw` and change `"round_robin"` to `"p2c_ewma"`, then `:wq`. Run the SIGHUP command from Section 4 (up-arrow to it). Terminal 1 prints, within about 1 s:
`... "msg":"config reload failed","event":"config_reload_failed","reason":"non_backend_change","fields":["algorithm"]`
Highlight it with the mouse. On "I put the file back", run `cp /tmp/roundrobin.orig.yaml demo/configs/roundrobin.yaml`. Do not send SIGHUP again: the rejected reload changed nothing. Point to the control page's request-share chart afterwards if time allows: round-robin is still active and still at about 25% each. Source: [ADR-0015](../adr/0015-reload-architecture.md) decision 4.

#### 4C. The real switch (3:30 to 3:45)

**On screen:** Control page tab.

**Voiceover:**
> So the demo runs 4 load balancers, one for each algorithm.
> All 4 share the same 4 backends.
> To change the algorithm, I send the 8 clients to a different load balancer.
> Now I switch to power-of-two-choices with EWMA.
> EWMA means exponentially weighted moving average.
> It is an average of recent latency, where new samples count more than old ones.
> My prediction is that backend3 gets almost no traffic.
> The other 3 backends share the traffic evenly.

**Director's notes:** Select `lb-p2c-ewma` in the **Active LB** list and click **Switch active LB** on the words "Now I switch". Confirm the label changes to `active: lb-p2c-ewma`. Start a stopwatch.

#### 4D. Wait and read the result (3:45 to 4:30)

**On screen:** Control page tab. The charts redraw for the new load balancer.

**Voiceover:**
> The algorithm picks 2 backends at random.
> It sends the request to the one with the lower average latency.
> Each load balancer measures latency only on the requests that it sends.
> So the average needs samples before it is useful.
> The charts use a 15 second window, so they also need time to fill.
> `<look at the request-share chart — wait until backend3's line is near 0>`
> Backend3 is now at `<pull from dashboard at record time>` percent.
> The other 3 backends are at about `<pull from dashboard at record time>` percent each.
> Their latencies are equal, so the algorithm chooses between them at random.
> The load balancer moved traffic away from the slow backend with no restart and no config change.

**Director's notes:** Do not claim the result before the stopwatch reads 30 s. Allow up to 45 s. Point at backend3's line falling on the request-share chart. Expected (rehearsal 2c): 32.2 / 34.2 / 0.0 / 35.3% for backend1 / 2 / 3 / 4. If backend3 is still above 10% at 45 s, say the line from 4D's fallback in Section 5 and move on. Sources: [ADR-0010](../adr/0010-p2c-ewma-backend-latency-state-cold-start-and-failure-penalty.md); [ADR-0023](../adr/0023-local-live-demo.md) decision 5.

#### 4E. The known limit (4:30 to 4:40)

**On screen:** Control page, unchanged.

**Voiceover:**
> This algorithm has a known limit.
> If backend3 becomes fast again, its old average stays high.
> So backend3 can stay without traffic.
> The design notes record this limit.

**Director's notes:** No clicks. Source: [ADR-0023](../adr/0023-local-live-demo.md) decision 5 and its Consequences (EWMA has no decay and updates only from proxied responses). Do not demonstrate it: that takes another 30 s and leaves p2c's state stale.

#### 4F. Switch back (4:40 to 5:00)

**On screen:** Control page tab.

**Voiceover:**
> Now I switch back to round-robin.
> Backend3 returns to about 25 percent almost at once.
> So the algorithm caused the change, and the backends stayed the same.

**Director's notes:** Select `lb-roundrobin` → **Switch active LB** on "Now I switch back". Expected: shares return to about 25% within about 15 s as the chart window refills (rehearsal, DEMO-SCRIPT "Recovery (a)").

**Second switch to consistent hashing: left out.** The request-share chart shows a skewed but steady split under consistent hashing (rehearsal: 52.8 / 5.6 / 41.9 / 0.1%). It cannot show that each client stays on one backend, because no panel breaks traffic down by client. The skew alone needs a long explanation and adds about 45 s. If the take is under 4:45 at this point and you want it anyway, use DEMO-SCRIPT Scenario 3 after you reset backend3 to `sleep_ms 0`.

### Scene 5: Failure-mode demo (5:00 to 6:05)

**On screen:** Control page for the first click, then the Grafana tab (`var-lb=lb-roundrobin`), scrolled so *Backend healthy*, *Circuit state* and *Request share by backend* are all visible. Terminal 1 (the log) is visible in its corner.

**Voiceover:**
> Next, a failure.
> First, I remove the delay from backend3.
> Now I stop backend1.
> `<look at Backend healthy — wait for backend1 to show 0>`
> The test requests to backend1 fail.
> The load balancer marks backend1 as unhealthy and stops sending it traffic.
> The other 3 backends take all the traffic.
> `<look at Circuit state — say the next line only if backend1 shows open>`
> The circuit breaker for backend1 is also open.
> A few requests were in progress on backend1 when it stopped.
> Those requests fail with status 502.
> The load balancer never retries a request.
> A retry can run the same request twice.
> The load balancer cannot know if that is safe.
> Now I start backend1 again.
> `<look at Backend healthy — wait for backend1 to show 1>`
> After 2 successful test requests, the load balancer marks it healthy.
> Its share goes back to about 25 percent.

**Director's notes:**
- On "I remove the delay", control page: backend3 row `sleep_ms 0` → **Apply**. Switch to the Grafana tab.
- On "Now I stop backend1", run `docker stop l7loadbalancer-demo-backend1` in Terminal 2.
- Expected: Terminal 1 prints a `health_ejected` line for backend1. *Backend healthy* shows backend1 at 0 within about 5 s, and its share falls to 0 within about 15 s (rehearsal, Scenario 5). Point at each one.
- **Circuit state is not guaranteed here.** The rehearsal recorded health ejection on kill, not the circuit opening. The conditional line exists for that reason. If *Circuit state* or Terminal 1 (`circuit_opened`) shows it open, say the line. If not, skip it.
- The rehearsal's abrupt kill produced 5 failed requests. The bench's graceful stop produced 3 (`RESULTS.md`, backend-kill row). `docker stop` is graceful (the backend handles SIGTERM), so expect a handful. Do not say a number unless you can read it on the request-rate panel's `5xx` series.
- On "Now I start backend1 again", run `docker start l7loadbalancer-demo-backend1`. Expected: `health_reinstated` in Terminal 1, healthy 1 at about 10 s, share near 25% by about 20 s.
- Sources: [ADR-0011](../adr/0011-health-passive-outlier-and-circuit-breaker-composition.md) decision 3 (only an active probe reinstates); [ADR-0018](../adr/0018-no-retry-ever.md) (no retry).

### Scene 6: Close (6:05 to 6:35)

**On screen:** GitHub tab, scrolled to the "Headline results" table in the README.

**Voiceover:**
> I also measured this load balancer against Nginx, a widely used open-source proxy.
> Over HTTP/1.1 with small responses, Nginx reached 11,500 requests per second.
> My load balancer reached 8,000.
> Nginx is ahead at all 3 response sizes that I tested.
> The results page states this, with the method and its limits.
> The code, the benchmark, and the decision records are on GitHub, at DMJain slash l7LoadBalancer.
> The demo runs on your own machine with one command: make demo-up.
> This project shows how I work.
> I write down each decision, I test each claim, and I report the numbers as they are.

**Director's notes:** Point at the 200 B row of the Nginx comparison table on "11,500". Hold on the table to the end, then stop the recording 3 s after the last word. Sources: README "Nginx comparison, HTTP/1.1, round-robin" table and `RESULTS.md` protocol table (Nginx 11500 req/s, load balancer 8000 req/s at 200 B; Nginx ahead at 200 B, 10 KiB and 1 MiB). Single run, ordinal claim only, so the voiceover gives the order and the two numbers and claims no ratio.

---

## Section 3: The script (voiceover only)

Read from this. Pause at each blank line. Angle-bracket cues are for you, not for reading aloud.

This is a Layer 7 load balancer that I built in Go.
Layer 7 means that it reads each HTTP request before it chooses where to send it.
Now, 8 test clients send traffic through it to 4 backends.
A backend is a service that does the work for a request.

I built this project to show how I design, test, and measure a backend system.
Later in this video, I change how it picks a backend while traffic flows.

A load balancer receives requests from clients.
It sends each request to one of several backends.
A selection algorithm decides which backend gets each request.
The selector is the part of the code that runs that algorithm.

This load balancer has 4 selection algorithms.
Round-robin sends requests to each backend in turn.
Least-connections picks the backend with the fewest requests in progress.
Consistent hashing sends the same client to the same backend each time.
Power-of-two-choices compares 2 random backends and picks the faster one.

The load balancer accepts HTTP/1.1 and HTTP/2.
HTTP/2 sends many requests over one connection.
It checks each backend in two ways.
It sends a test request to each backend at a fixed interval.
It also watches for errors on real traffic.

Each backend also has a circuit breaker.
A circuit breaker stops traffic to a backend that keeps failing.
It gives the backend time to recover.
The load balancer also exports metrics to Prometheus, a tool that stores metrics.

I also wrote down what this project leaves out.
It has no rate limiting, no security review, and no multi-region test.
Each design choice has a decision record: a short document that states the choice and the reason.
The project has 25.

Follow one request from the top of this diagram.
The client sends a request to the proxy handler.
The handler asks the selector for a backend.
If a backend is available, the handler adds 1 to its count of requests in progress.
The Go reverse proxy then sends the request to the backend.

Four design decisions matter most here.

First, the selector is an interface.
Each algorithm implements it.
So I can add an algorithm with no change to the request path.

Second, the count of requests in progress.
The handler wraps the response body.
When the body closes, the count goes down by 1.
A guard in the code makes sure that this decrease happens exactly once.
If it happened twice, least-connections would read the wrong count.

Third, the config reload.
SIGHUP is a signal that I send to the running process.
It tells the load balancer to read its config file again.
Then it swaps to a new backend list in one atomic step.
Requests in progress finish on the old list.
New requests use the new list.
In the benchmark, 2 reloads ran during steady traffic.
Both reloads had 0 errors.

Fourth, health checks and the circuit breaker.
A backend gets traffic only if it is healthy and its circuit allows traffic.
The circuit breaker never changes the health flag.
So the two parts never overwrite each other's decisions.

This is the control page for the demo.
Round-robin is the active algorithm now.
Before I started recording, I added a 200 millisecond (ms) delay to backend3.
Round-robin ignores latency.
So each backend still gets about 25 percent of the requests.

p99 latency is the time that 99 of every 100 requests stay under.
<look at the latency chart — read backend3's p99 and one fast backend's p99>
Backend3's p99 is about ___ ms.
The other backends are at about ___ ms.

I change the algorithm in the config file from round_robin to p2c_ewma.
Then I send SIGHUP.
<wait for the log line in Terminal 1>
The load balancer rejects the reload.
The log names the field that changed: algorithm.

I designed it to work this way.
A reload can change the backend list only.
Other settings feed state that the process already built.
So the load balancer rejects the whole reload and keeps its old config.
I put the file back.

So the demo runs 4 load balancers, one for each algorithm.
All 4 share the same 4 backends.
To change the algorithm, I send the 8 clients to a different load balancer.
Now I switch to power-of-two-choices with EWMA.
<click Switch active LB — start the stopwatch>

EWMA means exponentially weighted moving average.
It is an average of recent latency, where new samples count more than old ones.
My prediction is that backend3 gets almost no traffic.
The other 3 backends share the traffic evenly.

The algorithm picks 2 backends at random.
It sends the request to the one with the lower average latency.
Each load balancer measures latency only on the requests that it sends.
So the average needs samples before it is useful.
The charts use a 15 second window, so they also need time to fill.
<look at the request-share chart — wait until 30 s on the stopwatch and backend3 near 0>
Backend3 is now at ___ percent.
The other 3 backends are at about ___ percent each.
Their latencies are equal, so the algorithm chooses between them at random.
The load balancer moved traffic away from the slow backend with no restart and no config change.

This algorithm has a known limit.
If backend3 becomes fast again, its old average stays high.
So backend3 can stay without traffic.
The design notes record this limit.

Now I switch back to round-robin.
<click Switch active LB>
Backend3 returns to about 25 percent almost at once.
So the algorithm caused the change, and the backends stayed the same.

Next, a failure.
First, I remove the delay from backend3.
<Apply sleep_ms 0 on backend3 — switch to Grafana>
Now I stop backend1.
<run docker stop — look at Backend healthy, wait for 0>
The test requests to backend1 fail.
The load balancer marks backend1 as unhealthy and stops sending it traffic.
The other 3 backends take all the traffic.
<look at Circuit state — read the next line only if backend1 shows open>
The circuit breaker for backend1 is also open.

A few requests were in progress on backend1 when it stopped.
Those requests fail with status 502.
The load balancer never retries a request.
A retry can run the same request twice.
The load balancer cannot know if that is safe.

Now I start backend1 again.
<run docker start — look at Backend healthy, wait for 1>
After 2 successful test requests, the load balancer marks it healthy.
Its share goes back to about 25 percent.

I also measured this load balancer against Nginx, a widely used open-source proxy.
Over HTTP/1.1 with small responses, Nginx reached 11,500 requests per second.
My load balancer reached 8,000.
Nginx is ahead at all 3 response sizes that I tested.
The results page states this, with the method and its limits.

The code, the benchmark, and the decision records are on GitHub, at DMJain slash l7LoadBalancer.
The demo runs on your own machine with one command: make demo-up.
This project shows how I work.
I write down each decision, I test each claim, and I report the numbers as they are.

### Validation summary

- **Word count:** 1,102 words of voiceover (cues excluded, placeholders counted). At about 150 words per minute that is about 7:20 of speech. The waits in Scenes 4D and 5 fall inside the narration, so the take runs about 7:00 to 7:30. That is at the top of the 5 to 7 minute target. If the dry run runs over 7:00, cut in this order: Scene 4E (the known limit, 25 words), the scope pair in Scene 2 ("I also wrote down…" and the line after it, 22 words), and the 4D sentence "The load balancer moved traffic away…" (17 words).
- **Longest sentence:** 17 words ("I write down each decision, I test each claim, and I report the numbers as they are." and "The load balancer moved traffic away from the slow backend with no restart and no config change."). 119 sentences in total.
- **Technical terms, each defined on first use:** Layer 7 (Scene 1), backend (Scene 1), selection algorithm and selector (Scene 2), consistent hashing (Scene 2), HTTP/2 (Scene 2), circuit breaker (Scene 2), Prometheus (Scene 2), decision record (Scene 2), SIGHUP (Scene 3), ms (Scene 4A), p99 (Scene 4A), EWMA (Scene 4C). Terms used as known: HTTP, request, config, log, status code 502 (named with "status"), round-robin, least-connections and power-of-two-choices (each described in one sentence in Scene 2), interface, atomic, Go.
- **Checks:**
  1. Every sentence has 20 words or fewer: passed (script-checked).
  2. No banned word or phrase from the addendum: passed (script-checked against the addendum's lists).
  3. Active voice: passed. "The load balancer rejects the reload", not "the reload is rejected".
  4. A viewer with no load-balancer background can follow it: passed. Every algorithm gets one plain sentence before it appears in the demo.
  5. No technical term before its definition: passed. "p99" first appears in Scene 4A, with its definition. "EWMA" is defined in the sentence after "power-of-two-choices with EWMA".
- **One word, one meaning:** "backend" is the only word for a backend. "Server" and "node" are not used. "Load balancer" is never shortened to "LB" in speech. "Requests in progress" is the only phrase for in-flight requests.
- **Known exceptions to positive form:** "no rate limiting, no security review, and no multi-region test" and "The load balancer cannot know if that is safe" keep their negatives. The scope list is a list of absences. "Is unable to know" reads worse and means the same thing.

---

## Section 4: Terminal setup appendix

### Screen layout (one 1920 × 1080 display, recorded full screen)

```
+---------------------------------------------+---------------------------+
|                                             |  Terminal 1 (log tail)    |
|                                             |  lb-roundrobin events     |
|   Browser (one window, three tabs)          |  ~ 640 x 400              |
|   1. GitHub README                          +---------------------------+
|   2. Control page  127.0.0.1:8095           |  Terminal 2 (editor +     |
|   3. Grafana       127.0.0.1:3000           |  commands)                |
|   ~ 1280 x 1080                             |  ~ 640 x 680              |
|                                             |                           |
+---------------------------------------------+---------------------------+
```

In Scene 4B, drag Terminal 2 and Terminal 1 to the front over the browser, or give them the left half for 25 s. Use whichever you rehearsed in the dry run.

### Terminal 1: load balancer log tail

Runs for the whole take. It shows the reload rejection (4B), the health ejection and reinstatement (5), and any circuit change.

```sh
cd ~/Documents/projects/l7LoadBalancer
docker compose -f demo/docker-compose.yml logs -f --no-log-prefix --since 1s lb-roundrobin \
  | grep --line-buffered -E 'config_reload|health_|circuit_'
```

Expected: no output until an event happens. In 4B:
`{"time":"…","level":"WARN","msg":"config reload failed","event":"config_reload_failed","reason":"non_backend_change","fields":["algorithm"]}`
In Scene 5, a line with `"event":"health_ejected"` for backend1 after the stop, and `"event":"health_reinstated"` after the start. The `level`, `time` and other fields are as the logger prints them. Only the `msg`, `event`, `reason` and `fields` values are confirmed in the code.

### Terminal 2: editor and commands

Before the take, open the config in vim, positioned on the algorithm line:

```sh
cd ~/Documents/projects/l7LoadBalancer
vim -c 'set backupcopy=yes' +/^algorithm demo/configs/roundrobin.yaml
```

File: `demo/configs/roundrobin.yaml`, line `algorithm: "round_robin"`.

Commands for the take, in order. Type them into shell history before recording (run `history` to check), so that each one is one up-arrow away. Put them in a file and `cat` it if you prefer to copy.

```sh
# 4B: after :wq in vim
docker compose -f demo/docker-compose.yml kill -s HUP lb-roundrobin
# 4B: restore the file (no second SIGHUP needed; the reload was rejected)
cp /tmp/roundrobin.orig.yaml demo/configs/roundrobin.yaml
# Scene 5
docker stop l7loadbalancer-demo-backend1
docker start l7loadbalancer-demo-backend1
```

`docker compose kill -s HUP` sends SIGHUP to the process in that container. It is the command the rehearsal used (DEMO-SCRIPT Scenario 6). A `kill -HUP $(pgrep …)` on the host does not reach a process inside a Docker Desktop VM.

### Terminal 3: not used

The demo backends run with `LOG_REQUESTS=false`, so a backend log tail shows nothing useful. The control page already shows each backend's container state and profile.

### Browser

| Tab | URL | Settings |
|---|---|---|
| GitHub | `https://github.com/DMJain/l7LoadBalancer` | README, scrolled to "How a request flows" before Scene 2 |
| Control page | `http://127.0.0.1:8095/` | Its charts use `var-window=15s&refresh=5s` and follow the active load balancer |
| Grafana | `http://127.0.0.1:3000/d/l7loadbalancer/?var-window=15s&var-lb=lb-roundrobin&from=now-15m&to=now&refresh=5s` | Dashboard `l7LoadBalancer`, time range "Last 15 minutes", refresh 5 s, `$lb` = `lb-roundrobin`. No login: the demo Grafana allows anonymous viewing. |

The `$lb` variable on the Grafana tab stays at `lb-roundrobin` for the whole take. Scene 5 is the only scene that uses that tab, and round-robin is active again by then.

---

## Section 5: Common failure modes and recovery

| Symptom | Fix |
|---|---|
| Dashboard or control-page charts show "No data". | `docker compose -f demo/docker-compose.yml restart prometheus`, wait 20 s, reload the tab. Check that `var-lb` matches the active load balancer. |
| Charts show data but the shares are flat at 0 for every backend. | The generators stopped. On the control page, **Set rate** `400` and **Switch active LB** to the intended load balancer again. |
| Terminal 1 prints nothing after SIGHUP. | Check that the file changed in the container: `docker compose -f demo/docker-compose.yml exec -T lb-roundrobin cat /etc/l7lb/config.yaml \| grep algorithm`. If it still reads `round_robin`, the editor replaced the file. Restore the file, `docker compose -f demo/docker-compose.yml restart lb-roundrobin`, and use `nano` next time. |
| Terminal 1 prints `config_reload_failed` with `reason` `parse_error` or `validation_error`. | A typo in the edit. Restore with `cp /tmp/roundrobin.orig.yaml demo/configs/roundrobin.yaml`. On camera, say "That is a typo. The load balancer rejects it and keeps its old config," then retry once. |
| Terminal 1 prints `config_reloaded` (the reload was accepted). | The algorithm line was not changed. Restore the file, then send SIGHUP once more so the running config matches the file. |
| After the switch to p2c-ewma, backend3 is still above 10% at 45 s. | Say "The average is still learning," and go on to 4E. After the take, `docker compose -f demo/docker-compose.yml restart lb-p2c-ewma` gives it a fresh, empty average (ADR-0010 cold start; not rehearsed). |
| A fast backend sits at 0% under p2c-ewma. | Stale average from an earlier slow phase (ADR-0023 decision 5). Re-take: `docker compose -f demo/docker-compose.yml restart lb-p2c-ewma`, wait 30 s, start from Scene 4. |
| backend1 still reads healthy 20 s after `docker stop`. | `docker ps --filter name=l7loadbalancer-demo-backend1` must show nothing. If it still runs, `docker kill l7loadbalancer-demo-backend1` (the abrupt kill the rehearsal used), or click **Kill** on the backend1 row of the control page. |
| backend1 does not come back after `docker start`. | `docker compose -f demo/docker-compose.yml up -d backend1`, or click **Revive** on the control page. Wait up to 30 s for two good probes. |
| The generators died (control page names a failed peer, or `dropped` climbs). | `docker compose -f demo/docker-compose.yml up -d gen1 gen2 gen3 gen4 gen5 gen6 gen7 gen8`, then **Set rate** `400` on the control page. |
| Audio clipping or background noise found after recording. | If it is under 3 s in total and not on a number or term definition, keep the take. Otherwise re-shoot the whole take: one continuous take is the rule, so do not splice. |

---

## Section 6: Post-recording checklist

1. **Restore the rig.** `diff /tmp/roundrobin.orig.yaml demo/configs/roundrobin.yaml` must print nothing. `git status` must show no change under `demo/configs/`. Then `make demo-down`.
2. **Trim the ends only**: the first and last 2 to 3 s (the click on record, the pointer moving into place, keyboard noise). QuickTime: Edit → Trim.
3. **Check audio across the whole clip**, not only the start. Scrub to 1:00, 3:30, 5:30 and the last 30 s and listen to each. Peaks should sit near −6 dB with no clipping.
4. **Export** at 1080p minimum, 1440p if the recording is 1440p, H.264, at least 12 Mbit/s at 1080p (20 Mbit/s at 1440p) so terminal text stays sharp. Before you upload, check at full screen that the Terminal 1 log line in Scene 4B can be read.
5. **Upload** to YouTube as **Unlisted**. Title: "l7LoadBalancer: live selector switch and failure demo". Description: one line on what it shows, plus the GitHub URL.
6. **Raw file**: do not commit the video into the repository. A 6-minute 1080p file is several hundred MB and stays in git history permanently. Keep it outside the repository, or attach it to a GitHub release, and link it.
7. **README**: replace `<!-- VIDEO-LINK-SLOT: owner fills in after recording -->` in `README.md` with the YouTube link. That slot sits in the "Local live demo" section, after the architecture diagram. Also replace "**Video pending.**" in `demo/README.md` ("Walkthrough video" section). The brief asked for the link above the architecture section. Moving it there changes the S5.T13.2 README layout, so decide that separately.
8. **PROGRESS.md**: mark S5.T17.2 done, following the task protocol in `AGENTS.md`.
9. **LinkedIn post draft:**

   > Shipped the Sprint 5 demo of my portfolio project: a Layer 7 load balancer in Go.
   >
   > The 6-minute video switches the selection algorithm while traffic flows. With one backend slowed by 200 ms, round-robin keeps sending it 25% of requests. Power-of-two-choices with a latency average moves almost all traffic off it. Then I stop a backend and the load balancer removes it and adds it back.
   >
   > It also shows a choice I made on purpose: a SIGHUP reload can change the backend list only. A reload that changes the algorithm is rejected whole.
   >
   > Benchmarked against Nginx with a reproducible harness. Nginx is ahead over HTTP/1.1, and the results page says so.
   >
   > Video: <YouTube link>
   > Code, decision records and results: https://github.com/DMJain/l7LoadBalancer

---

## Warnings

- **Unsourced numbers.** None are spoken without a source. Six values are placeholders to read from the screen at record time: backend3's p99 and a fast backend's p99 (4A), backend3's share and the fast backends' share after the switch (4D). Rehearsal values for comparison: p50 175 ms against 2.5 ms; shares 0.0% and about 33%.
- **Departures from the brief** (see the top of this document): the algorithm switch uses the control page, not SIGHUP (ADR-0015 decision 4, ADR-0023); the background load is the demo's own generators, not vegeta or wrk; there is no deployment URL (ADR-0023 decision 1). If you want a working SIGHUP reload on camera (removing a backend, with zero errors), DEMO-SCRIPT Scenario 6 has the rehearsed steps. It adds about 45 s and needs a second restore step.
- **Not rehearsed in this exact form:**
  - The rejected-SIGHUP beat (4B). The code path is unit-tested and the log fields come from the code, but the rehearsal sent SIGHUP only with a backend removed. The dry run in pre-flight step 13 is the rehearsal for it.
  - `docker stop` / `docker start` in Scene 5. The rehearsal used the control page's **Kill** (abrupt) and **Revive**. `docker stop` is graceful, so ejection timing and the 502 count may differ slightly.
  - The circuit breaker opening when a backend stops. The rehearsal recorded health ejection, not the circuit, for a kill. The circuit opened in the rehearsal's flaky-backend scenario (`fail_rate 0.5`). Scene 5's circuit line is conditional for this reason.
- **Dependencies on open tasks.** S5.T17.2 (this recording) is the open task. Nothing else blocks it: the demo stack (S5.T16.x), the acceptance check (S5.T16.5), the recording script (S5.T17.1) and the README with its video slot (S5.T13.2) are all done. The S5.T19.1.1 hygiene commit (`632c7c2`) includes `docs/resume/l7lb-bullets.md`, which the hygiene spec says should stay untracked. That does not affect the video, but resolve it before you publish the repository link in the video and the post.
- **Benchmark caveat.** The Nginx numbers come from one run that `RESULTS.md` flags as noisy and incomplete. The voiceover says "the results page states this, with the method and its limits" and claims no ratio, as the README's "ordinal claim only" requires.
