# 0003. Plugins receive their dql.yaml entry

- Date: 2026-10-23

## Decision

- Every `Invoke` carries `settings`, a `google.protobuf.Struct` holding the plugin's own `dql.yaml` entry: every key as written, `plugin` and `path` included. Mapping keys, timestamps and binary values arrive as strings, as written.
- The plugin validates its settings. The core checks only the keys it reads itself: `plugin`, `path`, and an emit entry's `dialect`.
- An entry for a shipped plugin holds only the keys that plugin reads; any other key is a `dql.yaml` error. An entry for any other plugin may hold keys of its own.
- Shipped plugins read their settings from the entry directly when built in. Their executables also take them as flags; a third-party executable gets no flags.
- Relative paths in `dql.yaml`, a plugin's `path` included, are relative to its directory. Every plugin executable runs with that directory as its working directory, so a relative path in its settings opens as written. The flags of shipped executables carry absolute paths.

## Consequences

- A third-party source learns what to read, and a third-party emitter where to write, from the invocation alone.
- A new setting for a third-party plugin needs no change to dql.
