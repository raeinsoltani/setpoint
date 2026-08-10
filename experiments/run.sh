#!/usr/bin/env bash
#
# One measured run: one policy arm, one workload pattern, one cluster.
#
#   ./experiments/run.sh --arm ours-predictive --pattern ramp
#
# A "run" is not just `k6 run`. It is:
#
#   teardown  ->  apply exactly one controller  ->  reset the fleet  ->  warm up
#             ->  measure  ->  settle  ->  capture series  ->  check validity
#
# Every step other than "measure" exists because of something in docs/lab-notebook.md
# §6 that failed *silently* — producing a plausible-looking run whose numbers meant
# something other than what they appeared to mean. The checks below are the point of
# this script; the k6 invocation in the middle is the easy part.
#
# The output of a run is a directory under experiments/results/raw/ containing the
# raw series, the k6 summary, the autoscaler's own logs, the exact config that was
# live, and a run.json recording whether the run is valid and why. Nothing here
# computes an evaluation metric — experiments/analyze.py does that, from these files.
#
set -euo pipefail

# --------------------------------------------------------------------------- #
# Defaults
# --------------------------------------------------------------------------- #
ARM=""
PATTERN=""
TIME_SCALE=1
SMOKE=0
WARMUP_SECONDS=120
SETTLE_SECONDS=120
STEP=5s
PROM_PORT=9090
BASE_URL="http://localhost:8080"
NAMESPACE=default
MON_NS=monitoring
DEPLOYMENT=sample
CONTAINER=sample-app
OUTROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/experiments/results/raw"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The simulator starts its pool at 3 ready replicas (sim/demo/simulate.py,
# ReplicaPool(ready=3)) and gives `static` a fixed 8. Matching those here is what
# lets a cluster run and a simulator run of the same arm be laid side by side.
INITIAL_REPLICAS=3
STATIC_REPLICAS=8

# `static` at 8 is NOT the over-provisioned baseline the simulator's comment claims:
# every pattern peaks at 12-13 required replicas (spike 13, the rest 12), so 8 is close
# to the *mean* requirement and is under-provisioned at every peak. Measured on ramp it
# came in at 35.5% SLA violations — losing to ours-threshold on SLA *and* cost, which
# makes it read as a strawman rather than a baseline.
#
# So there are two static arms, and they are the two honest ends of the tradeoff:
#   static       fixed 8 — provisioned for the mean, cheap, violates under peaks
#   static-peak  ceil(peak(pattern)/target) — provisioned for the peak, safe, expensive
# static-peak is computed from the pattern rather than hardcoded, so it cannot drift
# from the workload the way the 8 did.
STATIC_PEAK_REPLICAS=""   # resolved from the pattern below
PINNED_REPLICAS=null      # what a fixed-fleet arm was actually pinned at; null otherwise

# Both static arms are "no controller, fixed fleet" everywhere outside the case that
# pins the replica count, so every other branch must ask about the class, not the name.
is_static_arm() { [[ "$ARM" == "static" || "$ARM" == "static-peak" ]]; }

# Same preference order as the Makefile: the simulator's venv if it exists, since that
# is where the pattern definitions and their dependencies live.
PYTHON_BIN="$([ -x "$(dirname "${BASH_SOURCE[0]}")/../sim/.venv/bin/python" ] \
  && echo "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/sim/.venv/bin/python" \
  || echo python3)"

ARMS="static static-peak ours-threshold ours-predictive ours-predictive-per-replica hpa-cpu hpa-custom"
PATTERNS="spike diurnal bursty ramp"

usage() {
  cat <<EOF
usage: $0 --arm ARM --pattern PATTERN [options]

  --arm ARM             one of: $ARMS
  --pattern PATTERN     one of: $PATTERNS
  --smoke               allow TIME_SCALE != 1; marks the run non-evaluation-grade
  --time-scale N        compress the time axis (implies --smoke for N != 1)
  --warmup SECONDS      constant-rate warmup before measurement (default $WARMUP_SECONDS)
  --settle SECONDS      keep capturing after the load ends (default $SETTLE_SECONDS)
  --prom-port PORT      local port for the Prometheus port-forward (default $PROM_PORT)
  --base-url URL        sample app URL (default $BASE_URL)
  --out DIR             output root (default experiments/results/raw)
  -h, --help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --arm)        ARM="$2"; shift 2 ;;
    --pattern)    PATTERN="$2"; shift 2 ;;
    --smoke)      SMOKE=1; shift ;;
    --time-scale) TIME_SCALE="$2"; shift 2 ;;
    --warmup)     WARMUP_SECONDS="$2"; shift 2 ;;
    --settle)     SETTLE_SECONDS="$2"; shift 2 ;;
    --prom-port)  PROM_PORT="$2"; shift 2 ;;
    --base-url)   BASE_URL="$2"; shift 2 ;;
    --out)        OUTROOT="$2"; shift 2 ;;
    -h|--help)    usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

