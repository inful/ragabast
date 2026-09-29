# OAuth / OIDC provider setup

Per-provider recipes for registering ragabast as an OAuth client. The
common pieces every provider needs:

- A **client ID** and **client secret** issued by the IdP.
- A **callback URL** of the form
  `https://<your-ragabast-host>/auth/<name>/callback`. The `<name>`
  is the slug you put under `auth.providers[].name` in the ragabast
  config; pick something URL-safe (lowercase ASCII, digits, hyphens).
- **Scopes** that let ragabast identify the user. The presets
  (`github`, `gitlab`, `forgejo`) choose sensible defaults; you can
  override via `auth.providers[].scopes` when needed.

> **Important**: register the callback URL **exactly** as ragabast will
> send it. A trailing slash mismatch is the most common cause of
> "the IdP redirected back but ragabast rejected the state cookie".
> HTTPS in production; HTTP is acceptable for local-dev only.

## github.com

1. Go to **Settings → Developer settings → OAuth Apps → New OAuth App**.
2. **Application name**: anything (e.g. "ragabast").
3. **Homepage URL**: `https://<your-ragabast-host>/`.
4. **Authorization callback URL**: `https://<your-ragabast-host>/auth/<name>/callback`.
5. **Enable Device Flow**: no.
6. Click **Register application**. On the next screen, **Generate a new
   client secret**.
7. Copy the **Client ID** and the **Client secret** into
   ragabast's config:

   ```yaml
   auth:
     providers:
       - name: gh
         type: github
         client_id: <paste>
         client_secret: <paste>
   ```

Default scopes: `read:user user:email` — enough to populate the
session's username and verified email.

## GitLab.com

1. Go to <https://gitlab.com/-/user_settings/applications> (or your
   self-hosted equivalent at `/-/user_settings/applications`).
2. **Name**: anything (e.g. "ragabast").
3. **Redirect URI**: `https://<your-ragabast-host>/auth/<name>/callback`.
4. **Scopes**: `read_user`, `profile`, `email` (the preset defaults).
   `read_user` is the GitLab API scope that unlocks `/api/v4/user`
   — without it, ragabast's userinfo fetch returns HTTP 403
   regardless of token validity. Mark **api** if you also want
   ragabast to call back into the GitLab API on the user's behalf
   — not currently used. `openid` is intentionally omitted from
   the defaults because only GitLab 16.0+ with OIDC applications
   enabled honors it; including it on older GitLabs produces a
   confusing login failure.
5. Click **Save application**. Copy the **Application ID** (this is
   the client_id) and **Secret** (client_secret) into ragabast.

For self-hosted GitLab, set `base_url` to the GitLab root
(e.g. `https://gitlab.example.com`).

Operators on OIDC-enabled GitLab (16.0+) who prefer the OIDC
ID-token path should switch to `type: oidc` with a
`discovery_url` pointing at `<gitlab>/.well-known/openid-configuration`.
type=oidc uses the OIDC UserInfo claim flow instead of
`/api/v4/user`, so the OIDC spec scopes (`openid`, `profile`,
`email`) are sufficient.

```yaml
auth:
  providers:
    - name: gl
      type: gitlab
      client_id: <Application ID>
      client_secret: <Secret>
      # base_url: https://gitlab.example.com   # omit for gitlab.com
```

## Forgejo (Codeberg, self-hosted, …)

Forgejo speaks the GitHub-compatible OAuth shape: its authorization
endpoint is `<base>/login/oauth/authorize` and its token endpoint is
`<base>/login/oauth/access_token`. The userinfo fetch goes against
`<base>/api/v1/user`.

1. In your Forgejo instance, go to **Site Administration → Users →
   … → Applications → Create New OAuth2 Application** (or, when self-
   hosting for your own account, **Settings → Applications → Manage
   OAuth2 Applications**).
2. **Application Name**: anything (e.g. "ragabast").
3. **Redirect URI**: `https://<your-ragabast-host>/auth/<name>/callback`.
4. **Confidential Client**: yes.
5. Save and copy the **Client ID** and **Client Secret**.

```yaml
auth:
  providers:
    - name: codeberg
      type: forgejo
      base_url: https://codeberg.org
      client_id: <paste>
      client_secret: <paste>
```

## Generic OIDC (Keycloak, Authentik, Auth0, Authelia, …)

Use `type: oidc` and point ragabast at the IdP's issuer URL. ragabast
fetches `<issuer>/.well-known/openid-configuration` at startup, picks
up the authorization + token + userinfo endpoints, and verifies the
returned `id_token` against the IdP's JWKS.

