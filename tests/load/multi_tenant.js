// multi_tenant.js — k6 load test: multi-tenant steady-state scenario
//
// Simulates 10 tenants each sending 10 RPS, sustained for 5 minutes.
// Total target: 100 RPS across 10 isolated tenant contexts.
//
// Usage:
//   k6 run tests/load/multi_tenant.js
//   k6 run --env BASE_URL=http://staging:9091 --env API_KEY=secret tests/load/multi_tenant.js

import http from "k6/http";
import { check, sleep } from "k6";
import { Rate, Trend } from "k6/metrics";
import { SharedArray } from "k6/data";

const BASE_URL = __ENV.BASE_URL || "http://localhost:9091";
const API_KEY = __ENV.API_KEY || __ENV.AUTH_TOKEN || "test-api-key";

const errorRate = new Rate("errors");
const runLatency = new Trend("run_latency", true);

const TENANT_COUNT = 10;
const RPS_PER_TENANT = 10;
const TOTAL_VUS = TENANT_COUNT * RPS_PER_TENANT; // 100 VUs

const tenants = new SharedArray("tenants", function () {
  const arr = [];
  for (let i = 0; i < TENANT_COUNT; i++) {
    arr.push(`load-test-tenant-${i}`);
  }
  return arr;
});

export const options = {
  stages: [
    { duration: "30s", target: TOTAL_VUS }, // ramp up
    { duration: "5m", target: TOTAL_VUS },  // sustain 10 tenants x 10 RPS
    { duration: "15s", target: 0 },         // ramp down
  ],
  thresholds: {
    http_req_duration: ["p(99)<500"],  // p99 latency < 500ms
    errors: ["rate<0.01"],             // error rate < 1%
    http_reqs: ["rate>50"],            // throughput > 50 RPS
  },
};

export default function () {
  // Each VU maps to a tenant based on VU id.
  const tenantId = tenants[__VU % TENANT_COUNT];

  const headers = {
    "Content-Type": "application/json",
    Authorization: `Bearer ${API_KEY}`,
    "X-Tenant-ID": tenantId,
  };

  // POST /v1/runs — create a run scoped to this tenant
  const payload = JSON.stringify({
    agent_id: "load-test-agent",
    input: {
      messages: [
        {
          role: "user",
          content: `Tenant ${tenantId} load test iteration ${__ITER}`,
        },
      ],
    },
  });

  const createRes = http.post(`${BASE_URL}/v1/runs`, payload, { headers });
  runLatency.add(createRes.timings.duration);

  const createOk = check(createRes, {
    "POST /v1/runs status 2xx": (r) => r.status >= 200 && r.status < 300,
  });
  errorRate.add(!createOk);

  // GET /v1/runs — list runs for this tenant
  const listRes = http.get(`${BASE_URL}/v1/runs`, { headers });
  const listOk = check(listRes, {
    "GET /v1/runs status 2xx": (r) => r.status >= 200 && r.status < 300,
  });
  errorRate.add(!listOk);

  // GET /v1/agents — list agents for this tenant
  const agentsRes = http.get(`${BASE_URL}/v1/agents`, { headers });
  const agentsOk = check(agentsRes, {
    "GET /v1/agents status 2xx": (r) => r.status >= 200 && r.status < 300,
  });
  errorRate.add(!agentsOk);

  // ~10 RPS per tenant: each iteration takes ~100ms + sleep
  sleep(0.1);
}
