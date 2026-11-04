# Phase 10 — implementation rules

This repository follows these rules for every subsequent change:

1. Inspect the current tree and environment before changing code.
2. Treat the IPA as read-only and do not bypass signing, DRM, certificate
   validation, anti-cheat, or access controls.
3. Clearly label verified findings, engineering assumptions, unknowns, and
   newly proposed independent behavior.
4. Do not invent ROS-specific wire messages or claim original-client
   compatibility without an authorized, reproducible integration test.
5. Add tests with each behavior change and run format, build, unit, race,
   integration, and supported simulator checks in proportion to risk.
6. Never place credentials in source, logs, fixtures, images, or reports.
7. Keep domain modules independent from transports and infrastructure adapters.
8. Prefer idempotent migrations, bounded queues/state, explicit error paths,
   structured redacted logs, and reversible deployment changes.
9. Report failures and blockers exactly as observed; do not convert a skipped
   or unavailable test into a pass.
10. Preserve the original IPA and downloaded content; independent server and
    client work must remain legally separable.