### Keycloak

1. In your realm, go to **Clients → Create client**.
2. **Client type**: OpenID Connect.
3. **Client ID**: pick anything (e.g. "ragabast"). ragabast config
   needs to match.
4. **Name**: anything.
5. On the next screen, enable **Client authentication** (so Keycloak
   issues a secret).
6. **Root URL**: `https://<your-ragabast-host>/`.
7. **Valid redirect URIs**:
   `https://<your-ragabast-host>/auth/<name>/callback`.
8. Save, then on the **Credentials** tab copy the **Client secret**.

```yaml
auth:
  providers:
    - name: kc
      type: oidc
      discovery_url: https://keycloak.example.com/realms/main
      client_id: ragabast
      client_secret: <paste>
```

### Authentik

1. **Applications → Providers → Create → OAuth2/OpenID Provider**.
2. Pick a **Provider name** (e.g. "ragabast"); set the
   **Authorization flow** to one that grants the scopes you want.
3. **Redirect URIs**:
   `https://<your-ragabast-host>/auth/<name>/callback`.
4. **Signing Key**: pick one configured in your Authentik instance.
5. Create the provider, then **Applications → Create** with
   **Provider** = the one you just made; copy the **Client ID** and
   **Client Secret** from the provider detail page.

```yaml
auth:
  providers:
    - name: ak
      type: oidc
      discovery_url: https://authentik.example.com/application/o/ragabast/
      client_id: <paste>
      client_secret: <paste>
```

### Authelia

Authelia publishes a well-known at `https://auth.example.com/.well-known/openid-configuration`.

```yaml
auth:
  providers:
    - name: authelia
      type: oidc
      discovery_url: https://auth.example.com
      client_id: ragabast
      client_secret: <paste>
```

### Active Directory (ADFS, Server 2019+)

ADFS Server 2019 added native OIDC support. The IdP "issuer" URL is
typically `https://adfs.example.com/adfs`.

1. **ADFS Management → Application Groups → Add Application Group**.
2. Name it (e.g. "ragabast"), pick **Server application**.
3. **Client identifier**: anything (e.g. "ragabast").
4. **Redirect URI**:
   `https://<your-ragabast-host>/auth/<name>/callback`.
5. On the next screen, enable **Generate shared secret**; copy it.
6. Note the **Identifier** (issuer URL) — it's shown on the
   application group's properties.

```yaml
auth:
  providers:
    - name: adfs
      type: oidc
      discovery_url: https://adfs.example.com/adfs
      client_id: ragabast
      client_secret: <paste>
```

> **Older ADFS** (Server 2016 and earlier) does not speak OIDC
> natively. Either upgrade to 2019+ or front ADFS with an OIDC bridge
> (e.g. [`oidc-bridge`](https://github.com/nicolastakashi/oidc-bridge),
> Authelia, Authentik, Keycloak) and point ragabast at the bridge's
> discovery URL.

## Multiple providers

List as many `auth.providers` entries as you want. When two or more
are configured, `GET /auth/login` renders a chooser page. Single-
provider setups redirect straight to that provider's login flow.

```yaml
auth:
  providers:
    - name: gh
      type: github
      client_id: ...
      client_secret: ...
    - name: gl
      type: gitlab
      base_url: https://gitlab.example.com
      client_id: ...
      client_secret: ...
    - name: ak
      type: oidc
      discovery_url: https://authentik.example.com/application/o/main/
      client_id: ...
      client_secret: ...
```

## Restricting who can log in

Per-provider `allowed_users` is a strict whitelist. Match is on the
IdP's username OR email — the field the IdP returns. Empty list (the
default) accepts any user the IdP authenticated.

```yaml
auth:
  providers:
    - name: company-gitlab
      type: gitlab
      client_id: ...
      client_secret: ...
      allowed_users:
        - alice@example.com
        - bob@example.com
        - carol
```

A user not on the list sees **403 "user is not in the allowed_users
list for this provider"**. The session is never created, so the next
browser request bounces back to `/auth/login`.

## Local development without an IdP

For working on ragabast itself, no OAuth config is needed — leave
`auth.providers` empty and the server runs in the historical
single-user / bearer-token mode. `/auth/login` returns 503 so an
operator who accidentally enables it sees the missing-config signal.
The navbar on every page also renders nothing in this mode — there
is no sign-in / sign-out UX to show.

## Session storage

Sessions are **in-memory only**. A server restart (including a
crash) wipes every live session — the next browser request after
restart sees an unauthenticated navbar even if the cookie value is
still in the browser's jar.

