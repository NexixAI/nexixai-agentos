NexixAI AI Implementor Standard v1

Purpose

This document defines mandatory control standards for AI-assisted code changes in NexixAI repositories.
Its goal is to prevent architectural drift, accidental refactors, stale-base PRs, and undocumented behavior changes.

Change Classification

Every AI-driven change MUST declare:
	•	Change Type: Hygiene / Rename / Documentation / Functional / Spec
	•	Behavioral Change: YES / NO
	•	API Contract Change: YES / NO
	•	Schema Change: YES / NO
	•	Data Migration Required: YES / NO
	•	Risk Level: Low / Medium / High
	•	Rollback Strategy: Revert commit(s)

Branch Base Requirement (No Stale-Base PRs)

All AI-driven work MUST:
	1.	sync main to match origin/main exactly, then
	2.	create a new branch from that updated main, then
	3.	open a PR back into main.

Codex Launcher Checklist (Mandatory Before Any Task)

Before performing any implementation work, the AI implementor MUST:
	1.	Confirm repository root

	•	Ensure commands are run from the repository root directory

	2.	Confirm clean working tree

	•	git status must show no uncommitted changes

	3.	Sync local main with remote

git checkout main
git fetch origin
git reset --hard origin/main
git clean -fd

	4.	Verify sync

	•	git status reports clean
	•	git rev-parse HEAD matches origin/main

	5.	Create a new task branch

	•	Branch MUST be created from the freshly synced main
	•	Branch name must reflect task intent (e.g. hygiene/..., spec/..., fix/...)

Failure to complete this checklist invalidates the implementation and requires restart.

Hygiene-Only Definition

A hygiene-only change:
	•	alters naming, documentation, or structure
	•	does NOT alter behavior, logic, or contracts
	•	does NOT introduce new features or optimizations

Forbidden Actions (Hard Rules)

AI implementors must NOT:
	•	refactor logic
	•	reorder execution paths
	•	introduce new abstractions
	•	rename non-targeted symbols
	•	optimize performance
	•	change defaults
	•	add new configuration options
	•	modify error handling behavior
	•	change tenancy or isolation semantics
	•	change API contract semantics
	•	change schema semantics

Spec Authority
	•	docs/governance/spec-authority.md and schema appendices are authoritative
	•	specs are spec-first and additive-only
	•	implementation must conform to specs, never redefine them

Architectural Invariants

All changes MUST preserve:
	•	control plane does not execute heavy compute
	•	workers do not make orchestration or policy decisions
	•	tenancy remains logical unless explicitly stated otherwise

Verification Requirements

Every AI-assisted PR MUST include:
	•	git diff --stat
	•	proof of zero forbidden tokens (grep/rg as applicable)
	•	build/test output
	•	explicit statement of what did NOT change
	•	confirmation that the branch is based on latest origin/main

Reviewer Checklist

Human reviewers MUST verify:
	•	scope adherence (hygiene-only means no behavior change)
	•	invariant preservation
	•	no architectural drift introduced
	•	no stale-base PR (branch base requirement satisfied)