die()  { echo "error: $*" >&2; exit 1; }
info() { echo "[$(date -u +%H:%M:%S)] $*"; }
step() { echo; echo "=== $* ==="; }

[[ -n "$ARM" && -n "$PATTERN" ]] || { usage >&2; exit 2; }
grep -qw -- "$ARM" <<<"$ARMS"         || die "unknown arm '$ARM'; expected one of: $ARMS"
grep -qw -- "$PATTERN" <<<"$PATTERNS" || die "unknown pattern '$PATTERN'; expected one of: $PATTERNS"

# TIME_SCALE != 1 is not a knob, it is a different experiment. Compressing the time
# axis changes the ratio between how fast load moves and how long a pod takes to
# become ready — and that ratio *is* the phenomenon under study (lab-notebook §7.2).
# A compressed run is a pipeline check. It must never reach the evaluation chapter,
# so it can only be produced deliberately and is recorded as non-evaluation-grade.
if [[ "$TIME_SCALE" != "1" ]]; then
  [[ "$SMOKE" == "1" ]] || die "TIME_SCALE=$TIME_SCALE requires --smoke: a compressed run is not an evaluation result (lab-notebook §7.2)"
  SMOKE=1
fi

# --------------------------------------------------------------------------- #
# Preflight
# --------------------------------------------------------------------------- #
step "preflight"

for bin in kubectl k6 jq curl; do
  command -v "$bin" >/dev/null || die "$bin not found in PATH"
done

CONTEXT="$(kubectl config current-context)"
kubectl get ns "$NAMESPACE" >/dev/null 2>&1 || die "namespace $NAMESPACE not reachable in context $CONTEXT"
kubectl get deploy "$DEPLOYMENT" -n "$NAMESPACE" >/dev/null 2>&1 \
  || die "deployment/$DEPLOYMENT missing — run 'make stack-up' first"
kubectl get sts -n "$MON_NS" prometheus-monitoring-kube-prometheus-prometheus >/dev/null 2>&1 \
  || die "kube-prometheus-stack not installed in ns/$MON_NS — run 'make monitoring'"

info "context      $CONTEXT"
info "arm          $ARM"
info "pattern      $PATTERN"
info "time scale   $TIME_SCALE$([[ $SMOKE == 1 ]] && echo '  (SMOKE — not an evaluation result)')"

# --------------------------------------------------------------------------- #
# Prometheus port-forward, owned by this script and torn down with it
# --------------------------------------------------------------------------- #
PF_PID=""
PF_STOP=""
cleanup() {
  local rc=$?
  # Remove the sentinel first so the supervisor loop does not respawn the child
  # we are about to kill.
  [[ -n "$PF_STOP" ]] && rm -f "$PF_STOP"
  if [[ -n "$PF_PID" ]]; then
    pkill -P "$PF_PID" 2>/dev/null || true
    kill "$PF_PID" 2>/dev/null || true
  fi
  exit $rc
}
trap cleanup EXIT INT TERM

