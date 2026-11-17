# Security

gantry controls Docker on its host, which is root-equivalent, so security
reports are welcome and taken seriously.

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub: on the repository's
**Security** tab, choose **Report a vulnerability**. Don't open a public
issue for them.

Please include what is affected (agent, web UI, packages), the version, and
how to reproduce it. You'll get an answer as soon as possible.

## Supported versions

Fixes go into the latest release.

## Design

How gantry is hardened, including when it is exposed to the internet, is
described in `docs/decisions.md` (decisions 20, 21, 26 and 27).
