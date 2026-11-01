# OAuth 2.1

Sagalore is an OAuth 2.1 authorization server for any client that needs to call this API as a player. The companion does not use these endpoints. It still signs in with `/v1/auth/bnet/start` and a `gss_` session.

An access token from this server is a `gst_` personal token. `Authorization: Bearer` on the existing routes accepts it, including `/v1/sync` and `/v1/ws`. It expires after one hour. A refresh token is `gsr_`. Sending a refresh token as an API key returns `401`.

The Battle.net application does not change. The redirect URI stays `{PUBLIC_BASE_URL}/v1/auth/bnet/callback`. Clients register with Sagalore. They never see `BNET_CLIENT_SECRET`.

`PUBLIC_BASE_URL` is the issuer. It is an origin with no path and no trailing slash. The same value is the protected resource.

## Discovery

`GET /.well-known/oauth-authorization-server` (RFC 8414) lists the endpoints below, `S256` as the only PKCE method, and `authorization_response_iss_parameter_supported: true`.

`GET /.well-known/oauth-protected-resource` (RFC 9728) names this origin as the resource and this issuer as the authorization server. A client that was pointed at `{origin}/v1` can read `/.well-known/oauth-protected-resource/v1`. The authorization server is still the origin.

A player route that returns `401` sets `WWW-Authenticate` to a bearer challenge. A presented token that is invalid or expired adds `error="invalid_token"`. `resource_metadata` is the protected-resource URL when `PUBLIC_BASE_URL` is set, and is omitted when it is empty.

Both metadata documents and `POST /oauth/register`, `/oauth/token`, and `/oauth/revoke` allow cross-origin browser calls (`Access-Control-Allow-Origin: *`). The consent page does not.

## Register

`POST /oauth/register` with JSON. This is open dynamic client registration (RFC 7591). Anyone who can reach the API can register a client. Redirect URIs are checked so a registered client cannot send the browser to an arbitrary site.

```bash
curl -s -X POST "$API/oauth/register" \
  -H "Content-Type: application/json" \
  -d '{"client_name":"Board","redirect_uris":["http://127.0.0.1:43821/callback"]}'
```

The response includes `client_id` (`gsc_…`). There is no secret when `token_endpoint_auth_method` is omitted or `none`. Ask for `client_secret_basic` or `client_secret_post` to get a `gck_…` secret. The secret is returned once and is stored only as a hash. `client_secret_expires_at` is `0`.

Registration is limited to 20 attempts per IP per hour (`X-Forwarded-For` when it is an IP, otherwise the connection address). A client that never starts authorization is deleted after 7 days.

Allowed redirect URIs:

- `https://` with a host
- `http://127.0.0.1`, `http://localhost`, or `http://[::1]`, any port
- a private-use scheme such as `com.example.app://callback`

`http://` to any other host is rejected. So are fragments, userinfo, `javascript:`, `data:`, and `file:`. The redirect URI on later requests must match a registered value exactly, including the port.

`grant_types` may be `authorization_code` and `refresh_token`. Both are the default. `response_types` is `code`. Unknown JSON fields are ignored.

The only scope is `player`: call this API as the consented account. Omitting `scope` means `player`.

## Authorize

Send the browser to `GET /oauth/authorize`.

| Query | |
| --- | --- |
| `response_type` | `code` |
| `client_id` | from registration |
| `redirect_uri` | exact registered URI |
| `state` | required, sent back unchanged |
| `code_challenge` | S256, 43 characters |
| `code_challenge_method` | `S256`. `plain` is rejected |
| `scope` | `player`, or omit it |
| `resource` | optional. The origin, or the origin plus `/v1` |
| `response_mode` | omit, or `query` |

PKCE is required for public and confidential clients.

If the browser has no consent cookie, the server redirects to Battle.net. After Battle.net returns, the server sets an HttpOnly cookie on `/oauth` for 15 minutes and shows the consent page. The cookie value starts with `goc_`. It is not a `gss_` session and the API rejects it. The companion poll cannot read that login. The consent form includes a CSRF token bound to that cookie and the authorization request. The page sends `Referrer-Policy: same-origin`. `POST /oauth/authorize` requires the CSRF token and an `Origin` equal to `PUBLIC_BASE_URL`. A missing `Origin`, or the value `null`, is accepted only when `Sec-Fetch-Site` is `same-origin`.

