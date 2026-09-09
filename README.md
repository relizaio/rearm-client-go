# rearm-client-go

Go client for the [ReARM](https://rearmhq.com) GraphQL API, shared by the
[ReARM CLI](https://github.com/relizaio/rearm-cli) and the
[Terraform provider](https://github.com/relizaio/terraform-provider-rearm).

- Typed operations are generated from the ReARM schema with
  [genqlient](https://github.com/Khan/genqlient) (`generated.go`); a schema change is a
  compile error in every consumer rather than a runtime surprise.
- Authentication is API-key only (`Authorization: Basic base64(id:secret)`), the same
  credentials the CLI uses. Browser sessions are out of scope.
- Talks to the programmatic endpoint `/api/programmatic/graphql` (stateless, no CSRF). On
  an older server without it the client falls back to `/graphql` and performs the CSRF
  session handshake that endpoint requires; `WithLegacyEndpoint()` forces that mode.
- `catalog/` works with declarative spec files (`kind: Catalog`, `kind: Branches`): load,
  apply (with dry run), export, render YAML, print change sets.

```go
c, err := rearm.New("https://app.rearmhq.com", os.Getenv("REARM_APIKEYID"), os.Getenv("REARM_APIKEY"))
spec, err := catalog.Load("branches/platform.yaml")
res, err := catalog.Apply(ctx, c, spec, true /* dry run */, &catalog.Source{Repo: "acme/config", Path: "branches/platform.yaml", Commit: sha})
fmt.Print(catalog.Format(res))
```

## Spec files

```yaml
kind: Catalog
version: 1
authoritative: false        # true = this file owns every component of the org (prune per org setting)
components:
  - name: payments-api
    type: COMPONENT
    versionSchema: semver
    featureBranchVersioning: Branch.Micro
    vcsUri: https://github.com/acme/payments-api
```

```yaml
kind: Branches
version: 1
component: acme-platform     # a product: its branches are feature sets
branches:
  - name: stable
    type: FEATURE
    autoIntegrate: ENABLED
    dependencies:
      - { component: payments-api, branch: main }
    dependencyPatterns:
      - { pattern: "^acme-.*", targetBranchName: main, fallbackToBase: ENABLED }
```

A field left out of a spec is not managed by that file and keeps its value in ReARM.
Rows absent from an authoritative spec follow the organization's declarative prune
setting (`LEAVE` reports them, `ARCHIVE` archives them); the setting lives on the
organization on purpose, a file cannot widen its own blast radius.

## Regenerating

```
go run github.com/Khan/genqlient
```

`schema/rearm.graphqls` is the public ReARM schema; `schema/declarative.graphqls` carries
the declarative operations. Operations live in `operations/*.graphql`.
