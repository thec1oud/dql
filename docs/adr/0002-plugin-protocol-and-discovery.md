# 0002. Plugin protocol framing and discovery

- Date: 2026-10-23

## Decision

- A plugin speaks over its stdin and stdout. Each message is a 4-byte big-endian length followed by the message's binary protobuf encoding, at most 1 GiB. The core sends `CoreMessage`s; the plugin answers with `PluginMessage`s.
- A session runs: the core sends `CoreHello` with the bundle's IR version and required features; the plugin answers with its `PluginHandshake`; the core replies with `HandshakeResult`. A refused plugin receives the reason and no further messages. An accepted plugin receives `Invoke` messages, each answered by an `InvokeResult` or a `PluginError`, until `Shutdown` or the end of its stdin.
- The core refuses a plugin of the wrong kind, one whose range excludes the bundle's IR version (below `min`, or a major above `maxMajor`), and one lacking a required feature.
- Plugins are named in `dql.yaml`. A plugin entry's `path:` names its executable explicitly. Without one, a shipped plugin runs in-process, built into `dql`; any other plugin `<name>` is the executable `dql-plugin-<name>` on `PATH`.

## Consequences

- Shipped plugins run in-process and as executables through the same `plugin.Plugin` interface, and tests cover both paths.
- A third-party plugin is installed by putting `dql-plugin-<name>` on `PATH`; no registry or config file lists plugins.
- `dql.yaml` entries that name a plugin accept an optional `path`.
