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
- `catalog/` works with declarative spec files (`kind: CATALOG`, `kind: BRANCHES`): load,
  apply (with dry run), export, render YAML, print change sets.

```go
c, err := rearm.New("https://app.rearmhq.com", os.Getenv("REARM_APIKEYID"), os.Getenv("REARM_APIKEY"))
spec, err := catalog.Load("branches/platform.yaml")
res, err := catalog.Apply(ctx, c, spec, true /* dry run */, &catalog.Source{Repo: "acme/config", Path: "branches/platform.yaml", Commit: sha})
fmt.Print(catalog.Format(res))
```

## Spec files

```yaml
kind: CATALOG
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
kind: BRANCHES
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

`schema/programmatic.graphqls` is the programmatic API contract as served by a ReARM
server at `GET /api/programmatic/schema` (only the API-key operations and the types they
reach); refresh it from a server, then regenerate. Operations live in `operations/*.graphql`.

## The full programmatic surface

`operations/cli.graphql` carries every operation the ReARM CLI sends (releases, artifacts, agent
sessions, instances, feature sets, pull requests, SBOM probing, versioning, signing keys); the
generator turns them into typed functions and `*_Operation` constants. Two ways to call:

```go
// typed: normalised result structs
resp, err := rearm.GetLatestReleaseProgrammatic(ctx, client, rearm.GetLatestReleaseInput{...})

// raw: the server's `data` member exactly as produced (what the CLI prints)
data, err := rearm.Raw(ctx, client, "GetLatestReleaseProgrammatic", rearm.GetLatestReleaseProgrammatic_Operation, vars)
```

Errors from the server come back as `rearm.GraphQLErrors`; a missing entity is `catalog.IsNotFound`.

### Files

Mutations whose variables carry files (artifacts on releases, deliverables, source code entries or
agent sessions) go through the GraphQL multipart form:

```go
data, err := rearm.UploadMultipart(ctx, client, "AddArtifactProgrammatic", rearm.AddArtifactProgrammatic_Operation,
    vars, []rearm.FilePart{{Filename: "bom.json", Content: f, VariablePath: "variables.artifactInput.file"}})
```

`rearm.DownloadArtifact` streams an artifact's bytes (processed or raw, by version).

## Browser-login sessions

`rearm login` (the CLI) approves a device-authorization request in the browser and keeps a
refresh token, never a key secret. Any Go program can act as that session:

```go
c, err := rearm.NewWithSession(url, refreshToken, cached, func(t rearm.SessionTokens) { save(t) })
```

The client trades the refresh token for one-hour access tokens on demand and hands every new
token set to the callback; the server slides the session 30 days per refresh, capped at 90 days
after approval. A refused refresh surfaces as `*rearm.SessionError` (unwrap with `errors.As`):
log in again. `c.Revoke(ctx)` ends the session.

The interactive flow itself is two calls, `rearm.StartDeviceLogin` and `rearm.PollDeviceLogin`;
printing the code, opening the browser and the polling loop belong to the caller.

