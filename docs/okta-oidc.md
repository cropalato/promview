# Configure Okta OIDC

This guide configures Okta as the OpenID Connect provider for Promview's browser sign-in flow. Promview uses Authorization Code with PKCE, validates Okta ID tokens, maps Okta groups to Promview roles, and creates its own opaque browser session. It does not persist Okta access, ID, or refresh tokens.

## Prerequisites

- An Okta administrator account with permission to create application integrations.
- A stable HTTPS URL for Promview, such as `https://promview.example.com`.
- Okta groups for the Promview roles you intend to grant, such as:
  - `promview-viewers`
  - `promview-operators`
  - `promview-administrators`
- Users assigned to the Okta application and at least one mapped group.

The examples use an Okta custom authorization server with the `default` identifier. If your organization uses the Okta organization authorization server instead, see [Choose The Issuer](#choose-the-issuer).

## Create The Okta Application

1. In the Okta Admin Console, open **Applications**, then **Applications**.
2. Select **Create App Integration**.
3. Choose **OIDC - OpenID Connect** as the sign-in method.
4. Choose **Web Application** as the application type.
5. Enter an application name such as `Promview`.
6. Enable the **Authorization Code** grant type. Promview adds PKCE to this flow.
7. Add this exact sign-in redirect URI:

   ```text
   https://promview.example.com/api/v1/auth/oidc/callback
   ```

8. Configure assignments for the users or groups allowed to sign in.
9. Save the integration.
10. Record the generated **Client ID** and **Client secret**.

Promview currently performs local logout only, so an Okta sign-out redirect URI is not required. Signing out revokes the Promview session but does not end the user's Okta single sign-on session.

## Choose The Issuer

The issuer must exactly match the `iss` claim in the ID token and the issuer returned by Okta discovery. Do not use the Okta Admin Console URL.

For a custom authorization server named `default`:

```text
https://your-org.okta.com/oauth2/default
```

For the Okta organization authorization server:

```text
https://your-org.okta.com
```

Custom authorization servers require the relevant Okta API Access Management capability. They are generally preferable when you need centrally managed custom claims and scopes. The organization authorization server can be sufficient for Okta-hosted browser sign-in, but its tokens are intended for the Okta organization and its claim configuration differs.

Confirm discovery is available before configuring Promview:

```sh
curl --fail 'https://your-org.okta.com/oauth2/default/.well-known/openid-configuration'
```

The returned `issuer` value must equal `PROMVIEW_OIDC_ISSUER_URL` exactly.

## Add The Groups Claim

Promview maps a provider group claim to server-owned roles. It never accepts an Okta role claim as a Promview role directly.

For a custom authorization server:

1. In the Okta Admin Console, open **Security**, **API**, then **Authorization Servers**.
2. Select the authorization server, such as `default`.
3. Open **Scopes** and create a `groups` scope if it does not already exist.
4. Open **Claims** and add a claim named `groups`.
5. Include the claim in the ID token.
6. Use a groups filter that includes only the groups Promview needs. For example, use a prefix filter for `promview-`.
7. Include the claim for the `groups` scope, or always include it if that matches your organization's policy.

For an organization authorization server, configure the groups claim on the application's sign-on settings. Okta's available controls vary by account type and Admin Console version; the resulting ID token must contain a string array similar to:

```json
{
  "groups": [
    "promview-viewers"
  ]
}
```

Promview's default requested scopes are `openid,profile,email,groups`, and its default group claim name is `groups`. If your custom authorization server does not define a `groups` scope, configure the claim to be included in every ID token and set `PROMVIEW_OIDC_SCOPES=openid,profile,email`. If your claim uses a different name or scope, configure `PROMVIEW_OIDC_GROUPS_CLAIM` and `PROMVIEW_OIDC_SCOPES` accordingly.

Keep group filters narrow. Okta can reject or omit a groups claim when too many groups match.

## Configure Promview

Set the following environment variables before starting Docker Compose:

```sh
export PROMVIEW_AUTH_MODE=oidc
export PROMVIEW_OIDC_ISSUER_URL='https://your-org.okta.com/oauth2/default'
export PROMVIEW_OIDC_CLIENT_ID='your-okta-client-id'
export PROMVIEW_OIDC_CLIENT_SECRET='your-okta-client-secret'
export PROMVIEW_OIDC_REDIRECT_URL='https://promview.example.com/api/v1/auth/oidc/callback'

docker compose up --detach --build
```

Create server-owned bindings for the Okta groups before users sign in:

```sh
docker compose run --rm app access set \
  --name promview-viewers \
  --role viewer \
  --oidc-issuer 'https://your-org.okta.com/oauth2/default' \
  --oidc-group 'promview-viewers'

docker compose run --rm app access set \
  --name promview-administrators \
  --role administrator \
  --oidc-issuer 'https://your-org.okta.com/oauth2/default' \
  --oidc-group 'promview-administrators'
```

Users without a matching binding are denied. Multiple bindings are unioned. Viewer and operator bindings can restrict access with label selectors:

```sh
docker compose run --rm app access set \
  --name platform-operators \
  --role operator \
  --oidc-issuer 'https://your-org.okta.com/oauth2/default' \
  --oidc-group 'promview-platform' \
  --selector 'team=platform'
```

Optional claim and scope settings are:

```sh
export PROMVIEW_OIDC_SCOPES='openid,profile,email,groups'
export PROMVIEW_OIDC_USERNAME_CLAIM='preferred_username'
export PROMVIEW_OIDC_EMAIL_CLAIM='email'
export PROMVIEW_OIDC_DISPLAY_NAME_CLAIM='name'
export PROMVIEW_OIDC_GROUPS_CLAIM='groups'
```

Store the client secret in your deployment's secret manager or protected environment configuration. Do not commit it to the repository or place it in browser-visible runtime configuration.

## Reverse Proxy Requirements

- Serve the public Promview URL over HTTPS.
- Forward `/api/v1/auth/oidc/login` and `/api/v1/auth/oidc/callback` to Promview without rewriting their paths.
- Forward all other `/api` routes and the application normally.
- Do not expose Promview directly over untrusted plain HTTP.
- Keep `PROMVIEW_OIDC_REDIRECT_URL` set to the public HTTPS callback URL. Promview does not derive it from `Host` or forwarding headers.

Promview marks its session and OIDC state cookies `Secure`, `HttpOnly`, and `SameSite=Lax` by default. TLS may terminate at the reverse proxy; the browser still needs to access Promview through the configured HTTPS URL.

## Local Loopback Testing

Promview permits insecure cookies only for loopback callback hosts. If Okta permits a loopback redirect URI for your test application, use a separate non-production Okta app integration:

```sh
export PROMVIEW_AUTH_MODE=oidc
export PROMVIEW_OIDC_ISSUER_URL='https://your-org.okta.com/oauth2/default'
export PROMVIEW_OIDC_CLIENT_ID='your-test-client-id'
export PROMVIEW_OIDC_CLIENT_SECRET='your-test-client-secret'
export PROMVIEW_OIDC_REDIRECT_URL='http://localhost:8080/api/v1/auth/oidc/callback'
export PROMVIEW_OIDC_COOKIE_SECURE=false

docker compose up --detach --build
```

Never set `PROMVIEW_OIDC_COOKIE_SECURE=false` for a non-loopback deployment. Promview rejects that configuration.

## Verify The Integration

1. Open the Promview URL in a private browser window.
2. Confirm the console shows **Sign in with your identity provider** instead of loading alerts.
3. Sign in through Okta.
4. Confirm Okta returns to `/api/v1/auth/oidc/callback` and Promview redirects to `/`.
5. Confirm the top bar shows the expected display name and bound role.
6. Confirm alerts and the live stream start only after authentication.
7. Select **Sign out** and confirm Promview returns to the sign-in screen.

You can also inspect non-secret runtime configuration:

```sh
curl --fail 'https://promview.example.com/api/v1/config'
```

Expected response:

```json
{
  "authMode": "oidc",
  "productName": "Promview"
}
```

`GET /api/v1/me` requires the Promview session cookie, so testing it with a plain `curl` command returns `401` unless you supply a session obtained through the browser flow.

## Troubleshooting

### Okta Reports A Redirect URI Error

The Okta sign-in redirect URI and `PROMVIEW_OIDC_REDIRECT_URL` must match exactly, including scheme, host, port, path, and trailing slash. The Promview callback path has no trailing slash:

```text
/api/v1/auth/oidc/callback
```

### Promview Denies A Signed-In User

- Decode the ID token in a secure administrative environment and confirm the configured group claim is present as a string array.
- Confirm the user is assigned to the Okta application.
- Confirm the user belongs to an Okta group referenced by a Promview role binding with the exact same issuer URL.
- Confirm the `groups` scope and claim inclusion rules apply to the ID token, not only the access token.
- Avoid logging or sharing a real ID token while troubleshooting.

### Discovery Or Issuer Validation Fails

- Use the issuer from Okta's discovery response exactly.
- Do not mix the organization issuer with endpoints from a custom authorization server.
- Confirm the Promview container can resolve the Okta domain and establish a trusted TLS connection.
- Confirm outbound HTTPS access to Okta's discovery, token, and JWKS endpoints is allowed.

### Sign-In Loops Or The Session Cookie Is Missing

- Confirm users access Promview through the same HTTPS origin configured in the callback URL.
- Confirm the reverse proxy does not rewrite the callback path.
- Confirm browser or proxy policy is not stripping `Secure` or `SameSite=Lax` cookies.
- Check that server clocks are synchronized because ID-token expiry validation depends on accurate time.

### Signing Out Immediately Signs In The Same User Again

Promview logout revokes only the Promview session. The Okta SSO session remains active, so starting another login may not prompt for credentials. End the Okta session separately or use a private browser window when testing account switching.

## Okta References

- [Okta: Build a Single Sign-On integration with OIDC](https://developer.okta.com/docs/guides/build-sso-integration/openidconnect/main/)
- [Okta: Customize tokens with a groups claim](https://developer.okta.com/docs/guides/customize-tokens-groups-claim/main/)
- [Okta: Authorization servers](https://developer.okta.com/docs/concepts/auth-servers/)
