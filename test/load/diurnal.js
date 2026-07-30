// Smooth daily traffic curve: 300 -> 1200 -> 300 req/s over 30 minutes.
// Prediction's best case among the three original patterns.
//
//   k6 run test/load/diurnal.js
import http from 'k6/http';
import { check } from 'k6';
import { diurnal, scenario, thresholds } from './lib/patterns.js';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export const options = {
  scenarios: { diurnal: scenario(diurnal) },
  thresholds,
};

export default function () {
  const res = http.get(`${BASE_URL}/`);
  check(res, { 'status is 200': (r) => r.status === 200 });
}
