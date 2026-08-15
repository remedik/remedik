# ADR-0003: Deterministic core; AI is read-only

- Status: accepted
- Date: 2026-08-15

## Context

Adjacent tools increasingly put an LLM in the remediation decision path. The
consistent blocker operators report for adopting auto-remediation is trust:
predictable behavior, explainable actions, bounded blast radius.

## Decision

The execution loop is deterministic: declared strategies, guards, and
(optionally) human approval decide everything. LLM-backed features
(diagnosis, summaries) are:

- **read-only by construction** — a separate component with a read-only
  ServiceAccount; it has no code path that mutates cluster state;
- **optional** — disabled by default and not even installed without an
  explicit flag;
- **provider-agnostic** — any OpenAI-compatible endpoint (including
  self-hosted Ollama/vLLM/LiteLLM) or the Anthropic API, with automatic
  secret redaction before any context leaves the cluster.

## Consequences

- remedik can be audited and reasoned about like any other controller.
- AI features degrade gracefully to "absent" and can never block or trigger
  remediation.
- Positioning vs. AI-first tools is a deliberate feature; moving this
  boundary requires a new ADR, not a PR.