# prom_ready [SECONDS] -> 0 once Prometheus answers on the local port.
prom_ready() {
  local deadline=$(( $(date +%s) + ${1:-30} ))
  while (( $(date +%s) < deadline )); do
    curl -sf "http://localhost:$PROM_PORT/-/ready" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

# `kubectl port-forward` is not durable: it drops on an apiserver hiccup, a Prometheus
# pod restart, or an idle connection reset. A run queries Prometheus during warmup and
# then not again until capture ~30 minutes later, so a forward that dies inside that gap
# takes the entire run with it — the load was driven, the series were never read, and
# the failure surfaces only at the very end. Supervise it: respawn until the sentinel
# file disappears, which cleanup does on the way out.
if curl -sf "http://localhost:$PROM_PORT/-/ready" >/dev/null 2>&1; then
  info "prometheus already reachable on :$PROM_PORT (reusing)"
else
  PF_STOP="$(mktemp -t setpoint-pf)"
  (
    while [[ -f "$PF_STOP" ]]; do
      kubectl port-forward -n "$MON_NS" svc/monitoring-kube-prometheus-prometheus \
        "$PROM_PORT:9090" >/dev/null 2>&1 || true
      sleep 1
    done
  ) &
  PF_PID=$!
  prom_ready 30 || die "prometheus did not become reachable on :$PROM_PORT"
  info "prometheus port-forward up on :$PROM_PORT (supervised, pid $PF_PID)"
fi

# promq QUERY [TIME] -> scalar value of the first sample, or "" if the query is empty.
#
# The assignments are on separate lines deliberately: `local a="$1" b="$a"` expands
# every word before applying any assignment, so a reference to an earlier variable in
# the same `local` sees the old (here: unset) value.
promq() {
  local q="$1"
  local t="${2:-}"
  local args=(--get "http://localhost:$PROM_PORT/api/v1/query" --data-urlencode "query=$q")
  [[ -n "$t" ]] && args+=(--data-urlencode "time=$t")
  curl -s "${args[@]}" | jq -r '.data.result[0].value[1] // ""'
}

# --------------------------------------------------------------------------- #
# Teardown: leave exactly zero controllers owning .spec.replicas
# --------------------------------------------------------------------------- #
# Two controllers writing the same Deployment fight, and the resulting replica trace
# is uninterpretable — the run looks fine and means nothing. This runs before every
# arm, including `static`, and is unconditional rather than conditional on what the
# previous run left behind, because the previous run may have been killed halfway.
step "teardown: removing every controller"

kubectl delete hpa sample-hpa-cpu sample-hpa-custom -n "$NAMESPACE" --ignore-not-found >/dev/null
kubectl delete scaledobject sample-keda -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1 || true
kubectl scale deploy/setpoint -n "$NAMESPACE" --replicas=0 >/dev/null
kubectl wait --for=delete pod -l app.kubernetes.io/name=setpoint -n "$NAMESPACE" --timeout=60s >/dev/null 2>&1 || true

remaining="$(kubectl get hpa -n "$NAMESPACE" -o name 2>/dev/null | wc -l | tr -d ' ')"
[[ "$remaining" == "0" ]] || die "an HPA still exists after teardown; refusing to run"
info "no HPA, no autoscaler pod"

# --------------------------------------------------------------------------- #
# Apply exactly one arm
# --------------------------------------------------------------------------- #
step "applying arm: $ARM"

CONFIG_APPLIED=""

apply_setpoint_with_policy() {
  local policy="$1"
  local tmp; tmp="$(mktemp -t setpoint-cm)"

  # Rewrite only `name:` inside the `policy:` block, leaving every comment in the
  # ConfigMap intact. A blanket sed would also hit metadata.name.
  awk -v p="$policy" '
    /^    policy:/ { inpolicy = 1 }
    /^    scaler:/ { inpolicy = 0 }
    inpolicy && /^      name:/ && !done { sub(/name:[[:space:]]*[a-z-]+/, "name: " p); done = 1 }
    { print }
  ' "$REPO/deploy/setpoint/configmap.yaml" > "$tmp"

  grep -q "name: $policy " "$tmp" || grep -q "name: $policy$" "$tmp" \
    || die "failed to set policy.name=$policy in the generated ConfigMap"

  kubectl apply -f "$tmp" >/dev/null
  CONFIG_APPLIED="$tmp"

  # A ConfigMap change does NOT restart the pod, and a mounted ConfigMap updates only
  # on the kubelet's own sync period. Without this restart the previous arm's policy
  # keeps running under the new arm's name — the single most dangerous silent failure
  # this script can have, because every downstream number would be attributed wrongly.
  kubectl scale deploy/setpoint -n "$NAMESPACE" --replicas=1 >/dev/null
  kubectl rollout restart deploy/setpoint -n "$NAMESPACE" >/dev/null
  kubectl rollout status deploy/setpoint -n "$NAMESPACE" --timeout=120s >/dev/null

  # Read the policy back out of the live ConfigMap rather than trusting the apply.
  local live
  live="$(kubectl get cm setpoint-config -n "$NAMESPACE" -o jsonpath='{.data.config\.yaml}' \
          | awk '/^policy:/{p=1} p && /^  name:/{print $2; exit}')"
  [[ "$live" == "$policy" ]] || die "live ConfigMap has policy.name=$live, expected $policy"
  info "setpoint running policy '$policy' (verified against the live ConfigMap)"
}

case "$ARM" in
  static)
    kubectl scale deploy/"$DEPLOYMENT" -n "$NAMESPACE" --replicas="$STATIC_REPLICAS" >/dev/null
    PINNED_REPLICAS="$STATIC_REPLICAS"
    info "no controller; fleet pinned at $STATIC_REPLICAS replicas (mean-provisioned)"
    ;;
  static-peak)
    # Asks the simulator for the pattern's peak, for the same reason analyze.py imports
    # it: the peak must come from the workload definition that is actually driven, not
    # from a number typed here that can silently stop matching it.
    STATIC_PEAK_REPLICAS="$("$PYTHON_BIN" -c "
