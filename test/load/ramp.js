// A single sustained trend: 200 req/s, rising linearly to 1200 over 20 minutes.
//
// Added for this project because the three original patterns do not contain the
// workload predictive scaling is actually for. `spike` and `bursty` are step
// functions no forecaster can anticipate; `diurnal` trends but also turns. This one
// gives a forecaster a clean trend to extrapolate, and is where the predictive arm
// should show its largest advantage. If it does not win here, it does not win.
//
//   k6 run test/load/ramp.js
import http from 'k6/http';
import { check } from 'k6';
import { ramp, scenario, thresholds, CONNECTION_OPTIONS } from './lib/patterns.js';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export const options = {
  scenarios: { ramp: scenario(ramp) },
  thresholds,
  ...CONNECTION_OPTIONS,
};

export default function () {
  const res = http.get(`${BASE_URL}/`);
  check(res, { 'status is 200': (r) => r.status === 200 });
}
