#!/usr/bin/env bash
#
# The live defense demo: one constant load, three controllers, one disturbance.
#
#   ./demo.sh up        before the talk: preflight, Grafana, demo dashboard, fleet at 1
#   ./demo.sh stage     the decision ticker and a command pane, in one tmux window
#   ./demo.sh healthy   start 550 req/s; forecast TOTAL load, stabilizer off
#   ./demo.sh broken    same load; forecast PER-POD load, stabilizer off; fleet reset to 6
#   ./demo.sh masked    same broken policy; the 90 s stabilizer back on; fleet reset to 6
#   ./demo.sh kick      knock the fleet down to 2 pods, as if a node had died
#   ./demo.sh down      stop the load, print its summary, restore the committed config
#
#   ./demo.sh watch     the decision ticker alone (watch.py)
#   ./demo.sh code      open the code stops in VS Code
#   ./demo.sh test      the same contrast as a unit test, no cluster needed
#
# The traffic never changes between acts, so anything that moves on screen was moved
# by the controller. The kick is what makes stability visible: at the correct fleet the
# broken policy sits as still as the healthy one, because per-pod load is constant and
# inside the dead-band, so its forecast has no trend to extrapolate. Stability is the
# question of what happens after a disturbance, and only a disturbance can answer it.
#
# The demo runs the binaries the thesis measured — nothing is rebuilt (lab-notebook
# §11.18) — and writes nothing under experiments/results/.
#
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
NS=default
MON_NS=monitoring
RATE="${RATE:-550}"
FLEET=$(( (RATE + 99) / 100 ))   # the correct fleet: ceil(RATE / target), target 100
KICK_TO="${KICK_TO:-2}"
STATE="${TMPDIR:-/tmp}/setpoint-demo"
mkdir -p "$STATE"

# The image every Phase 6 number was measured against (experiments/run.sh). The demo
# makes claims about the measured system, so it refuses to run against another one.
EXPECTED_APP_IMAGE="${EXPECTED_APP_IMAGE-docker.io/library/sample-app@sha256:d089f870ba7acd9071b75f7556cd2d6c8b73125d9faa2b6efddcd9657db5ab1d}"

die()  { echo "error: $*" >&2; exit 1; }
say()  { printf '\033[1m%s\033[0m\n' "$*"; }

# set_config POLICY WINDOW [FLEET] — the committed ConfigMap with exactly two fields
# changed, and optionally the fleet reset before the new controller starts.
#
# The same rewrite experiments/run.sh does, for the same reason: only `name:` inside
# `policy:` and the stabilization window change, so every other setting is the one
# the thesis measured. The order is run.sh's too: stop the controller, set the fleet,
# then start exactly one controller, so no act inherits the previous act's mess. A
# ConfigMap change does not reach a running pod, so the controller is started fresh
# and the live value read back rather than trusted.
set_config() {
  local policy="$1" stab="$2" fleet="${3:-}" tmp
  tmp="$(mktemp -t setpoint-demo-cm)"
  awk -v p="$policy" -v s="$stab" '
    /^    policy:/ { inpolicy = 1 }
    /^    scaler:/ { inpolicy = 0 }
    inpolicy && /^      name:/ && !done { sub(/name:[[:space:]]*[a-z-]+/, "name: " p); done = 1 }
    inpolicy && /^      stabilization_window_seconds:/ {
      sub(/stabilization_window_seconds:[[:space:]]*[0-9]+/, "stabilization_window_seconds: " s)
    }
    { print }
  ' "$REPO/deploy/setpoint/configmap.yaml" > "$tmp"
  kubectl scale deploy/setpoint -n "$NS" --replicas=0 >/dev/null
  kubectl wait --for=delete pod -l app.kubernetes.io/name=setpoint -n "$NS" --timeout=60s >/dev/null 2>&1 || true
  if [[ -n "$fleet" ]]; then
    kubectl scale deploy/sample -n "$NS" --replicas="$fleet" >/dev/null
    kubectl rollout status deploy/sample -n "$NS" --timeout=120s >/dev/null
    load_running && settle "$fleet"
  fi
  kubectl apply -f "$tmp" >/dev/null
  rm -f "$tmp"
  kubectl scale deploy/setpoint -n "$NS" --replicas=1 >/dev/null
  kubectl rollout status deploy/setpoint -n "$NS" --timeout=90s >/dev/null

  local live
  live="$(live_config)"
  [[ "$live" == "$policy $stab" ]] || die "live config is '$live', expected '$policy $stab'"
}

