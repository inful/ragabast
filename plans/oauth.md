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
4. **Scopes**: `openid`, `profile`, `email` (the preset defaults).
   Mark **api** if you also want ragabast to call back into the GitLab
   API on the user's behalf — not currently used.
5. Click **Save application**. Copy the **Application ID** (this is
   the client_id) and **Secret** (client_secret) into ragabast.

For self-hosted GitLab, set `base_url` to the GitLab root
(e.g. `https://gitlab.example.com`).

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