- `auth.session_ttl` (env `AUTH_SESSION_TTL`) — max age of a session
  cookie. Sliding renewal: every authenticated request through the
  middleware extends the session's expiry by this TTL. An idle user
  is logged out after one TTL; an active user stays signed in
  indefinitely. Default 12h. Set to `0` to disable expiry (sessions
  still vanish on restart, just not on idle).
- `auth.cookie_name` (env `AUTH_COOKIE_NAME`) — session-cookie name.
  Default `ragabast_session`. Override when running multiple
  ragabast instances behind the same host (e.g. dev + staging on
  different ports) so cookies don't collide.

A persistent session store (Bolt, Badger, SQLite) is not currently
implemented. The store interface in `internal/web/session.go` is
small enough to swap when an operator asks for it; until then,
single-replica deployments only. A load-balanced multi-replica
deployment without sticky sessions will see users logged out on
every request — the cookie value alone is not enough to identify
the right replica's session store.

## Browser UX (navbar)

Every rendered page (`/`, `/ingest`, `/documents`, `/chat` and the
ingest-success page) carries a navbar at the top, sourced from a
shared `header` template so the look stays consistent:

| State | Navbar shows |
|---|---|
| OAuth not configured | *(nothing — historical single-user behavior)* |
| OAuth configured, not signed in | "Sign in" link → `/auth/login?next=<current URL>` |
| OAuth configured, signed in | `username (provider)` + a real form-POST "Sign out" button |

The "Sign in" link's `?next=` threads the current URL through the
chooser page so the OAuth state callback can land the user back
where they were going after auth completes. The link is built from
the `Accept: text/html` signal so a browser sees it; a curl script
that didn't send the header still gets 401 + `WWW-Authenticate:
Bearer` so it can retry correctly.

The "Sign out" button is a regular form that POSTs to
`/auth/logout` with the CSRF token in a hidden field. The csrf
middleware requires the form value to match the `ragabast_csrf`
cookie; a cross-origin attacker cannot make the browser send the
cookie on a POST, so the sign-out flow is safe by the same
mechanism as every other state-changing endpoint.

## Debugging a broken flow

The most common failure modes:

- **"redirect_uri mismatch" from the IdP** — the callback URL
  registered with the IdP doesn't exactly match the one ragabast
  sends. Look at the server's log line on the failed `/auth/<provider>/login`
  request; it shows the URL it constructed.
- **"state cookie missing or mismatched"** — the browser sent the
  callback but the `ragabast_oauth_state` cookie was either missing
  or didn't match the `state` query parameter. Usually a clock skew
  between ragabast and the IdP (the state TTL is 10 minutes), or the
  user took longer than 10 minutes to authorize.
- **"login succeeds but every subsequent request bounces to the
  sign-in page"** — the writer (OAuth callback) and reader
  (authMiddleware) disagree on the cookie name. The cookie name
  defaults to `ragabast_session`; if `auth.cookie_name` is set on
  the server but the browser already has a stale cookie under the
  default name, the middleware reads the default-named cookie
  (empty) while the callback writes the configured name. Either
  unset `AUTH_COOKIE_NAME` to use the default, or have users clear
  cookies for the host before retrying. The first version of this
  code shipped with this bug — the fix (in `NewServer`) applies
  the default at startup so writer and reader can never disagree.
- **"navbar shows 'Sign in' even when I just signed in"** — same
  root cause as above; the auth middleware couldn't read the
  cookie, so the session wasn't attached to the request context.
  The navbar only shows "Sign out" when `UserFromContext` returns
  a non-empty Subject, which only happens when the session
  lookup succeeded. Fix the cookie-name mismatch and the navbar
  updates on the next request.
- **"server won't start with `type: oidc` provider"** — the OIDC
  discovery round-trip at startup failed (bad `discovery_url`,
  unreachable IdP, expired TLS cert). The startup log shows the
  exact URL and the underlying error. Fix the URL or the network
  path; the server refuses to start with a broken provider
  because silent fallback would mean login is broken with no
  signal in the logs.
- **"token exchange failed: …"** — the IdP rejected the code.
  Usually a wrong client_secret, a revoked grant, or a code that's
  already been exchanged once.
- **"userinfo fetch failed"** — the IdP returned a token but the
  profile endpoint 5xx'd or 401'd. Check the operator's API token
  scopes and that the user granted them.
- **"user is not in the allowed_users list"** — the IdP authenticated
  the user successfully but `allowed_users` rejected them. The
  match is on the IdP's `username` OR `email`; both are surfaced in
  `/auth/me` for inspection.
