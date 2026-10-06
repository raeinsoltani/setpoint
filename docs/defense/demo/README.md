# Live demo

Ten minutes at the end of the defense. One constant load, three controllers, and the
same disturbance applied to each. Nothing about the traffic changes, so anything that
moves on screen was moved by a controller.

```
./demo.sh up        before the talk: preflight, Grafana, demo dashboard, fleet at 1
./demo.sh stage     the ticker and a command line in one tmux window
./demo.sh healthy   start 550 req/s; forecast TOTAL load, stabilizer off
./demo.sh kick      knock the fleet down to 2 pods, as if a node had died
./demo.sh broken    forecast PER-POD load, stabilizer off; fleet reset to 6
./demo.sh masked    the same broken policy, 90 s stabilizer on; fleet reset to 6
./demo.sh down      stop the load, print its summary, restore the committed config
./demo.sh code      open the three code stops in VS Code
./demo.sh test      the same contrast as a unit test, no cluster needed
```

## Why a kick

At the correct fleet, the broken policy sits exactly as still as the healthy one. Per-pod
load is constant, so its forecast has no trend to extrapolate, and 92 req/s is inside
the 10% dead-band. The first rehearsal showed this: started at 6 pods, it made 0 moves in
13 cycles. Stability is a question about what happens after a disturbance, so the demo
applies one. `kick` writes `.spec.replicas = 2` behind the controller's back, the same
write a node failure forces on the fleet.

That stillness is also the answer to the question it raises: *"if it is unstable, why did
it sit at 6?"* Both policies share the same fixed point. They differ in what a change in
`r` does next. In the total-load policy, `r` cancels and the kick never reaches the
decision. In the per-replica policy, the kick reaches the forecaster as a trend and the
horizon multiplies it (slide 6). The unit test's initial climb from one replica, and the
measured patterns' steps, are the same kick by other means.

## What it looks like

Rehearsed four times on 2026-10-06, on this machine, against the pinned image.

**healthy.** Climbs 1 → 2 → 6 → 7–9 and trims back to 6 within ~90 s. It overshoots
because the controller's 1m rate window is still filling, which looks like a rising
trend. After the kick it returns in **one decision**, `2 → 6`, then holds. That held in
all three timed kicks. The forecast barely moves through it (576 → 579 → 568 total):
it forecasts the traffic, and the traffic did not change.

**broken.** Holds at 6 with nothing to show, until the kick. Then it reverses at every
cycle, between 1 and 8–10 pods:

```
  ⚡ fleet knocked from 6 to 2 pods by hand · traffic unchanged
  14:14:08  ▲   2 → 9    ██░░░░░░░       269.0 req/s   446 /pod   4.0%
  14:14:23  ▼   9 → 1    █××××××××        60.7 req/s     0 /pod   0.5%
  14:14:38  ▲   1 → 10   █░░░░░░░░░      530.6 req/s   941 /pod   5.5%
  14:14:53  ▼  10 → 1    █×××××××××       52.9 req/s     0 /pod   0.6%
```

That is 9 reversals in 10 moves, the signature the thesis measured as 119 of 120.
The `0 /pod` lines are the forecaster predicting no load at all while the fleet serves
550 req/s.

**masked.** After the kick it jumps to 10 and the stabilizer holds it there, while the
policy's own recommendation (amber `┃`, and the dashed line in Grafana) crawls up from
**one pod**:

```
  14:17:42  ·  10 → 10   ┃█████████    55.1 req/s   0 /pod   held · raw 1
  14:18:12  ·  10 → 10   ██┃███████    54.1 req/s  25 /pod   held · raw 3
  14:18:42  ·  10 → 10   ████┃█████    55.0 req/s  49 /pod   held · raw 5
  14:18:57  ▼  10 → 6    ██████××××
```

The fleet looks like ordinary autoscaling: a 15-second oscillation is now a slow one,
and every check passes. Slide 12's sentence, in one frame.

