## 1. Election

- [x] 1.1 Leader election on the manager, with a lease in the operator's namespace
- [x] 1.2 The reconciler runs only on the leader

## 2. The gateway

- [x] 2.1 A readiness predicate the handler consults before accepting
- [x] 2.2 A non-leader answers 503, naming the pod and the reason
- [x] 2.3 Tests: a non-leader records nothing; a leader behaves as before

## 3. The chart

- [x] 3.1 The lease rule, and the RBAC test covering it
- [x] 3.2 `replicaCount`, defaulting to one
- [x] 3.3 NOTES and the chart README

## 4. Readiness

- [x] 4.1 Readiness stays independent of leadership, so `--wait` completes
- [x] 4.2 The reasoning for rejecting the stricter probe is written down
- [x] 4.3 e2e waits for the gateway to accept, not merely for a ready pod

## 5. Proof

- [x] 5.1 e2e: two replicas, and only one of them acts
- [x] 5.2 Architecture and CHANGELOG
