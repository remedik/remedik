## 1. The parameter

- [x] 1.1 `format` on `webhook.call`, defaulting to today's body
- [x] 1.2 An unknown format is refused by `Plan`, not at `Execute`
- [x] 1.3 The Alertmanager body: labels that route, annotations that explain

## 2. Proof

- [x] 2.1 Tests for both bodies, and for the refusal
- [x] 2.2 e2e: an escalation reaches Alertmanager and is accepted
- [x] 2.3 Verified against a real Alertmanager, not a stub

## 3. Documentation

- [x] 3.1 A cookbook recipe, saying what the raised alert is named
- [x] 3.2 The action's own reference, architecture and CHANGELOG
