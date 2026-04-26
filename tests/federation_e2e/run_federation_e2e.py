#!/usr/bin/env python3
import json
import os
import time
import requests

NODEA_FED = os.getenv("NODEA_FED", "http://nodea-federation:8083")
NODEB_FED = os.getenv("NODEB_FED") or os.getenv("NODEB_STACKA") or "http://nodeb-federation:8086"

TENANT = os.getenv("TENANT", "tnt_demo")
PRINCIPAL = os.getenv("PRINCIPAL", "prn_ci")
WAIT_SECONDS = int(os.getenv("E2E_WAIT_SECONDS", "180"))

def wait_ok(url, timeout=WAIT_SECONDS, label=None):
    label = label or url
    print(f"Waiting for {label} ...", flush=True)
    t0 = time.time()
    attempts = 0
    last_error = None
    while time.time() - t0 < timeout:
        attempts += 1
        try:
            r = requests.get(url, headers={"X-Tenant-Id": TENANT, "X-Principal-Id": PRINCIPAL}, timeout=5)
            if r.status_code == 200:
                elapsed = time.time() - t0
                print(f"OK: {label} ready after {elapsed:.1f}s", flush=True)
                return True
            last_error = f"status={r.status_code} body={r.text[:200]}"
        except Exception as exc:
            last_error = str(exc)
        time.sleep(0.5)
    raise SystemExit(f"timeout waiting for {label} after {timeout}s (attempts={attempts}); last_error={last_error}")

def read_first_sse_event(url, timeout=10):
    with requests.get(url, headers={"X-Tenant-Id": TENANT, "X-Principal-Id": PRINCIPAL, "Accept": "text/event-stream"}, stream=True, timeout=timeout) as r:
        r.raise_for_status()
        data = None
        for raw in r.iter_lines(decode_unicode=True):
            if raw is None:
                continue
            line = raw.strip()
            if line.startswith("data:"):
                data = line[len("data:"):].strip()
                break
        if not data:
            raise SystemExit("no SSE data line received")
        return json.loads(data)

def main():
    # Wait for services
    wait_ok(f"{NODEA_FED}/v1/federation/health", label="node A federation health")
    wait_ok(f"{NODEB_FED}/v1/federation/health", label="node B federation health")

    # Forward a run to node B via node A federation
    fwd = {
      "forward": {
        "target_selector": {
          "stack_id": "stk_local_node_b",
          "region": "local",
          "required_capabilities": ["runs.forward"],
          "preferred_model_tier": "fast"
        },
        "auth": {
          "tenant_id": TENANT,
          "principal_id": PRINCIPAL,
          "scopes": ["runs:write", "events:read"],
          "subject_type": "api_key",
          "api_key_id": "key_ci"
        },
        "run_request": {
          "agent_id": "agt_demo",
          "input": {"type": "text", "text": "hello from federation e2e"},
          "context": {"locale": "en-US", "timezone": "UTC", "metadata": {"source": "ci"}},
          "tooling": {"tools": []},
          "run_options": {"max_steps": 3, "timeout_ms": 10000, "model_tier": "fast"},
          "idempotency_key": f"idem_ci_{int(time.time())}"
        },
        "traceparent": "00-00000000000000000000000000000000-0000000000000000-01"
      }
    }

    r = requests.post(f"{NODEA_FED}/v1/federation/runs:forward",
                      headers={"Content-Type": "application/json", "X-Tenant-Id": TENANT, "X-Principal-Id": PRINCIPAL},
                      data=json.dumps(fwd), timeout=10)
    r.raise_for_status()
    resp = r.json()
    forwarded = resp.get("forwarded", {})
    run_id = forwarded.get("remote_run_id")
    if not run_id:
        raise SystemExit(f"missing remote_run_id in response: {resp}")

    # Stream proxied events from node A federation for that run_id
    env = read_first_sse_event(f"{NODEA_FED}/v1/federation/runs/{run_id}/events", timeout=15)

    event = env.get("event", {})
    assert event.get("tenant_id") == TENANT, f"tenant mismatch: {event}"
    assert event.get("run_id") == run_id, f"run_id mismatch: {event}"
    assert event.get("event_id"), "missing event_id"
    assert event.get("type"), "missing event type"

    # Test ingest dedupe: send same envelope twice; expect rejected==1
    ingest = {
      "peer_id": "peer_ci",
      "auth": {"tenant_id": TENANT, "principal_id": PRINCIPAL},
      "events": [env, env]
    }
    r2 = requests.post(f"{NODEA_FED}/v1/federation/events:ingest",
                       headers={"Content-Type": "application/json", "X-Tenant-Id": TENANT, "X-Principal-Id": PRINCIPAL},
                       data=json.dumps(ingest), timeout=10)
    r2.raise_for_status()
    ir = r2.json()
    if ir.get("accepted") != 1 or ir.get("rejected") != 1:
        raise SystemExit(f"unexpected ingest counts: {ir}")

    print("OK: federation forward + SSE proxy + ingest dedupe")

if __name__ == "__main__":
    main()
