# Namespaces

A namespace is an independent migration history. Use them when several apps share one database, each with its own `migrations/` directory. Single-app use needs no configuration: everything lives in the namespace `default`.

## Setting the namespace

=== "CLI"

    ```bash
    mmmigrate apply -namespace billing
    mmmigrate status -namespace billing
    ```

    `-namespace` is accepted by `apply`, `status`, `commit` and `watch`. When not passed, the `MMMIGRATE_NAMESPACE` environment variable is used, then `default`.

=== "Library"

    ```go
    err := mmmigrate.RunMigrations(ctx, db, postgres.Dialect{}, fsys, false,
        mmmigrate.WithNamespace("billing"))
    ```

    An empty string means the default namespace. See [Library API](library.md#namespaces).

=== "WASM"

    ```js
    await mm.apply(db, { fs, namespace: 'billing' })
    ```

Pass the same namespace to every command for a given app. Setting `MMMIGRATE_NAMESPACE` in that app's environment is the easiest way.

## What it separates

Both [tracking tables](faq.md#where-is-the-tracking-table) have a `namespace` column: `applied` is keyed by `(namespace, version)` and `current` has one row per namespace. Each namespace therefore has its own version numbers, its own `current.sql` checksum, and its own [lock](concurrency.md): applies in different namespaces don't block each other (except on SQLite, where locking is file-level).

## Caveats

- **`_mmmigrate` is reserved.** mmmigrate records the migrations of its own tracking tables under it, and rejects it as an app's namespace.
- **Only bookkeeping is separated.** The apps' own objects still share the database, so migrations must not create conflicting tables. On PostgreSQL, have each app create its objects in its own schema.
- **Use a separate shadow database per app.** <a id="shadow-databases"></a>Shadow verification resets the whole shadow database (all tables in `public` on PostgreSQL, all tables on MySQL and SQLite) before replaying. Never point two apps' `-shadow-url` at the same shadow database at the same time, and never at a real database. See [Shadow database](shadow-database.md).
- **`namespace` has no default in SQL.** Manual inserts into the tracking table must include it, e.g. `INSERT INTO mmmigrate.applied (namespace, version, name) VALUES ('billing', 1, 'initial_schema');`.
