# PoC specification

This directory holds the detailed PoC contracts produced during [02 - PoC specification](https://github.com/ginsys/bronzeward/milestone/2), and the accepted operator-experience specification and client contracts for [04 - Operator experience](https://github.com/ginsys/bronzeward/milestone/4), which are not PoC contracts. This index itself contains no implementation contracts and does not establish specification approval or implementation readiness.

| Contract artifact | Work item |
| --- | --- |
| [`docs/spec/compilation.md`](compilation.md) | [Secret ingress and compilation](https://github.com/ginsys/bronzeward/issues/17) |
| [`docs/spec/persistence-api.md`](persistence-api.md) | [Persistence and API contracts](https://github.com/ginsys/bronzeward/issues/18) |
| [`api/openapi.yaml`](../../api/openapi.yaml), the OpenAPI 3.1 description of the API that persistence and API §9 defines, checked against the route table by `internal/api/openapi_test.go` and against the server's answers and accepted requests by `internal/api/openapi_response_test.go` | [Describe API v1 in OpenAPI and test the description against the route table](https://github.com/ginsys/bronzeward/issues/275) |
| [`docs/spec/execution-recovery.md`](execution-recovery.md) | [Execution and recovery](https://github.com/ginsys/bronzeward/issues/19) |
| [`docs/spec/acceptance-plan.md`](acceptance-plan.md) | [Review and finalize the PoC specification](https://github.com/ginsys/bronzeward/issues/20) |
| [`docs/spec/dependency-monitor.md`](dependency-monitor.md) | [Implement dependency retention checks](https://github.com/ginsys/bronzeward/issues/24), the monitor section the specification review required before implementation |
| [`docs/spec/operator-experience.md`](operator-experience.md) and its [wireframes](operator-experience-wireframes.md) | [Design the operator journeys for bronzewardctl and the web UI](https://github.com/ginsys/bronzeward/issues/252), the post-PoC journeys for [04 - Operator experience](https://github.com/ginsys/bronzeward/milestone/4); accepted for implementation on 25 October 2026, with the client contracts of [#253](https://github.com/ginsys/bronzeward/issues/253) |
| [`docs/spec/client-contracts.md`](client-contracts.md) | [Specify client authentication, API behavior and compatibility](https://github.com/ginsys/bronzeward/issues/253), accepted on 25 October 2026 for [04 - Operator experience: CLI and web UI](https://github.com/ginsys/bronzeward/milestone/4); a client contract added after the milestone-02 specification, on the existing API |

Add links here when those files land. Keep detailed contracts in these artifacts, with links to the [current design](../design/Talos_Configuration_and_Machine_Management_Design.md), experimental evidence and relevant profile/policy decisions. Accepted design changes also update the design; the specification does not silently override it. If a contract needs subdivision, update this index and its issue's deliverables together.

For the PoC contracts, the [specification review](https://github.com/ginsys/bronzeward/issues/20) owns acceptance and refinement requirements. The operator-experience specification and the client contracts were reviewed under their own work items, [#252](https://github.com/ginsys/bronzeward/issues/252) and [#253](https://github.com/ginsys/bronzeward/issues/253), and do not change the PoC contracts. Issue state and landed review evidence establish completion; this index is not a status checklist.
