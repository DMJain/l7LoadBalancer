# Demo run sheet

The steps to rehearse and run the live demo. The narration is in [`docs/demo/video-script.md`](../docs/demo/video-script.md). To change the algorithm, edit `demo/configs/lb.yaml` and send SIGHUP. The process never restarts (ADR-0015 decisions 18–21).

Scenarios 2–4 and the acceptance numbers were measured on this one-LB stack. Scenarios marked *earlier rig* were measured on an earlier four-LB rig with the same code, backends and clients. Check those numbers in your dry run.

## Set up

1. Run `make demo-up`, then `demo/acceptance.sh`. It must print `All checks passed.` (measured: backend1/2/4 p50 2.5 ms, backend3 175 ms, then backend3 share 0.00% and 0 client errors after the reload).
2. Set `algorithm: "round_robin"` in `demo/configs/lb.yaml` and send SIGHUP. The committed file can hold a different algorithm.
3. Open **http://127.0.0.1:8095**. The header reads `Algorithm: Round robin (round_robin on lb)`, the load reads `now: 400 req/s`, and every backend row reads `0 / 0 / 0` and `running`.
4. In a terminal at the repository root, have these two commands ready:

   ```sh
   docker compose -f demo/docker-compose.yml logs -f --since 1s lb | grep --line-buffered -E 'config_reload|health_|circuit_'
   docker compose -f demo/docker-compose.yml kill -s HUP lb
   ```

5. Open `demo/configs/lb.yaml` in any editor.

Expected log lines: `"msg":"config reloaded",…,"previous_algorithm":"round_robin","algorithm":"p2c_ewma"` on a reload, `"event":"health_ejected"` and `circuit_opened` on a stop, and `"event":"health_reinstated"` on a start.

## Timing

- After a reload, wait about 30 s before you read a distribution. The charts use a 15 s window, and P2C needs fresh latency samples.
- To reset, click **Clear** on every backend, set the rate to `400`, set `round_robin` and send SIGHUP. Wait about 20 s, until each backend shows about 25%.
- After scenario 6, wait up to 40 s for the circuit to close (30 s cooldown, ADR-0012).

## Scenarios

| # | Do | Wait | Expected |
|---|---|---|---|
| 1 | Nothing (reset state). | 20 s | Share 25% each. p99 about 5 ms. All healthy, circuits closed, `errors 0`. |
| 2 | backend3: delay `200`, **Apply**. Wait 25 s. Set `p2c_ewma`, save, SIGHUP. | 30–45 s | Before: backend3 still 25%, p99 about 250 ms (histogram bucket), others about 5 ms. After: reload log line, header `P2C-EWMA` and `SUCCESS`, a marker. backend3 falls to about 0%, others about 33%. "Failed" and `errors` do not change. Measured: 0.0%, others 32.8 / 35.0 / 32.3%, 0 errors (ADR-0010). |
| 3 | Set `round_robin`, save, SIGHUP. Then **Clear** backend3. | 20 s | Second marker, `SUCCESS`. backend3 back to 25%. Measured: 96,213 requests in the 30 s around the reload, 0 client errors. |
| 4 | Click **Try a reload with an invalid config**. | none | Page: `Rejected by lb as expected: config: unsupported algorithm "p3c_ewma"`. Header: `Last reload: REJECTED at hh:mm:ss — …`. Algorithm and chart do not change (ADR-0015 decisions 13–16). Terminal variant: set `p3c_ewma` in the file and SIGHUP. The log shows `config_reload_failed`, `reason: validation_error`. Restore the file. |
| 5 | Optional, *earlier rig*. Reload to `least_conn` with backend3 at 200 ms, or to `consistent_hash` with all fast. | 30 s | `least_conn`: backend3 about 1%, others uneven (78 / 18 / 2%). `consistent_hash`: skewed but steady (52.8 / 5.6 / 41.9 / 0.1%). Each client maps to one backend, and the bounded ring spills the hottest key (ADR-0008, ADR-0009). |
| 6 | *Earlier rig.* backend2: fail rate `0.5`, **Apply**. Then **Clear**. | 40 s | In about 3 s backend2 is Unhealthy (passive outlier detection) and its share falls toward 0. Circuit Open about 6 s after the injection. Others about 33%. After **Clear**: healthy in about 10 s, circuit closed in about 40 s (ADR-0011, ADR-0012). |
| 7 | backend1: **Stop**. Wait 20 s. **Start**. | 30 s | `exited`, Unhealthy in about 5 s, share 0 in about 15 s, circuit Open. A few requests in flight fail (*earlier rig*: 1 and 5), and the LB never retries them (ADR-0018). After **Start**: healthy in about 10 s (two good probes), about 25% in about 20 s. |
| 8 | Optional. Delete backend4's two lines, save, SIGHUP. Wait 15 s. Restore them, SIGHUP. | 15 s | Log `added 0, removed 1, unchanged 3`, then `added 1, removed 0, unchanged 3`. backend4 drains to 0%, others about 33%. backend4 returns after one good probe. Zero client errors (ADR-0016). |

## Known behaviours

Do not contradict these on camera.

- Each request picks its backend once, at its start. Requests in flight keep their backend, and the listener stays open (ADR-0015 decision 19).
- The new algorithm starts warm. Latency averages and in-flight counts live on the backends, and every algorithm keeps them current (ADR-0015 decision 20).
- The LB rejects a bad config whole (unknown algorithm, duplicate backend, bad URL, any other changed field). The old config keeps serving.
- P2C-EWMA with four different latencies (0 / 50 / 100 / 200 ms) tends to 50 / 33 / 17 / 0% (*earlier rig*).
- EWMA has no decay, and only proxied responses update it. Under P2C, a recovered backend can stay starved. A reload to another algorithm sends it traffic and refreshes its average (ADR-0023 decision 5).
- `least_conn` breaks ties in registry order, so at this rate it prefers `backend1`. Do not promise an even split.

## Teardown

```sh
make demo-down
git checkout demo/configs/lb.yaml    # git diff on the file must then be empty
```
