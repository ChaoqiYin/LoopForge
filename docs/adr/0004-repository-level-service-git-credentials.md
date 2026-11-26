# 0004. Repository-level encrypted service credentials for Git

- Status: Accepted
- Date: 2026-11-26
- Deciders: LoopForge platform
- Related: #1, #6, #9

## Context

Externally triggered Work Items run without a signed-in user. User-level Git
credentials are bound to a user and host, so they cannot authenticate a run
that no person started. Repository configuration also needs to work with any
standard Git host, not only GitHub, and PR/MR APIs must stay optional.

Existing user credentials remain useful when a person starts a run and should
not be force-migrated.

## Decision

Add repository-level service credentials.

- `repository_git_credentials` stores `id`, `repository_id`, `name`, `kind`
  (`ssh` or `https`), `host`, optional `username`, and the sealed `secret`.
- The secret is encrypted with AES-256-GCM through the platform credential
  keyring and bound to the repository and credential ID with additional
  authenticated data. It is never returned by the API.
- `Repository.ServiceCredentialID` references the credential used for
  unattended execution.
- Resolution order: repository service credential first, then user-level
  credentials as an optional override. Existing user credentials keep working.
- Both SSH and HTTPS transports are supported. The provider field is for
  display and optional API routing; it is not required for clone, fetch,
  checkout, commit or push.
- PR/MR remains optional. GitHub and GitHub Enterprise support read/write;
  GitLab support is read-only in this phase; no Gitea/Gitee PR adapter is
  added.

## Consequences

- External triggers can clone and push without a logged-in user.
- Credential rotation is per repository and does not disturb user overrides.
- Deployments must keep `AGENTFLOW_PROJECT_CREDENTIAL_KEY` with their database
  backups; losing it makes existing secrets unreadable.
- The plaintext token field on repositories remains deprecated and is not a
  credential source.
- Secrets are scoped to a repository rather than a host, matching the fact
  that externally triggered work belongs to a repository.

## Alternatives considered

- **Keep user-only credentials:** impossible for runs without a user and
  requires shared human accounts.
- **Store a plaintext token on the repository:** simple but unacceptable for
  a service credential.
- **Require a Git provider API:** blocks standard Git hosts and duplicates
  what the Git protocol already supports.