The consent page shows the client name, the BattleTag, and the redirect URI. Its `Content-Security-Policy` `form-action` is `'self'` plus the redirect URI's origin (scheme only, for a private-use URI). Chromium applies `form-action` to every hop of a form POST, so a 302 from Allow straight to another origin is dropped and the button looks like it did nothing. The decision therefore answers `303` to `/oauth/continue`, a same-origin page that says it is sending the browser back and then navigates to the client. Allow arrives there with `code`, `state`, and `iss` (the issuer). Deny arrives with `error=access_denied`.

A second Allow with the same consent cookie and CSRF token, within two minutes, redirects to the same code and state. The code can still be exchanged only once. A different CSRF token is rejected. An unknown or already-used authorization request is an HTML page that tells the player to go back to the app and start again. An unknown `client_id` or a redirect URI that was not registered is a JSON error and does not redirect.

The authorization code starts with `gso_`, expires in 10 minutes, and works once. The code is stored as a hash.

## Token

`POST /oauth/token` as `application/x-www-form-urlencoded`.

Authorization code:

```bash
curl -s -X POST "$API/oauth/token" \
  -d "grant_type=authorization_code" \
  -d "code=$CODE" \
  -d "redirect_uri=$REDIRECT" \
  -d "client_id=$CLIENT_ID" \
  -d "code_verifier=$VERIFIER"
```

A confidential client also sends its secret. `client_secret_post` puts `client_secret` in the body. `client_secret_basic` uses HTTP Basic, and the token endpoint answers `401` with `WWW-Authenticate: Basic` when that secret is missing or wrong. A public client must not send a secret.

The JSON response:

```json
{
  "access_token": "gst_…",
  "token_type": "Bearer",
  "expires_in": 3600,
  "refresh_token": "gsr_…",
  "scope": "player"
}
```

`resource` is included when the authorize request sent one. Use the access token on the API:

```bash
curl -s "$API/v1/groups" -H "Authorization: Bearer $ACCESS"
```

A wrong verifier, a mismatched redirect URI, an expired code, or a second use of the code is `invalid_grant`. The second use also revokes the access token and refresh token that the first use issued.

Refresh:

```bash
curl -s -X POST "$API/oauth/token" \
  -d "grant_type=refresh_token" \
  -d "refresh_token=$REFRESH" \
  -d "client_id=$CLIENT_ID"
```

Each refresh returns a new access token and a new refresh token. The previous refresh token stops working. The previous access token keeps working until it expires, about an hour, unless the family is revoked.

If a client presents a refresh token that was already rotated or revoked, the server revokes every refresh token and access token in that family and returns `invalid_grant`. The client must send the user through the authorize endpoint again.

Refresh tokens expire after 30 days. A refresh family cannot be extended past 90 days from the consent that started it. A later refresh expires at that cap when the cap is sooner.

## Revoke

`POST /oauth/revoke` (RFC 7009). The client authenticates the same way as at the token endpoint. The response is `200` with an empty body when the token is revoked and when the token is unknown.

```bash
curl -s -X POST "$API/oauth/revoke" \
  -d "token=$REFRESH" \
  -d "token_type_hint=refresh_token" \
  -d "client_id=$CLIENT_ID"
```

Revoking a refresh token revokes the family, including access tokens from that consent. Revoking an access token revokes that access token only. The client can still refresh.

`token_type_hint` may be `access_token` or `refresh_token`. A wrong hint still finds the token.

`DELETE /v1/me/tokens/{id}`, called with a session or a hand-minted `gst_`, revokes that token. For an OAuth access token it also revokes the refresh family. An OAuth access token cannot call `POST /v1/me/tokens` or `DELETE /v1/me/tokens/{id}`. A `gst_` token minted with `POST /v1/me/tokens` does not expire and has no refresh token. OAuth rows use the label `oauth:` plus the client name.

A player route that rejects a presented bearer token sets `WWW-Authenticate` to `error="invalid_token"`. A missing token does not set that error. Both include `resource_metadata` when `PUBLIC_BASE_URL` is set.

## What a client stores

Store `client_id`, and the secret if you registered a confidential client. Store the latest refresh token. Replace it on every refresh. Discard the previous one. The access token is a bearer token for one hour.

Do not put the Battle.net client secret, a `gss_` session, or a refresh token in a mobile app that you cannot keep private. Public clients use PKCE and no secret.
