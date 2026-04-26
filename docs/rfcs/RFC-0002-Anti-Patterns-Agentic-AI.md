# RFC-0002: Common Anti-Patterns in Agentic AI Architecture

**Author:** Emily Yoshida  
**Organization:** Self (NexixAI)  
**Status:** Draft
**Updated:** 2026-01-02

-----

## Abstract

This RFC catalogs common architectural anti-patterns observed in agentic AI system design, with particular focus on the confusion between **specification, governance, and enforcement**. Each anti-pattern is documented with symptoms, root causes, real costs, and corrective approaches aligned with RFC-0001 AI Job Control Language (AI JCL) principles.

The core thesis: **most AI reliability failures are specification failures**, not model failures.

-----

## Motivation

The rapid adoption of LLM-based agents has produced a predictable failure mode: teams attempt to manage undefined behavior with increasingly sophisticated infrastructure. Circuit breakers, retry logic, audit trails, and governance committees are deployed to compensate for the absence of formal specifications.

This mirrors a classic distributed-systems error: attempting to enforce constraints that were never defined.

This RFC provides a diagnostic lens to identify these failures and a corrective path grounded in specification-first architecture.

-----

## Scope and Non‑Goals

This RFC **does apply** to:

- Decision‑making agents
- Customer‑facing automation
- Policy enforcement systems
- Agent orchestration platforms
- Any system where “correct” can be defined

This RFC **does NOT apply** to:

- Creative generation (art, fiction, ideation)
- Exploratory analysis without correctness criteria
- Brainstorming or inspiration tools
- Human‑in‑the‑loop drafting assistants

If correctness cannot be specified, governance is intentionally impossible.

-----

## Target Audience

- Platform engineers building agent orchestration systems
- Architects evaluating agentic AI strategies
- Engineering leaders assessing vendor proposals
- Teams experiencing “unreliable AI” in production

-----

## Architectural Principle: Dependency Order

Governance, enforcement, and implementation are **not peers**.

They form a strict dependency chain:

```
Governance (Specifications, Contracts, STOP conditions)
        ↓
Enforcement (Rate limits, breakers, validation)
        ↓
Implementation (Prompts, models, execution)
```

- Enforcement **depends on** governance.
- Implementation **depends on** governance.
- Enforcement does **not** create governance.
- Observability does **not** substitute for specification.

Violating this order produces the anti‑patterns described below.

-----

## Anti‑Pattern Catalog

### AP‑001: Enforcement as Governance

**Symptom:**  
Operational controls are treated as the primary reliability strategy.

**Root Cause:**  
Layer‑3 enforcement is used to compensate for missing Layer‑2 specifications.

**Real Cost:**

- Sophisticated monitoring of undefined behavior
- Expensive infrastructure managing symptoms
- False confidence without correctness guarantees

**Correction:**  
Define schemas, acceptance criteria, STOP conditions, and change classification **before** enforcement.

-----

### AP‑002: Prompts as Specifications

**Symptom:**  
Prompt engineering is treated as architectural design.

**Root Cause:**  
Implementation detail is mistaken for a contract.

**Key Distinction:**

- Prompts communicate intent.
- Specifications define correctness.

**Correction:**  
Write machine‑readable schemas and testable acceptance criteria. Use prompts only to convey them.

-----

### AP‑003: Audit Trails as Problem Solving

**Symptom:**  
Heavy investment in logs and explanations to understand incorrect decisions.

**Root Cause:**  
Attempting forensics on behavior that was never defined.

**Key Insight:**  
If you must ask *why* an agent decided something, you failed to define *what* was acceptable.

**Correction:**  
Prevent violations with pre‑output STOP conditions. Use audit trails for compliance and edge‑case debugging only.

-----

### AP‑004: The “Probabilistic Nature” Excuse

**Symptom:**  
Inconsistent outputs are blamed on inherent LLM randomness.

**Root Cause:**  
Misunderstanding where non‑determinism actually exists.

**Clarification:**  
LLMs may be probabilistic at the sampling layer, but **execution is deterministic** given:

- Temperature = 0
- Stable inputs
- Stable model version

This does not eliminate system‑level stochasticity (model updates, context changes), but it does eliminate execution variance.

**Correction:**  
If outputs vary unacceptably, the specification is incomplete.

-----

### AP‑005: Governance Theater

**Symptom:**  
Policies, ethics boards, and training replace executable constraints.

**Root Cause:**  
Process substituted for engineering.

**Indicators:**

- ✔ Governance PDFs
- ✔ Review committees
- ✘ No schemas
- ✘ No STOP conditions
- ✘ No automated enforcement

**Correction:**  
Translate governance policies into executable system constraints.

-----

### AP‑006: Retrofitted Reliability

**Symptom:**  
Specifications and controls are added only after production failures.

**Root Cause:**  
Treating agent systems like features instead of infrastructure.

**Why It Fails:**

- Incorrect behavior becomes “expected”
- Users adapt to undefined outputs
- Each fix addresses symptoms, not causes

**Correction:**  
Specification‑first development. Define before deploy.

-----

### AP‑007: Hallucination Obsession

**Symptom:**  
“Hallucinations” are treated as the core reliability problem.

**Root Cause:**  
Undefined information boundaries.

**Key Insight:**  
If the spec does not constrain sources, plausible fabrication is within scope.

**Correction:**  
Define allowed data sources, citation requirements, and explicit “unknown” handling.

-----

## Organizational Root Cause

These anti‑patterns persist not just due to technical misunderstanding, but because they optimize for:

- Speed over correctness
- Optics over enforceability
- Post‑hoc blame management over prevention

Prompt‑only systems avoid review.  
Governance theater satisfies compliance optics.  
Audit trails shift accountability after failure.

Specification‑first systems force hard decisions early. They also expose when requirements are actually unknown, which makes stakeholders uncomfortable.

-----

## Enforcement‑First: The Narrow Exception

Enforcement‑first design is valid **only** for:

- Resource protection
- Cost containment
- Abuse prevention
- Incident blast‑radius control

It is **never** sufficient for defining correct behavior.

-----

## Diagnostic Framework

### Red Flags

- “Better” without defining “good”
- Enforcement managing undefined behavior
- Governance without code
- Detection without prevention
- “It’s just how AI works”

### Green Flags

- Formal schemas
- Testable acceptance criteria
- Pre‑output STOP conditions
- Change classification
- Specification before implementation

-----

## Validation Tests

**Specification Test:**  
Can a new engineer define “correct” without running the agent?

**Prevention Test:**  
Are failures prevented more often than detected?

**Change Test:**  
Are specs updated before prompts?

**Stop Test:**  
Can the system refuse to emit invalid output?

-----

## Summary

**Core Insight:**  
Most AI reliability problems are specification problems masquerading as model problems.

**Correct Order:**

1. Governance (specifications, STOP conditions)
1. Enforcement (controls that enforce boundaries)
1. Implementation (prompts and execution)

**Principle:**  
Prevent bad behavior before you need to explain it.

-----

## References

- RFC‑0001: AI Job Control Language (AI JCL)
- AgentOS Architecture Documentation
- Distributed Systems Reliability Patterns

-----

## Changelog

- 2026‑01‑02: Initial draft with strengthened scope, clarifications, and organizational context