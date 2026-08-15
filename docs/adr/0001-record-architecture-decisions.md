# ADR-0001: Record architecture decisions

- Status: accepted
- Date: 2026-08-15

## Context

remedik is built in public, spec-first, with contributors (human and AI)
joining at different times. Structural decisions need a durable, reviewable
home; chat threads and issue comments do not survive.

## Decision

We record architecturally significant decisions as ADRs in `docs/adr/`,
numbered sequentially, in this lightweight format (context / decision /
consequences). Behavior contracts live in OpenSpec specs; ADRs capture the
*why* behind structural choices.

## Consequences

- Decisions are reviewable in PRs and stay discoverable.
- Superseded ADRs are marked as superseded, never deleted.
