# Provider Proxy provider proxy extensions

This branch extends Rota for provider-backed superproxies such as Bright Data.

## Logical proxies

A proxy is no longer uniquely identified only by `address + protocol`. The
logical identity also includes the upstream username, configured target country,
session strategy, and fixed session id.

That allows multiple entries to share the same gateway, for example:

| Name | Address | Provider | Country | Session |
|---|---|---|---|---|
| the validation client DE | brd.superproxy.io:44445 | brightdata | DE | per_request |
| the validation client BR | brd.superproxy.io:44445 | brightdata | BR | per_request |
| the validation client CA | brd.superproxy.io:44445 | brightdata | CA | per_request |

The Bright Data base username can be stored without `-country-xx` and
`-session-...`; Rota materializes these routing components for each request.

## Session strategies

- `none`: keep the provider's default behavior.
- `per_request`: generate a new Bright Data session id and a fresh upstream
  connection for every outgoing HTTP proxy request or HTTPS CONNECT. This is
  the intended the validation client mode when validating many URLs quickly.

For HTTPS, one CONNECT tunnel can carry multiple encrypted requests to the same
origin if the client reuses connections. Rota cannot see those inner request
boundaries without TLS interception. the validation client should therefore close/avoid
reusing proxy tunnels when it requires one exit per URL, or use the diagnostics
probe endpoint, which creates a fresh provider session per URL/attempt.
- `fixed`: use `session_id` repeatedly. This is intended for debugging,
  especially direct-Bright-Data vs Rota comparisons using the same provider
  session.

A new session id asks Bright Data to select a new peer. It is still possible for
the provider to choose the same underlying exit again; Rota does not claim a
unique IP unless it has observed it.

## HTTPS and HTTP 403

Normal HTTPS proxying remains a CONNECT tunnel. Rota can see whether the tunnel
was established, but it cannot see the encrypted target response status.

Therefore:

- CONNECT 200 = transport/tunnel established.
- It does **not** mean the target page returned HTTP 200.
- A target HTTP 403 must not mark the proxy itself unhealthy.

For diagnostics, Rota exposes a separate endpoint where Rota itself is the HTTP
client and can therefore observe the target status without TLS MITM.

## Exit observation

`POST /api/v1/diagnostics/exits`

Example body:

```json
{"proxy_ids":[12,13,14]}
```

The result records configured country plus observed exit country/IP/ASN. The
latest observation is also stored on the logical proxy and displayed in the
dashboard.

## URL x country diagnostics

`POST /api/v1/diagnostics/probe`

Example:

```json
{
  "urls": [
    "https://example.de/a",
    "https://example.de/b"
  ],
  "proxy_ids": [12,13,14],
  "attempts": 3,
  "follow_redirects": true
}
```

For `per_request`, every URL/attempt receives a fresh provider session.
The response contains raw results and a `matrix` grouped by URL and configured
country.

Assessments intentionally use `blocked_sample` rather than "country blocked".
For example, three sampled DE exits all returning 403 is evidence about that
sample, not proof that every possible German exit is blocked.

## Project usage

For the pilot, a Rota proxy user is the project identity:

- `validation-client`
- `scraping-client`

Usage records contain project/user, pool, logical proxy, provider session, and
payload bytes up/down. HTTPS byte accounting is measured at the CONNECT tunnel.

Export CSV:

```text
GET /api/v1/usage/export?project=validation-client&from=2026-10-01&to=2026-10-31&format=csv
```

The dashboard Users page also exposes "Export project usage CSV".

Rota byte counters are internal traffic measurements; they are not guaranteed to
match provider billing exactly.

## Future pilot follow-ups

The following remain intentionally separate from this first implementation:

- provider billing API reconciliation;
- creating/synchronizing provider zones from the provider control-plane API;
- full role-based dashboard access (`viewer/operator/admin`).

The provider routing abstraction introduced here is the basis for automated
provider configuration later.