# settle FLEET — wait until the controller's own signal reads the fleet as settled.
#
# Resetting the fleet is not enough. The signal is a rate over a 1m window, so for a
# minute after an oscillating act it still remembers the oscillation, and a controller
# started into that inherits the previous act's disturbance. Rehearsed: the masked
# act, started straight after the broken one, moved 6 -> 4 -> 9 before any kick.
# Three readings in a row within 5% of RATE/FLEET means the window has flushed.
settle() {
  local want ok=0 v
  want="$(awk -v r="$RATE" -v f="$1" 'BEGIN { print r / f }')"
  printf 'settling the metric window at %s pods' "$1"
  for _ in $(seq 1 45); do
    v="$(kubectl get --raw "$PROM_API?query=$SIGNAL" 2>/dev/null | jq -r '.data.result[0].value[1] // ""')"
    if [[ -n "$v" ]] && awk -v v="$v" -v w="$want" 'BEGIN { exit !(v > w * 0.95 && v < w * 1.05) }'; then
      ok=$((ok + 1)); (( ok >= 3 )) && { printf ' — %.0f req/s per pod\n' "$v"; return 0; }
    else
      ok=0
    fi
    printf '.'; sleep 3
  done
  echo; say "warning: the signal did not settle (last $v, want ~$want); starting anyway"
}

