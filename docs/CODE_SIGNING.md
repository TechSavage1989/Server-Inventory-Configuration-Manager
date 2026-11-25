# Code signing (Azure Artifact Signing)

The Release workflow signs `Server-Inventory-Configuration-Manager.exe` with
Azure Artifact Signing, using the same account, certificate profile and
GitHub OIDC approach as Network Toolkit (its `docs/CODE_SIGNING_PLAN.md`).
Signing is required: a release fails unless the three repository secrets below
exist and every signature is Valid and timestamped (owner requirement,
2026-11-25). Before `go build` it also signs the PowerShell scripts the
application writes to disk and runs (restore, file share migration, guest
preparation, receiver media, Hyper-V read and copy, disk fix-ups), so the
signed scripts are what the EXE embeds. The inventory collector and its
wrapper are not signed because their exact bytes are the protected baseline.
See `EDR-EXCLUSIONS.md` for what endpoint protection sees.

Configuration in `.github/workflows/release.yml` (not secret):

- Endpoint: `https://eus.codesigning.azure.net` (East US)
- Account: `networktoolkit-signing`
- Certificate profile: `jpaull-codesign`
- Signing client: Microsoft's `sign` .NET tool, pinned in `SIGN_TOOL_VERSION`
  (latest listed version on nuget.org when added: `0.9.1-beta.26552.4`).
- Timestamp: `http://timestamp.acs.microsoft.com`, SHA-256 file and timestamp
  digests.

## How it works

The `publish` job runs in the GitHub `release` environment with
`id-token: write`, so its OIDC token subject is
`repo:TechSavage1989/Server-Inventory-Configuration-Manager:environment:release`.
Right after `go build`, it signs in with `azure/login` (no client secret or
password), signs the executable in place, and checks with
`Get-AuthenticodeSignature` that the signature is Valid and timestamped. Every
later step (Test-Binary, Go tests, update install test, release manifest and
SHA-256, publication) uses the signed bytes, so the manifest hash is the hash of
the signed file. It signs out of Azure at the end.

- Never sign or re-sign a published asset; signing changes the bytes and the
  published SHA-256.
- If signing fails, or any of the three secrets is missing, the release fails;
  it never falls back to unsigned.
- The updater validation (`binary_validation.go`) does not reject the
  Authenticode certificate table, so published older versions accept a signed
  update. The manifest SHA-256 stays the byte check; the signature is the
  publisher identity Windows, SmartScreen and endpoint protection see.

## Setup the owner does (once)

The Artifact Signing account, identity validation, certificate profile, app
registration and its signer role come from the Network Toolkit setup. This
repository needs only:

1. **A federated credential for this repository** on the same Entra app
   registration (App registrations → the Network Toolkit signing app →
   Certificates & secrets → Federated credentials → Add credential → scenario
   *GitHub Actions deploying Azure resources*): Organization
   `TechSavage1989`, Repository `Server-Inventory-Configuration-Manager`,
   Entity type **Environment**, environment name `release`. Use an Environment
   credential, not a branch or tag one: Entra matches the subject literally.
2. **Repository secrets** (Settings → Secrets and variables → Actions), the
   same values as Network Toolkit's: `AZURE_CLIENT_ID` (Application ID),
   `AZURE_TENANT_ID` (Directory ID) and `AZURE_SUBSCRIPTION_ID`. None of them
   is a password.

The `release` environment is created automatically by the first run that uses
it; do not add required reviewers unless every release should wait for a manual
approval. The Artifact Signing identity validation must be Completed and the
app must hold the *Artifact Signing Certificate Profile Signer* role on the
account, or the sign-in or signing step fails.
