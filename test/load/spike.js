// Sudden 4x step: 300 -> 1300 req/s for ten minutes, then back.
//
//   k6 run test/load/spike.js
//   BASE_URL=http://localhost:8080 k6 run --summary-export=spike.json test/load/spike.js
//
// Reach the service with:
//   kubectl port-forward svc/sample 8080:80
import http from 'k6/http';
import { check } from 'k6';
import { spike, scenario, thresholds } from './lib/patterns.js';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export const options = {
  scenarios: { spike: scenario(spike) },
  thresholds,
};

export default function () {
  const res = http.get(`${BASE_URL}/`);
  check(res, { 'status is 200': (r) => r.status === 200 });
}
