# Phase 1 — ROS Legacy IPA research and feasibility

Date: 2026-10-29

## Verified findings

- The supplied artifact is `RoS-Legacy-iOS.ipa`, 846,004,086 bytes, SHA-256 `d1f638ad49387a0bca3b5e391799e07faa12f3dca7db32f9fd22f07db14e8c55`.
- Bundle identifier: `net.roslegacy.ios`; executable: `client`; version/build: `1.610637.617289`.
- The bundle declares `CFBundleSupportedPlatforms = iPhoneOS`, minimum OS 16.0, and iPhone/iPad families.
- The executable is a thin arm64 Mach-O with `LC_VERSION_MIN_IPHONEOS`; no simulator slice or simulator platform metadata was observed.
- The executable is signed by Apple iPhone OS Application Signing. This observation is not authorization to modify, re-sign, decrypt, or redistribute the client.
- Xcode 27 and iOS 26.0/26.1 Simulator runtimes are installed on the host. An iPhone 17 Pro simulator booted successfully.
- The real `simctl` probe accepted the IPA into the simulator app registry, but launching `net.roslegacy.ios` was denied. This is recorded as a device-only incompatibility, not a passing client test.

## Engine indicators

Readable symbols include `NeoX Engine`, `neox::` namespaces, Cocos2d-derived UI symbols, and NeoX game/resource subsystems. The strongest defensible conclusion is **a proprietary NeoX-based client with Cocos2d-derived components**. This is an evidence-based engine indicator, not a claim about source ownership or an official SDK integration path.

## Network indicators

The binary links against CFNetwork, Security, GameKit, GameController, and related Apple frameworks. Readable symbols include QuickNet UDP transport/protocol classes, login/request/connection classes, and server-connection abstractions. These indicate network code exists, but they do not disclose message schemas, authentication requirements, server addresses, encryption, matchmaking semantics, or a compatible server contract.

Plain-text packaged configuration includes third-party SDK configuration and associated-domain declarations. Those values were not copied into this report because they are not a documented private-server interface and may contain account/provider credentials. No authorized ROS Legacy server protocol specification was supplied or verified.

## Go/no-go gate

| Gate question | Result | Evidence / consequence |
|---|---|---|
| Client version identified? | Yes | Bundle metadata is recorded in `ipa-metadata.json`. |
| Engine identified? | Partial/strong indicator | NeoX and Cocos2d-derived symbols; no source-level confirmation. |
| Authorized integration mechanism available? | No evidence | No SDK, server documentation, or developer authorization was supplied. |
| Enough protocol information for a compatibility service? | No | Strings/linkage are insufficient to implement a lawful compatible protocol. |
| Legal restrictions known? | Unknown | Licensing, service terms, and authorization still require owner confirmation. |
| Can testing occur without modifying protected functionality? | Yes for inventory only | Simulator original-client execution is blocked; physical-device testing requires authorization and a supported device path. |

## Phase 1 decision

**Original ROS Legacy client compatibility: NO-GO for implementation and simulator claims.** Do not write a ROS-specific compatibility adapter or claim multiplayer compatibility from this IPA alone.

**Independent platform work: GO, with a clean boundary.** A separately implemented Go backend, original client/test client, and protocol-neutral integration harness may be developed without pretending to reproduce the original client protocol. Phase 2 may proceed as an independent backend foundation, while original-client integration remains gated on documented authorization and protocol information.

## Next smallest meaningful step

Build the independent backend foundation with an explicit, versioned API contract and test-only client. Do not reuse the original client’s embedded secrets/configuration, modify the IPA, or infer undocumented wire messages. The existing Phase 11 runner is ready to consume that backend and a simulator-compatible test client once they exist.
