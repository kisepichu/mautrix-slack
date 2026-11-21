# mautrix-slack
![Languages](https://img.shields.io/github/languages/top/mautrix/slack.svg)
[![License](https://img.shields.io/github/license/mautrix/slack.svg)](LICENSE)
[![GitLab CI](https://mau.dev/mautrix/slack/badges/main/pipeline.svg)](https://mau.dev/mautrix/slack/container_registry)

A Matrix-Slack puppeting bridge based on [slack-go](https://github.com/slack-go/slack).

## Documentation
All setup and usage instructions are located on [docs.mau.fi]. Some quick links:

[docs.mau.fi]: https://docs.mau.fi/bridges/go/slack/index.html

* [Bridge setup](https://docs.mau.fi/bridges/go/setup.html?bridge=slack)
  (or [with Docker](https://docs.mau.fi/bridges/general/docker-setup.html?bridge=slack))
* Basic usage: [Authentication](https://docs.mau.fi/bridges/go/slack/authentication.html)

### Features & Roadmap
[ROADMAP.md](https://github.com/mautrix/slack/blob/main/ROADMAP.md)
contains a general overview of what is supported by the bridge.

### User token login (this fork)
To puppet your own account without browser cookies, use an internal Slack app with a user token:

1. Create an app at <https://api.slack.com/apps> from [app-manifest-user.yaml](app-manifest-user.yaml)
   and install it to your workspace.
2. Copy the **User OAuth Token** (`xoxp-`) from *OAuth & Permissions*.
3. Under *Basic Information → App-Level Tokens*, create a token with the `connections:write` scope (`xapp-`).
4. Send `login app` to the bridge bot and paste both tokens.

Messages you send through the bridge are posted as you. The API marks them with the app's `app_id`, and workspace
admins can see and remove the app. Read receipts and typing notifications are not sent with this login.

## Discussion
Matrix room: [#slack:maunium.net](https://matrix.to/#/#slack:maunium.net)
