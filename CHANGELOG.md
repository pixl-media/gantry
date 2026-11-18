# Changelog

Every entry is a merged pull request.

## [0.2.0](https://github.com/pixl-media/gantry/releases/tag/v0.2.0) (2026-11-18)

### Features

- API: /api/v1/info with host details ([#30](https://github.com/pixl-media/gantry/pull/30))
- API: volume sizes per stack ([#31](https://github.com/pixl-media/gantry/pull/31))
- Web UI: notice when an operation ends, messages at the top right ([#33](https://github.com/pixl-media/gantry/pull/33))
- Single sign-on with OIDC for the web UI ([#35](https://github.com/pixl-media/gantry/pull/35))

### Maintenance

- Web UI: split the bundle into React, MUI and app chunks ([#32](https://github.com/pixl-media/gantry/pull/32))
- Package settings template with every setting ([#36](https://github.com/pixl-media/gantry/pull/36))
- Drop the minimatch override ([#37](https://github.com/pixl-media/gantry/pull/37))

### Dependencies

- Bump i18next from 27.1.1 to 27.1.2 in /web in the web-dependencies group ([#34](https://github.com/pixl-media/gantry/pull/34))

## [0.1.1](https://github.com/pixl-media/gantry/releases/tag/v0.1.1) (2026-11-18)

### Features

- Web UI: logo in the header, English by default ([#26](https://github.com/pixl-media/gantry/pull/26))
- Web UI: port configurable with GANTRY_WEB_PORT ([#27](https://github.com/pixl-media/gantry/pull/27))
- Stack file access: report missing access, lock editing, targeted chown ([#28](https://github.com/pixl-media/gantry/pull/28))

### Fixes

- Keep the release notes in GoReleaser releases ([#22](https://github.com/pixl-media/gantry/pull/22))

### Maintenance

- Docs: link decisions, update the README for v0.1 ([#23](https://github.com/pixl-media/gantry/pull/23))
- Logo: a gantry crane holding a container ([#24](https://github.com/pixl-media/gantry/pull/24))
- Docs: resource usage tools and no self-update ([#25](https://github.com/pixl-media/gantry/pull/25))

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
