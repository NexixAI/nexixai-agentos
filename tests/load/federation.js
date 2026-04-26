// federation.js — k6 load test: federation forwarding scenario
//
// Simulates 50 VUs hitting the federated endpoint, sustained for 2 minutes.
// Tests the federation layer's ability to forward and proxy requests
// across peer nodes.
//
// Usage:
//   k6 run tests/load/federation.js
//   k6 run --env BASE_URL=http://staging:9091 --env API_KEY=secret tests/load/federation.js

import http from "k6/http";
import { check, sleep } from "k6";
import { Rate, Trend } from "k6/metrics";

const BASE_URL = __ENV.BASE_URL || "http://localhost:9091";
const API_KEY = __ENV.API_KEY || __ENV.AUTH_TOKEN || "test-api-key";

const errorRate = new Rate("errors");
const federatedLatency = new Trend("federated_latency", true);
const eventLatency = new Trend("event_latency", true);

export const options = {
  stages: [
    { duration: "15s", target: 50 }, // ramp up to 50 VUs
    { duration: "2m", target: 50 },  // sustain 50 VUs for 2 minutes
    { duration: "10s", target: 0 },  // ramp down
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

export default function () {
  // Step 1: POST /v1/runs — create a run that may be forwarded via federation
  const runPayload = JSON.stringify({
    agent_id: "federated-load-test-agent",
    input: {
      messages: [
        {
          role: "user",
          content: `Federation load test VU=${__VU} iter=${__ITER}`,
        },
      ],
    },
  });

  const createRes = http.post(`${BASE_URL}/v1/runs`, runPayload, { headers });
  federatedLatency.add(createRes.timings.duration);

  const createOk = check(createRes, {
    "POST /v1/runs status 2xx": (r) => r.status >= 200 && r.status < 300,
  });
  errorRate.add(!createOk);

  // Step 2: POST /v1/runs/{id}/events — stream events on the created run
  if (createRes.status >= 200 && createRes.status < 300) {
    let runId;
    try {
      const body = JSON.parse(createRes.body);
      runId = body.id || body.run_id;
    } catch (_) {
      // If response parsing fails, skip event streaming.
    }

    if (runId) {
      const eventPayload = JSON.stringify({
        type: "message",
        content: `Federation event from VU=${__VU}`,
      });

      const eventRes = http.post(
        `${BASE_URL}/v1/runs/${runId}/events`,
        eventPayload,
        { headers }
      );
      eventLatency.add(eventRes.timings.duration);

      const eventOk = check(eventRes, {
        "POST /v1/runs/{id}/events status 2xx": (r) =>
          r.status >= 200 && r.status < 300,
      });
      errorRate.add(!eventOk);
    }
  }

  // Step 3: GET /v1/agents — read via potentially federated path
  const agentsRes = http.get(`${BASE_URL}/v1/agents`, { headers });
  const agentsOk = check(agentsRes, {
    "GET /v1/agents status 2xx": (r) => r.status >= 200 && r.status < 300,
  });
  errorRate.add(!agentsOk);

  sleep(0.5);
}