import math, sys
sys.path.insert(0, '$REPO/sim/demo')
from simulate import PATTERNS, TARGET, DURATION
peak = max(float(PATTERNS['$PATTERN'](float(t))) for t in range(int(DURATION)))
print(math.ceil(peak / TARGET))
")" || die "could not compute the peak requirement for pattern '$PATTERN'"
    [[ "$STATIC_PEAK_REPLICAS" =~ ^[0-9]+$ ]] \
      || die "peak requirement for '$PATTERN' came back as '$STATIC_PEAK_REPLICAS'"
    kubectl scale deploy/"$DEPLOYMENT" -n "$NAMESPACE" --replicas="$STATIC_PEAK_REPLICAS" >/dev/null
    PINNED_REPLICAS="$STATIC_PEAK_REPLICAS"
    info "no controller; fleet pinned at $STATIC_PEAK_REPLICAS replicas (peak-provisioned for $PATTERN)"
    ;;
  ours-threshold)               apply_setpoint_with_policy threshold ;;
  ours-predictive)              apply_setpoint_with_policy predictive ;;
  ours-predictive-per-replica)  apply_setpoint_with_policy predictive-per-replica ;;
  hpa-cpu)
    kubectl get apiservice v1beta1.metrics.k8s.io >/dev/null 2>&1 \
      || die "hpa-cpu needs metrics-server:
  helm repo add metrics-server https://kubernetes-sigs.github.io/metrics-server/
  helm upgrade --install metrics-server metrics-server/metrics-server -n kube-system --set args={--kubelet-insecure-tls}"
    kubectl apply -f "$REPO/deploy/hpa/hpa-cpu.yaml" >/dev/null
    info "applied HorizontalPodAutoscaler/sample-hpa-cpu"
    ;;
  hpa-custom)
    kubectl get apiservice v1beta1.custom.metrics.k8s.io >/dev/null 2>&1 \
      || die "hpa-custom needs prometheus-adapter with the rule documented in deploy/hpa/hpa-custom.yaml"
    kubectl apply -f "$REPO/deploy/hpa/hpa-custom.yaml" >/dev/null
    info "applied HorizontalPodAutoscaler/sample-hpa-custom"
    ;;
esac

# --------------------------------------------------------------------------- #
# Reset the fleet to a known starting point
# --------------------------------------------------------------------------- #
if ! is_static_arm; then
  step "resetting fleet to $INITIAL_REPLICAS replicas"
  kubectl scale deploy/"$DEPLOYMENT" -n "$NAMESPACE" --replicas="$INITIAL_REPLICAS" >/dev/null
fi

kubectl rollout status deploy/"$DEPLOYMENT" -n "$NAMESPACE" --timeout=180s >/dev/null
info "fleet ready"

# --------------------------------------------------------------------------- #
# Warmup
# --------------------------------------------------------------------------- #
# Two independent reasons, both of which produce a silently wrong run if skipped:
#
#   1. `http_requests_total` does not exist until the first request (§6.4), and
#      `rate(...[1m])` needs a full minute of samples before it means anything. A
#      measurement starting at t=0 measures the metric pipeline filling up.
#   2. Each policy should enter the measured window at *its own* equilibrium for the
#      pattern's starting load, not at an arbitrary replica count. Otherwise the
#      first minute of every trace is a common transient that dilutes the contrast.
step "warmup: ${WARMUP_SECONDS}s at the pattern's t=0 rate"

WARMUP_LOG="$(mktemp -t warmup-log)"
PATTERN="$PATTERN" WARMUP_SECONDS="$WARMUP_SECONDS" BASE_URL="$BASE_URL" \
  k6 run --quiet "$REPO/experiments/warmup.js" >"$WARMUP_LOG" 2>&1 \
  || { cat "$WARMUP_LOG"; die "warmup load failed — is the sample app reachable at $BASE_URL?"; }

