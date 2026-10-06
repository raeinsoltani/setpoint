// Constant load for the live demo: the same arrival rate for the whole demo, so
// anything that moves on screen was moved by the controller and not by the traffic.
//
//   RATE=550 k6 run --address localhost:6565 docs/defense/demo/load.js
//
// 550 req/s against a target of 100 per replica means the correct fleet is 6, at
// ~92 req/s each. That sits inside the 10% dead-band, and the noise in a 1m rate()
// (a few percent) stays well inside ceil()'s (500, 600] bucket, so a healthy
// controller has no reason to move off 6.
//
// The connection options are the evaluation's own (test/load/lib/patterns.js), for
// the reason given there: with keep-alive, every connection pins to one pod and the
// fleet the controller builds never receives the load it was built for.
//
// --address exposes k6's REST API, which watch.py polls for the failure rate the
// user sees. That rate is the client's view, and the one the app's own histogram
// cannot see (lab-notebook §11.20).
import http from 'k6/http';
import { CONNECTION_OPTIONS } from '../../../test/load/lib/patterns.js';

const RATE = Number(__ENV.RATE || 550);

export const options = {
  scenarios: {
    constant: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: __ENV.DURATION || '40m',
      preAllocatedVUs: Math.ceil(RATE * 0.25),
      // Generous on purpose. Under the broken policy requests stall for seconds; too
      // few VUs and k6 quietly sends less than RATE, which would look like the
      // service coping. dropped_iterations in the summary says whether this held.
      maxVUs: RATE * 3,
    },
  },
  ...CONNECTION_OPTIONS,
  summaryTrendStats: ['med', 'p(95)', 'p(99)', 'max'],
};

export default function () {
  http.get(`${__ENV.BASE_URL || 'http://localhost:8080'}/`);
}
