// chat_burst.js — k6 load test: single-tenant burst scenario
//
// Simulates a single tenant sending a burst of requests.
// 100 concurrent VUs, ramp up over 30s, sustain for 2m.
//
// Usage:
//   k6 run tests/load/chat_burst.js
//   k6 run --env BASE_URL=http://staging:9091 --env API_KEY=secret tests/load/chat_burst.js

import http from "k6/http";
import { check, sleep } from "k6";
import { Rate, Trend } from "k6/metrics";

const BASE_URL = __ENV.BASE_URL || "http://localhost:9091";
const API_KEY = __ENV.API_KEY || __ENV.AUTH_TOKEN || "test-api-key";

const errorRate = new Rate("errors");
const runLatency = new Trend("run_latency", true);

export const options = {
  stages: [
    { duration: "30s", target: 100 }, // ramp up to 100 VUs over 30s
    { duration: "2m", target: 100 },  // sustain 100 VUs for 2 minutes
    { duration: "10s", target: 0 },   // ramp down
  ],
  thresholds: {
    http_req_duration: ["p(99)<500"],  // p99 latency < 500ms
    errors: ["rate<0.01"],             // error rate < 1%
    http_reqs: ["rate>50"],            // throughput > 50 RPS
  },
};

const headers = {
  "Content-Type": "application/json",
  Authorization: `Bearer ${API_KEY}`,
};

// Pre-generated payload for creating a run.
const runPayload = JSON.stringify({
  agent_id: "load-test-agent",
  input: {
    messages: [
      { role: "user", content: "Hello, this is a load test message." },
    ],
  },
});

export default function () {
  // POST /v1/runs — primary workload
  const createRes = http.post(`${BASE_URL}/v1/runs`, runPayload, { headers });
  runLatency.add(createRes.timings.duration);

  const createOk = check(createRes, {
    "POST /v1/runs status 2xx": (r) => r.status >= 200 && r.status < 300,
  });
  errorRate.add(!createOk);

  // GET /v1/agents — lightweight read
  const agentsRes = http.get(`${BASE_URL}/v1/agents`, { headers });
  const agentsOk = check(agentsRes, {
    "GET /v1/agents status 2xx": (r) => r.status >= 200 && r.status < 300,
  });
  errorRate.add(!agentsOk);

  // GET /v1/runs — list runs
  const listRes = http.get(`${BASE_URL}/v1/runs`, { headers });
  const listOk = check(listRes, {
    "GET /v1/runs status 2xx": (r) => r.status >= 200 && r.status < 300,
  });
  errorRate.add(!listOk);

  sleep(0.5);
}