**Why `kick` waits for a decision first.** The total-load policy rebuilds total load as
`metric × ready`. `ready` drops the moment pods go, but the metric's denominator,
`count(up)`, waits for the next 5 s scrape. In one rehearsal a reconcile landed 4 s
after the kick and read 76.8 × 2 = 154 req/s of a real 550. The healthy policy dipped to
1 pod for one cycle, then went to 7. So `kick` strikes just after a decision, which
gives every act the same full interval before the controller looks. It can take up to
15 s to fire, so narrate while it waits. If you are asked: it is a one-cycle transient
in the signal, not in the control law. The loop still returns, which is what stability
means. It is recorded in the lab notebook (§11.21).

**Do not quote the thesis's failure figures for what is on screen.** User-side failures
here are a few percent per swing (2.2% over the broken act), not the 19–41% of the
measured `spike` runs. At constant 550 req/s, one warm pod carries ~500 req/s, and the
failures come from pods removed with requests in flight. The reversal signature is the
part that reproduces. Say that.

## Run sheet

| t | do | on screen | say (cue; full text in the speaker notes) |
|---|---|---|---|
| −15 min | `./demo.sh up`, log in to Grafana, open the dashboard, `./demo.sh stage` | fleet 1, no load | |
| 0:00 | demo slide | | 550 req/s throughout; the right answer is 6 pods; only the controller changes |
| 0:20 | `./demo.sh healthy`, then switch to VS Code | ticker starts climbing | |
| 0:30 | **code stop 1**: `policy.go:101` and `:138` | | the whole bug: `Update(metric * ready)` vs `Update(metric)` |
| 2:00 | back to Grafana + ticker | settled at 6 | it overshot while the rate window filled, and came back |
| 2:15 | `./demo.sh kick` | `2 → 6` in one cycle | `r` cancels; the kick never reaches the decision |
| 3:00 | `./demo.sh broken` (~40 s: resets to 6, waits for the signal to settle) | holds at 6 | same fixed point; nothing to see yet |
| 4:00 | `./demo.sh kick` | sawtooth, ▲▼ every line | reverses every 15 s, forever; this is 119 of 120 |
| 5:45 | `./demo.sh masked` (~40 s), then VS Code while it runs | | |
| 5:50 | **code stop 2**: `stabilize.go:88` | | scale-down takes the window's *max*: this is what is about to hide it |
| 6:45 | back; `./demo.sh kick` | 10 pods, held; amber raw at 1 | the policy wants one pod; the stabilizer refuses; the dashboard looks fine |
| 8:45 | `./demo.sh test` (optional) | `total-load=0, per-replica=29` | the same result, in one second, without a cluster |
| 9:15 | back to the conclusion slide | | |
| after | `./demo.sh down` | k6 summary | |

The ticker keeps running through the code stops, so the broken act is still oscillating
when you come back to it. That's the point to make: it does not settle on its own.

## Screen

One display, mirrored to the projector. Grafana (Chrome, light theme, kiosk) in the top
half and the tmux window from `./demo.sh stage` in the bottom half: ticker above, a
two-line command pane below, so the room sees each command typed. VS Code full screen
for the code stops, with Cmd-Tab to move between them. Raise the terminal font until a
ticker line still fits on one row.

```
http://localhost:3000/d/setpoint-demo/?kiosk&refresh=5s&theme=light
```

## If something goes wrong

| symptom | do |
|---|---|
| Grafana blank or "bad gateway" | the tunnel respawns within a second; reload the page |
| ticker prints `controller restarting` for > 30 s | `kubectl get pods`; if setpoint is not Running, rerun the act command |
| `settling ... warning: did not settle` | carry on: the act starts anyway, a few seconds less clean |
| Docker Desktop or the cluster is gone | `./demo.sh test`, and show the rehearsal screen recording |
| any act misbehaves | it is a live system; narrate what it did, then show the recording |

Record a full screen rehearsal with QuickTime the day before and keep it on the desktop.
It is the only fallback that shows the same thing. A rehearsal plot cannot go on a slide,
because the deck shows only figures the thesis has (`../README.md`).

## What it touches

The `setpoint-config` ConfigMap, the `sample` replica count, and a `setpoint-demo-dashboard`
ConfigMap in `monitoring`. It writes nothing under `experiments/results/`, and rebuilds
nothing: `up` refuses to run unless the sample app is the measured image
(lab-notebook §11.18). `down` restores the committed ConfigMap, as `run.sh` expects to
find it.
