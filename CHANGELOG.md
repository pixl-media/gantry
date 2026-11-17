# Changelog

Every entry is a merged pull request.

## [0.1.0](https://github.com/pixl-media/gantry/releases/tag/v0.1.0) (2026-11-17)

### Features

- List stacks and show stack details ([#2](https://github.com/pixl-media/gantry/pull/2))
- Start, stop, restart, update and remove stacks ([#3](https://github.com/pixl-media/gantry/pull/3))
- Add the web UI with session login ([#4](https://github.com/pixl-media/gantry/pull/4))
- Add container logs: merged, live and in the web UI ([#6](https://github.com/pixl-media/gantry/pull/6))
- Edit stacks: Compose file, .env, create and delete ([#7](https://github.com/pixl-media/gantry/pull/7))
- Anthracite theme, color scheme switcher and AGENTS.md split ([#11](https://github.com/pixl-media/gantry/pull/11))
- Actions on single services ([#14](https://github.com/pixl-media/gantry/pull/14))
- Live stack changes instead of polling ([#15](https://github.com/pixl-media/gantry/pull/15))
- Separate tokens for the API and the web login; record the scope ([#16](https://github.com/pixl-media/gantry/pull/16))
- TLS in the agent ([#17](https://github.com/pixl-media/gantry/pull/17))
- Stack list: filter, search and a card view ([#18](https://github.com/pixl-media/gantry/pull/18))
- Packages and releases: .deb/.rpm with systemd, git-cliff notes ([#20](https://github.com/pixl-media/gantry/pull/20))

### Fixes

- Security hardening: first and second review ([#8](https://github.com/pixl-media/gantry/pull/8))

### Maintenance

- Document architecture decisions ([#1](https://github.com/pixl-media/gantry/pull/1))
- Build the web UI with Node 26 LTS ([#9](https://github.com/pixl-media/gantry/pull/9))
- Fix open Dependabot alerts (kin-openapi, minimatch) ([#12](https://github.com/pixl-media/gantry/pull/12))
- License gantry under the AGPLv3 ([#13](https://github.com/pixl-media/gantry/pull/13))
- Pin actions and base images; attest pushed images ([#19](https://github.com/pixl-media/gantry/pull/19))

### Dependencies

- Bump jsdom from 31.0.0 to 31.0.1 in /web in the web-dependencies group ([#10](https://github.com/pixl-media/gantry/pull/10))