# Gate on the metric pipeline actually working. §6.4 lists three ways this returns an
# empty vector while every component reports healthy; the autoscaler then reads zero
# load, holds at min_replicas, and the run looks like a policy failure.
observed="$(promq 'sum(rate(http_requests_total{app="sample"}[1m]))')"
[[ -n "$observed" ]] || die "no http_requests_total after warmup — check the ServiceMonitor's targetLabels: [app] (§6.4)"
awk -v v="$observed" 'BEGIN { exit !(v > 0) }' \
  || die "http_requests_total rate is $observed after warmup; the metric pipeline is not carrying load"

targets="$(promq 'count(up{app="sample"} == 1)')"
ready="$(promq "kube_deployment_status_replicas_ready{deployment=\"$DEPLOYMENT\",namespace=\"$NAMESPACE\"}")"
[[ "${targets:-0}" == "${ready:-x}" ]] \
  || die "scrape targets ($targets) != ready replicas ($ready); the fleet is not fully observable"
info "metric pipeline live: ${observed} req/s across $targets scrape targets"

# An HPA whose metric is still `<unknown>` does not scale at all, and reports the
# condition only in `.status.conditions` — from the outside it looks exactly like a
# policy that chose not to act. Gate on it having a real reading before measuring.
if [[ "$ARM" == hpa-* ]]; then
  hpa_name="sample-${ARM}"
  for _ in $(seq 1 12); do
    cur="$(kubectl get hpa "$hpa_name" -n "$NAMESPACE" -o jsonpath='{.status.currentMetrics}' 2>/dev/null || true)"
    [[ -n "$cur" && "$cur" != "null" && "$cur" != *"<unknown>"* ]] && break
    sleep 5
  done
  [[ -n "$cur" && "$cur" != "null" && "$cur" != *"<unknown>"* ]] \
    || die "$hpa_name still reports no metric after warmup; it would sit at minReplicas for the whole run"
  info "$hpa_name is reading its metric"
fi

spec_before="$(promq "kube_deployment_spec_replicas{deployment=\"$DEPLOYMENT\",namespace=\"$NAMESPACE\"}")"

# --------------------------------------------------------------------------- #
# Measure
# --------------------------------------------------------------------------- #
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUTDIR="$OUTROOT/${PATTERN}__${ARM}__${STAMP}"
mkdir -p "$OUTDIR"
[[ -n "$CONFIG_APPLIED" ]] && cp "$CONFIG_APPLIED" "$OUTDIR/configmap-applied.yaml"
mv "$WARMUP_LOG" "$OUTDIR/k6-warmup.log"

step "measuring: $PATTERN under $ARM"
info "output -> $OUTDIR"

T_START="$(date -u +%s)"
set +e
BASE_URL="$BASE_URL" TIME_SCALE="$TIME_SCALE" \
  k6 run --summary-export="$OUTDIR/k6-summary.json" "$REPO/test/load/$PATTERN.js" \
  2>&1 | tee "$OUTDIR/k6-stdout.log"
K6_RC="${PIPESTATUS[0]}"
set -e
T_END="$(date -u +%s)"

# 99 is k6's exit code for a failed threshold. Thresholds here are recorded, not
# gating: a breach under a deliberately overloaded arm is the result being measured.
[[ "$K6_RC" == "0" || "$K6_RC" == "99" ]] || die "k6 exited $K6_RC"

step "settling for ${SETTLE_SECONDS}s (captures scale-down after the load ends)"
sleep "$SETTLE_SECONDS"
T_CAPTURE_END="$(date -u +%s)"
T_CAPTURE_START=$((T_START - WARMUP_SECONDS))

# --------------------------------------------------------------------------- #
# Capture
# --------------------------------------------------------------------------- #
step "capturing series"

# The forward may have dropped and been respawned at any point during the ~30 quiet
# minutes above. Prometheus itself kept scraping throughout — the data is there either
# way — so this waits for the tunnel rather than failing the run over it.
prom_ready 60 || die "prometheus unreachable on :$PROM_PORT at capture time"

