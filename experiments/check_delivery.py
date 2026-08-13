import json, glob, math, os

def spike(t):   return 1300 if 600 <= t < 1200 else 300
def diurnal(t):
    s = math.sin(math.pi * t / 1800); return 300 + 900 * s * s
def bursty(t):
    for st in (300, 800, 1300):
        if st <= t < st+120: return 1200
        if st+120 <= t < st+180: return 200
    return 400
def ramp(t):
    if t < 300: return 200
    if t < 1500: return 200 + 1000*(t-300)/1200
    return 1200
PAT = dict(spike=spike, diurnal=diurnal, bursty=bursty, ramp=ramp)

# Plateau windows only: regions where the intended rate is constant for >=150s, sampled
# 90s after the edge so the 1m rate() window is fully inside the plateau.
PLATEAU = {
    'spike':   [(700, 1150)],
    'diurnal': [(800, 1000)],
    'bursty':  [(400, 410), (900, 910), (1400, 1410)],
    'ramp':    [(1600, 1790)],
}

rows = []
for d in sorted(glob.glob('experiments/results/raw/*/')):
    try:
        rj = json.load(open(os.path.join(d, 'run.json')))
        sj = json.load(open(os.path.join(d, 'series.json')))
    except Exception:
        continue
    if rj.get('smoke') or not rj.get('valid'):
        continue
    pat, arm = rj['pattern'], rj['arm']
    t0 = rj['window']['measure_start']
    res = sj['total_rps']['result']
    if not res:
        continue
    vals = [(int(ts) - t0, float(v)) for ts, v in res[0]['values']]
    f = PAT[pat]
    worst = None
    for lo, hi in PLATEAU[pat]:
        seg = [(t, v) for t, v in vals if lo <= t <= hi]
        if not seg:
            continue
        intended = f(seg[0][0])
        delivered = sum(v for _, v in seg) / len(seg)
        ratio = delivered / intended
        if worst is None or ratio < worst[2]:
            worst = (intended, delivered, ratio)
    if worst:
        rows.append((pat, arm, rj['timestamp'], *worst))

rows.sort(key=lambda r: r[5])
print(f"{'pattern':8} {'arm':36} {'intended':>9} {'delivered':>10} {'ratio':>7}")
for pat, arm, ts, i, dv, r in rows:
    flag = '  <-- SHORTFALL' if r < 0.95 else ''
    print(f"{pat:8} {arm:36} {i:9.0f} {dv:10.1f} {r:7.3f}{flag}")
print(f"\nruns checked: {len(rows)}   worst ratio: {min(r[5] for r in rows):.3f}")