# The collector query from deploy/setpoint/configmap.yaml, URL-encoded, read through
# the API server's service proxy so no port-forward has to be alive.
PROM_API="/api/v1/namespaces/$MON_NS/services/monitoring-kube-prometheus-prometheus:9090/proxy/api/v1/query"
SIGNAL="$(python3 -c 'import urllib.parse; print(urllib.parse.quote("sum(rate(http_requests_total{app=\"sample\"}[1m])) / clamp_min(count(up{app=\"sample\"} == 1), 1)"))')"

# The controller's last successful decision, from its own exporter; empty if unreachable.
decision_stamp() {
  kubectl get --raw "/api/v1/namespaces/$NS/services/setpoint:metrics/proxy/metrics" 2>/dev/null \
    | awk '/^autoscaler_last_success_timestamp_seconds /{ print $2 }'
}

# "POLICY WINDOW" as the live ConfigMap has them.
live_config() {
  kubectl get cm setpoint-config -n "$NS" -o jsonpath='{.data.config\.yaml}' \
    | awk '/^policy:/{p=1} p && /^  name:/{n=$2} p && /^  stabilization_window_seconds:/{s=$2} END{print n, s}'
}

load_running() { [[ -f "$STATE/k6.pid" ]] && kill -0 "$(cat "$STATE/k6.pid")" 2>/dev/null; }

start_load() {
  load_running && return 0
  RATE="$RATE" nohup k6 run --address localhost:6565 "$HERE/load.js" \
    > "$STATE/k6.log" 2>&1 &
  echo $! > "$STATE/k6.pid"
  say "load: $RATE req/s, constant from here to the end (log: $STATE/k6.log)"
}

stop_load() {
  load_running || return 0
  # SIGINT, not SIGKILL: k6 prints its end-of-test summary on an interrupt, and that
  # summary is the user-side record of the whole demo.
  kill -INT "$(cat "$STATE/k6.pid")"
  for _ in $(seq 1 30); do load_running || break; sleep 1; done
  rm -f "$STATE/k6.pid"
  grep -E 'http_req_failed|http_req_duration|http_reqs|dropped_iterations' "$STATE/k6.log" || true
}

# kubectl port-forward dies on any hiccup, and a dead Grafana tunnel in front of the
# examiner is the worst failure this demo can have. Respawn it until `down`.
start_grafana() {
  [[ -f "$STATE/grafana.pid" ]] && kill -0 "$(cat "$STATE/grafana.pid")" 2>/dev/null && return 0
  touch "$STATE/grafana.run"
  (
    while [[ -f "$STATE/grafana.run" ]]; do
      kubectl port-forward -n "$MON_NS" svc/monitoring-grafana 3000:80 >/dev/null 2>&1 || true
      sleep 1
    done
  ) </dev/null >/dev/null 2>&1 &
  # Detached from the terminal's streams: a loop that keeps them open would keep any
  # caller reading this script's output — a pipe, a CI log — waiting forever.
  echo $! > "$STATE/grafana.pid"
}

stop_grafana() {
  rm -f "$STATE/grafana.run"
  if [[ -f "$STATE/grafana.pid" ]]; then
    pkill -P "$(cat "$STATE/grafana.pid")" 2>/dev/null || true
    kill "$(cat "$STATE/grafana.pid")" 2>/dev/null || true
    rm -f "$STATE/grafana.pid"
  fi
}

preflight() {
  for bin in kubectl k6 python3; do command -v "$bin" >/dev/null || die "$bin not found"; done
  kubectl get deploy sample setpoint -n "$NS" >/dev/null || die "the stack is not up: make stack-up"

  local digests
  digests="$(kubectl get pods -n "$NS" -l app=sample --field-selector=status.phase=Running \
    -o jsonpath='{range .items[*]}{.status.containerStatuses[0].imageID}{"\n"}{end}' | sort -u | sed '/^$/d')"
  [[ -z "$EXPECTED_APP_IMAGE" || "$digests" == "$EXPECTED_APP_IMAGE" ]] \
    || die "sample app is not the measured image:
  running:  $digests
  expected: $EXPECTED_APP_IMAGE"

  # Two controllers on one Deployment fight, and the replica trace means nothing.
  if [[ -n "$(kubectl get hpa -n "$NS" -o name 2>/dev/null)" ]]; then
    kubectl delete hpa --all -n "$NS" >/dev/null
    say "removed a leftover HPA: setpoint must be the only controller"
  fi
}

case "${1:-}" in
  up)
    preflight
    stop_load
    set_config predictive 0 1
    kubectl create configmap setpoint-demo-dashboard -n "$MON_NS" \
      --from-file=demo.json="$HERE/grafana-demo.json" --dry-run=client -o yaml \
      | kubectl label --local -f - grafana_dashboard=1 -o yaml | kubectl apply -f - >/dev/null
    start_grafana
    say "ready: fleet at 1, no load, healthy policy loaded"
    echo "  dashboard  http://localhost:3000/d/setpoint-demo/?kiosk&refresh=5s   (admin/admin)"
    echo "  ticker     $0 watch     (in a second terminal)"
    ;;
  healthy)
    preflight
    # `up` already loaded this config, so the first `healthy` of a demo only starts
    # the load: restarting the controller again would be a pause with nothing to show.
    [[ "$(live_config)" == "predictive 0" ]] || set_config predictive 0
    start_load
    say "now: forecasting TOTAL load, stabilizer off"
    ;;
  broken)
    load_running || die "no load running — start with: $0 healthy"
    set_config predictive-per-replica 0 "$FLEET"
    say "now: forecasting PER-POD load, stabilizer off — same traffic, fleet at $FLEET"
    ;;
  masked)
    load_running || die "no load running — start with: $0 healthy"
    set_config predictive-per-replica 90 "$FLEET"
    say "now: the same broken policy, with the 90 s stabilizer the thesis measured — fleet at $FLEET"
    ;;
  kick)
    # Written to .spec.replicas behind the controller's back — the same write a node
    # failure forces on the fleet. The ticker flags it, so a disturbance is never read
    # as a decision.
    #
    # Timed to land just after a decision, so the controller's next look is a full
    # interval away. Rehearsed: the policy rebuilds total load as metric x ready, and
    # ready drops at once while the metric's denominator, count(up), waits for the next
    # 5 s scrape. A reconcile 4 s after the kick read 154 req/s of a real 550 and the
    # healthy policy dipped to 1 pod for a cycle; 14 s after, it went 2 -> 6 directly.
    # Same stimulus for every act, then, or the acts are not comparable.
    last="$(decision_stamp)"
    if [[ -n "$last" ]]; then
      printf 'kicking right after the next decision'
      for _ in $(seq 1 40); do
        [[ "$(decision_stamp)" != "$last" ]] && break
        printf '.'; sleep 0.5
      done
      echo
    fi
    kubectl scale deploy/sample -n "$NS" --replicas="$KICK_TO" >/dev/null
    say "kick: fleet knocked down to $KICK_TO pods — traffic unchanged"
    ;;
  stage)
    # The ticker, with a short command pane under it, so the room sees each command.
    tmux kill-session -t setpoint-demo 2>/dev/null || true
    tmux new-session -d -s setpoint-demo -c "$HERE" "$HERE/demo.sh watch"
    tmux split-window -v -l 4 -t setpoint-demo -c "$HERE"
    tmux set -t setpoint-demo status off >/dev/null
    exec tmux attach -t setpoint-demo
    ;;
  down)
    stop_load
    kubectl apply -f "$REPO/deploy/setpoint/configmap.yaml" >/dev/null
    kubectl rollout restart deploy/setpoint -n "$NS" >/dev/null
    kubectl rollout status deploy/setpoint -n "$NS" --timeout=90s >/dev/null
    stop_grafana
    say "load stopped; committed config restored (predictive, 90 s stabilizer)"
    ;;
  watch)
    exec python3 "$HERE/watch.py" "${@:2}"
    ;;
  code)
    cd "$REPO"
    at() { echo "$1:$(grep -n "$2" "$1" | head -1 | cut -d: -f1)"; }
    code -g "$(at internal/policy/policy.go 'func (p \*PredictiveTotalLoad) Decide')" \
            "$(at internal/policy/stabilize.go 'func (s \*Stabilizer) direction')" \
            "$(at internal/policy/policy_test.go 'func TestPerReplicaForecastingOscillatesUnderConstantLoad')"
    ;;
  test)
    cd "$REPO"
    go test ./internal/policy -count=1 -v \
      -run 'TestPerReplicaForecastingOscillatesUnderConstantLoad|TestOscillationSpansTheReplicaRange' \
      | grep -E 'policy_test|^(--- |ok|FAIL)'
    ;;
  *)
    sed -n '3,15p' "$0"; exit 2
    ;;
esac