# `total_rps` is offered load, not served load: shed requests are counted in
# http_requests_total precisely so that offered load stays measurable under overload
# (§6.2). `per_replica_rps` mirrors the collector query in the ConfigMap; for the
# ours-* arms it is cross-checked against autoscaler_metric_value in analyze.py,
# which is what the policy actually saw.
declare -a SERIES=(
  "total_rps|sum(rate(http_requests_total{app=\"sample\"}[1m]))"
  "shed_rps|sum(rate(http_requests_shed_total{app=\"sample\"}[1m]))"
  "per_replica_rps|sum(rate(http_requests_total{app=\"sample\"}[1m])) / clamp_min(count(up{app=\"sample\"} == 1), 1)"
  "ready_replicas|kube_deployment_status_replicas_ready{deployment=\"$DEPLOYMENT\",namespace=\"$NAMESPACE\"}"
  "spec_replicas|kube_deployment_spec_replicas{deployment=\"$DEPLOYMENT\",namespace=\"$NAMESPACE\"}"
  "scrape_targets|count(up{app=\"sample\"} == 1)"
  "in_flight|sum(http_requests_in_flight{app=\"sample\"})"
  "cpu_cores|sum(rate(container_cpu_usage_seconds_total{namespace=\"$NAMESPACE\",container=\"$CONTAINER\"}[1m]))"
  "restarts|sum(kube_pod_container_status_restarts_total{namespace=\"$NAMESPACE\",container=\"$CONTAINER\"})"
  "latency_p50|histogram_quantile(0.50, sum by (le) (rate(http_request_duration_seconds_bucket{app=\"sample\"}[1m])))"
  "latency_p95|histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{app=\"sample\"}[1m])))"
  "latency_p99|histogram_quantile(0.99, sum by (le) (rate(http_request_duration_seconds_bucket{app=\"sample\"}[1m])))"
  "autoscaler_metric_value|autoscaler_metric_value"
  "autoscaler_predicted_value|autoscaler_predicted_value"
  "autoscaler_desired_replicas|autoscaler_desired_replicas"
  "autoscaler_ready_replicas|autoscaler_ready_replicas"
  "autoscaler_raw_recommendation|autoscaler_raw_recommendation"
)

PARTS="$(mktemp -d -t series-parts)"
for entry in "${SERIES[@]}"; do
  name="${entry%%|*}"
  query="${entry#*|}"
  curl -s --get "http://localhost:$PROM_PORT/api/v1/query_range" \
    --data-urlencode "query=$query" \
    --data-urlencode "start=$T_CAPTURE_START" \
    --data-urlencode "end=$T_CAPTURE_END" \
    --data-urlencode "step=$STEP" \
  | jq --arg n "$name" --arg q "$query" \
      '{($n): {query: $q, status: .status, result: (.data.result // [])}}' \
    > "$PARTS/$name.json"
