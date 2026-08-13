// Three 2-minute bursts to 1200 req/s, each followed by a 1-minute trough at 200.
//
// The anti-flapping stress case: every trough invites a scale-down moments before
// the next burst. The replica-change count from this run is the quantitative
// evidence for the stabilization requirement on proposal page 3.
//
//   k6 run test/load/bursty.js
import http from 'k6/http';
import { check } from 'k6';
import { bursty, scenario, thresholds, CONNECTION_OPTIONS } from './lib/patterns.js';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export const options = {
  scenarios: { bursty: scenario(bursty) },
  thresholds,
  ...CONNECTION_OPTIONS,
};

export default function () {
  const res = http.get(`${BASE_URL}/`);
  check(res, { 'status is 200': (r) => r.status === 200 });
}
