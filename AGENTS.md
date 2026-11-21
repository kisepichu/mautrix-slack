# Agent rules for this fork

This is a fork of mautrix-slack that adds user-token app login and a catch-up for app logins.
The design is in `docs/superpowers/specs/2026-11-21-user-token-login-design.md`.

- Never write information that identifies a real Slack workspace (workspace name, team or enterprise ID, channel
  names or IDs, people's names or IDs) in code, docs, commit messages, issues or pull requests. Use made-up values in
  tests and examples.
- When testing against Slack, write (send, edit, delete, react) only in a personal test workspace, or only in your own
  DM in any other workspace. Reading is not restricted.
- Keep changes self-contained so they can be proposed upstream. Do not reformat or refactor upstream code that the
  change does not need.
- Format imports with `goimports -local go.mau.fi/mautrix-slack`. Do not use `Msgf` with zerolog.