done
jq -s 'reduce .[] as $x ({}; . + $x)' "$PARTS"/*.json > "$OUTDIR/series.json"
rm -rf "$PARTS"

captured="$(jq -r '[to_entries[] | select(.value.result | length > 0)] | length' "$OUTDIR/series.json")"
total="$(jq -r 'length' "$OUTDIR/series.json")"
info "captured $captured/$total series with data"

# Window-wide latency quantiles, from the application's own histogram.
#
# k6's --summary-export carries only p(90) and p(95); it evaluates a p(99) threshold
# but does not export the value. The evaluation reports p99 (lab-notebook §7.2), so it
# has to come from the histogram — and a single rate() over the whole measurement
# window is the right aggregate, unlike the [1m] series above, which is a *series* of
# rolling quantiles and cannot be collapsed into one number after the fact.
num_or_null() {
  [[ "$1" =~ ^-?[0-9]+([.][0-9]+)?([eE][-+]?[0-9]+)?$ ]] && echo "$1" || echo null
}
MEASURED_SECONDS=$((T_END - T_START))
lat_bucket="sum by (le) (rate(http_request_duration_seconds_bucket{app=\"sample\"}[${MEASURED_SECONDS}s]))"
LAT_P50="$(num_or_null "$(promq "histogram_quantile(0.50, $lat_bucket)" "$T_END")")"
LAT_P95="$(num_or_null "$(promq "histogram_quantile(0.95, $lat_bucket)" "$T_END")")"
LAT_P99="$(num_or_null "$(promq "histogram_quantile(0.99, $lat_bucket)" "$T_END")")"
info "window latency p50/p95/p99 = ${LAT_P50}/${LAT_P95}/${LAT_P99} s"

# The autoscaler's own structured log is the artefact §7.1 calls the best defense
# demo — it shows capacity being added while the observed metric is still below
# target. Worth keeping per run, not just per project.
if [[ "$ARM" == ours-* ]]; then
  kubectl logs deploy/setpoint -n "$NAMESPACE" --tail=-1 > "$OUTDIR/setpoint.log" 2>/dev/null || true
fi
kubectl get events -n "$NAMESPACE" --sort-by=.lastTimestamp > "$OUTDIR/events.txt" 2>/dev/null || true

# --------------------------------------------------------------------------- #
# Validity
# --------------------------------------------------------------------------- #
# A run that fails these is not a bad result, it is not a result. Recording the
# reasons in run.json means analyze.py can exclude it automatically rather than
# relying on someone remembering which afternoon's runs were the broken ones.
step "validity"

REASONS=()

# Did the host stay awake for the whole measurement?
#
# `caffeinate -i` asserts PreventUserIdleSystemSleep, which does not stop a lid-close or
# a forced sleep. On 2026-08-09 the machine slept ~3.5 h in total across ~100 sleep/wake
# cycles and stretched a 30-minute ours-predictive run to 7.7 h of wall clock — every
# series smeared across a window 15x too long, and k6 unable to offer the pattern. That
# run was caught, but by the *dropped-iterations* gate, whose message blames the load
# generator. A run destroyed by the host sleeping must say so, or the next person reads
# "k6 under-delivered" and goes looking in the wrong place.
EXPECTED_SECONDS="$("$PYTHON_BIN" -c "
import sys
sys.path.insert(0, '$REPO/sim/demo')
from simulate import DURATION
print(int(DURATION / $TIME_SCALE))
" 2>/dev/null || echo 0)"
if [[ "$EXPECTED_SECONDS" =~ ^[0-9]+$ ]] && (( EXPECTED_SECONDS > 0 )); then
  # 5% covers k6's startup and teardown; anything beyond it is lost wall clock.
  limit=$(( EXPECTED_SECONDS * 105 / 100 ))
  (( MEASURED_SECONDS <= limit )) || REASONS+=("measurement window was ${MEASURED_SECONDS}s against an expected ${EXPECTED_SECONDS}s: the host lost wall-clock time (sleep/suspend) and every series is stretched")
fi

# Restarts must be counted with increase() over the per-pod series, not as a delta of
# sum() across the fleet. The counter is per pod and cumulative over that pod's life, so
# a sum() taken at two instants compares two *different pod sets*: any arm that scales
# down deletes pods, their counts leave the sum, and the delta goes negative. That is not
# hypothetical — the 2026-08-09 ours-predictive run was failed by "restarted -1 time(s)".
# increase() is evaluated per series and summed after, so pod churn cannot make it
# negative, and a pod created mid-run is a new series starting at 0 rather than a reset.
RUN_SECONDS=$((T_CAPTURE_END - T_CAPTURE_START))
restarts_raw="$(promq "sum(increase(kube_pod_container_status_restarts_total{namespace=\"$NAMESPACE\",container=\"$CONTAINER\"}[${RUN_SECONDS}s]))" "$T_CAPTURE_END")"
# increase() extrapolates, so one real restart lands near but not exactly on 1.0.
RESTARTS="$(awk -v v="${restarts_raw:-0}" 'BEGIN { printf "%d", (v < 0 ? 0 : int(v + 0.5)) }')"
(( RESTARTS == 0 )) || REASONS+=("sample-app restarted $RESTARTS time(s) during the run: metric history is destroyed and the fleet was serving degraded (§6.2)")

DROPPED="$(jq -r '.metrics.dropped_iterations.count // .metrics.dropped_iterations.values.count // 0' "$OUTDIR/k6-summary.json")"
HTTP_REQS="$(jq -r '.metrics.http_reqs.count // .metrics.http_reqs.values.count // 0' "$OUTDIR/k6-summary.json")"
awk -v d="${DROPPED:-0}" -v r="${HTTP_REQS:-0}" 'BEGIN { exit !(r > 0 && d / (d + r) > 0.01) }' \
  && REASONS+=("k6 dropped $DROPPED of $((${DROPPED%%.*} + ${HTTP_REQS%%.*})) iterations (>1%): offered load was below the pattern, which flatters the arm")

# Count changes in spec.replicas — the quantity a controller writes. Zero under an
# autoscaled arm means the controller was not attached; non-zero under `static`
# means one still is.
changes="$(jq -r '
  [.spec_replicas.result[0].values[]? | .[1] | tonumber] as $v
  | if ($v | length) < 2 then 0
    else [range(1; $v | length) | select($v[.] != $v[. - 1])] | length
    end' "$OUTDIR/series.json" 2>/dev/null || echo 0)"
if ! is_static_arm && [[ "${changes:-0}" == "0" ]]; then
  REASONS+=("spec.replicas never changed under an autoscaled arm: the controller was not driving the Deployment")
fi
if is_static_arm && [[ "${changes:-0}" != "0" ]]; then
  REASONS+=("spec.replicas changed $changes times under the $ARM arm: a controller is still attached")
fi

[[ "$captured" == "$total" ]] || info "note: $((total - captured)) series empty (expected for autoscaler_* on non-ours arms)"

# `valid` and `smoke` are deliberately separate axes. `valid` answers "did the run
# mechanics work" — a run that fails it is not a result at all and cannot be fixed by
# reinterpretation. `smoke` answers "is this evaluation-grade" — the mechanics may be
# perfect and the numbers still belong nowhere near the evaluation chapter. Folding
# the second into the first would make a working pipeline check look like a broken run.
if (( ${#REASONS[@]} == 0 )); then
  VALID=true
  info "valid"
else
  VALID=false
  for r in "${REASONS[@]}"; do echo "  INVALID: $r"; done
fi

if [[ "$SMOKE" == "1" ]]; then
  echo "  SMOKE: TIME_SCALE=$TIME_SCALE — pipeline check only, excluded from the evaluation (§7.2)"
fi

# --------------------------------------------------------------------------- #
# run.json
# --------------------------------------------------------------------------- #
jq -n \
  --arg arm "$ARM" --arg pattern "$PATTERN" --arg stamp "$STAMP" \
  --arg context "$CONTEXT" --arg namespace "$NAMESPACE" --arg deployment "$DEPLOYMENT" \
  --argjson time_scale "$TIME_SCALE" --argjson smoke "$([[ $SMOKE == 1 ]] && echo true || echo false)" \
  --argjson valid "$VALID" \
  --argjson warmup "$WARMUP_SECONDS" --argjson settle "$SETTLE_SECONDS" --arg step "$STEP" \
  --argjson t_start "$T_START" --argjson t_end "$T_END" \
  --argjson t_capture_start "$T_CAPTURE_START" --argjson t_capture_end "$T_CAPTURE_END" \
  --argjson initial_replicas "$INITIAL_REPLICAS" --argjson static_replicas "$STATIC_REPLICAS" \
  --argjson pinned_replicas "$PINNED_REPLICAS" \
  --argjson restarts "$RESTARTS" --argjson k6_rc "$K6_RC" \
  --argjson lat_p50 "$LAT_P50" --argjson lat_p95 "$LAT_P95" --argjson lat_p99 "$LAT_P99" \
  --arg git_sha "$(git -C "$REPO" rev-parse HEAD)" \
  --arg git_dirty "$(git -C "$REPO" status --porcelain | head -c 1 | wc -c | tr -d ' ')" \
  --arg k6_version "$(k6 version 2>/dev/null | head -1)" \
  --arg kubectl_version "$(kubectl version -o json 2>/dev/null | jq -r '.serverVersion.gitVersion // "unknown"')" \
  --argjson reasons "$(printf '%s\n' "${REASONS[@]+"${REASONS[@]}"}" | jq -R . | jq -s 'map(select(. != ""))')" \
  '{
     arm: $arm, pattern: $pattern, timestamp: $stamp,
     valid: $valid, invalid_reasons: $reasons, smoke: $smoke, time_scale: $time_scale,
     window: {
       measure_start: $t_start, measure_end: $t_end,
       capture_start: $t_capture_start, capture_end: $t_capture_end,
       warmup_seconds: $warmup, settle_seconds: $settle, step: $step
     },
     fleet: { initial_replicas: $initial_replicas, static_replicas: $static_replicas,
              pinned_replicas: $pinned_replicas, container_restarts: $restarts },
     latency_window_seconds: { p50: $lat_p50, p95: $lat_p95, p99: $lat_p99 },
     cluster: { context: $context, namespace: $namespace, deployment: $deployment,
                kubernetes: $kubectl_version },
     tooling: { k6: $k6_version, git_sha: $git_sha, git_dirty: ($git_dirty != "0") },
     k6_exit_code: $k6_rc
   }' > "$OUTDIR/run.json"

step "done"
echo "  $OUTDIR"
echo
echo "  next:  python3 experiments/analyze.py"
