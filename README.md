# dql

dql, the Declarative Query Language, is a compiler with a plugin architecture.
Source plugins describe a database as a universal catalog; the core checks it,
derives shapes, conversions and validation rules, and bundles them; emitter
plugins turn the bundle into host-language code.

## Contributing

Enable the repository's hooks once per clone:

```sh
git config core.hooksPath scripts/hooks
```

Branches start from `dev` as `feature/<topic>`. Owners re-sign with
`scripts/resign.sh` and merge with `scripts/merge.sh`.
