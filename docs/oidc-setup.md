# Single sign-on with OpenID Connect

Cantinarr accepts one external OpenID Connect (OIDC) identity provider per server. Web, iOS and Android use Authorization Code with PKCE through the browser. Enable it in **Settings > Single sign-on**. All settings are off by default: SSO, automatic account creation, SSO-only policy and outbound proxy use. Group restrictions start empty.

## Register the provider

1. Set **Settings > External Address** to the origin your users can reach, such as `https://media.example.com`. HTTPS is required; HTTP is accepted only for loopback development (`localhost`, `127.0.0.1`, `[::1]`). The origin cannot include a path, query or fragment.
2. Register an OIDC client with authorization-code flow and PKCE S256. Use the exact **Registered callback** shown in Single sign-on settings: `https://media.example.com/api/auth/oidc/callback`. Do not register the mobile `cantinarr://oidc` return with the provider; the server receives and validates the provider callback for every client.
3. Enter the provider's issuer, client ID and client secret. The issuer must match its discovery document, including any realm/application path and trailing slash. Secrets are write-only and encrypted with the server's existing encryption key. A blank secret field preserves the saved secret; **Remove saved client secret** explicitly removes it for a public client.
4. Keep `openid profile email`, which Cantinarr always requests. Add any extra scope names needed for group claims. **Use outbound proxy** is off for direct connections, including self-hosted providers. Turn it on deliberately when this provider should use the server's outbound proxy (or standard proxy environment variables). The choice covers discovery, JWKS, token exchange and UserInfo. The user's browser must also be able to reach the provider's authorization page.
5. Save, run **Validate discovery**, then **Test sign-in**. Testing saves the displayed settings, opens the provider in your browser, and checks the full code exchange and required claims. It reports success without creating or linking an account. A successful discovery check alone does not verify the client secret or group claims.
6. Enable single sign-on. Users see **Continue with [provider label]** on the sign-in screen.

Discovery is fetched from the issuer's `/.well-known/openid-configuration`. Provider endpoints must use HTTPS, with the same loopback exception for development. A missing external address, unreadable saved secret, or invalid provider configuration produces an explicit configuration error. A provider outage produces a retryable sign-in error.

### Authentik

Create an OAuth2/OpenID provider and associate it with a Cantinarr application. Register the callback as an exact redirect URI. Use the default per-provider issuer mode, normally `https://auth.example.com/application/o/cantinarr/`; Authentik's global issuer mode serves discovery under a different path and is unsuitable for issuer-based discovery here. Configure scope mappings to return the desired group array. See [Authentik's provider and issuer documentation](https://docs.goauthentik.io/add-secure-apps/providers/oauth2/).

### Authelia

Register Cantinarr under `identity_providers.oidc.clients` with the exact redirect URI, authorization-code grant, PKCE S256, and scopes `openid`, `profile`, `email` plus `groups` when needed. Enter the plaintext client secret in Cantinarr and store its supported hash in Authelia; do not paste the hash into Cantinarr. Use your Authelia HTTPS issuer. See [Authelia's OIDC client configuration](https://www.authelia.com/configuration/identity-providers/openid-connect/clients/).

### Keycloak

Create an OpenID Connect client in the intended realm. Enable Standard Flow, configure its client authentication/secret, require S256 PKCE, and register the callback. Use `https://auth.example.com/realms/your-realm` as the issuer. For group restrictions, add a Group Membership protocol mapper to an assigned/requested client scope and emit a `groups` array in the ID token or UserInfo. Match the emitted names exactly, including any leading slash/full group path. See [Keycloak's OIDC endpoints](https://www.keycloak.org/securing-apps/oidc-layers) and [protocol mappers](https://www.keycloak.org/admin-api/protocol-mappers).

### Pocket ID

Create an OIDC client, register the callback and copy its client ID/secret into Cantinarr. Use the Pocket ID installation's HTTPS issuer. Assign the client to the permitted Pocket ID user groups; newly created clients require an allowed-group assignment there. Cantinarr's optional allowed groups are an additional check against the claims Pocket ID returns. See [Pocket ID's client group restrictions](https://pocket-id.org/docs/configuration/allowed-groups).

### Google

Create a **Web application** OAuth client, configure the consent screen/audience and any required test users, and register the server callback as an authorized redirect URI. The issuer is `https://accounts.google.com`. Use the web client's ID and secret even for Cantinarr's mobile apps, since the server exchanges the provider code. Google's standard OIDC claims do not provide a generic group list: leave Cantinarr's **Allowed groups** empty unless an intermediary provider supplies one. Cantinarr does not interpret Google email domains or `hd` as group membership. See [Google's OpenID Connect guide](https://developers.google.com/identity/openid-connect/openid-connect).

## Accounts, groups and invitations

An identity is the pair `(issuer, subject)`. Usernames, email addresses and Plex emails never match or merge existing accounts. Changing an issuer or a provider's subject mapping creates different identities.

Existing users sign in with their current Cantinarr method, open **Settings > Account > Linked sign-in**, and choose **Link with [provider]**. The initiating Cantinarr session must still be permitted when the link commits. Linking preserves the account's role, requests, kids policy and service grants. Each account can have only one identity for a given issuer; a provider identity can belong to only one account. Users and administrators can inspect the link; administrators use **Users > account menu > Linked sign-in**.

**Create accounts automatically** defaults off. When enabled, an unlinked identity creates an ordinary user with the same password/passkey, included-AI and service-grant defaults as a newly invited user. A provider name is only a suggested username; collisions receive a unique suffix and never join accounts. Cantinarr continues to manage roles and all access grants.

**Allowed groups** accepts one exact group name per line. **Group claim** defaults to `groups` and names a top-level claim, not a dotted JSON path. When restrictions are configured, the claim must be an array of nonempty strings and contain at least one configured name. Matching is case-sensitive. Missing or malformed required groups deny sign-in. UserInfo can supply missing claims only after its subject matches the verified ID token; it never overrides a signed claim.

In SSO-only mode, **every invitation requires both the link and successful SSO**, including an invitation for an administrator. Redeeming the link first returns an SSO-required continuation without consuming it or issuing local credentials. The app opens the provider; after successful checks the server atomically consumes the invitation, links the intended account and creates its SSO session. An identity linked to another account, or an invitation attempting to replace an existing link, is refused without consuming the invitation. Automatic account creation is not required for an invited account.

## Requiring SSO and recovering access

Before saving **Require single sign-on**, complete **Test sign-in** with the current configuration and ensure at least one administrator has a usable local password. A provider, secret, scope, group, signup, proxy or external-address change requires another successful test. Turn off the requirement before changing those settings, save and test, then enable it again.

Saving SSO-only mode revokes regular users' local device sessions and pending local MCP grants. Password, passkey, invitation, refresh, MCP token exchange and authoritative session checks enforce the policy. Local administrator password/passkey sign-in remains available for recovery; the sign-in screen keeps those controls visible. Cantinarr refuses to demote or delete the last administrator with a usable local password while the policy is active.

If the provider is unavailable, use an administrator's local password, repair and retest the configuration, or disable SSO. Disabling SSO restores local sign-in policy but never revives revoked sessions. The user's existing per-account password/passkey switches still apply.

Unlinking revokes the account's SSO devices, MCP sessions and pending grants for that issuer. Self-service unlinking is refused unless a permitted local password or passkey remains. An administrator can unlink another account, then issue an invitation if it needs to link a new provider identity.

## Session and browser behavior

SSO sessions follow the existing Cantinarr lifetimes: app access JWTs last 30 days, with stable opaque refresh tokens that do not expire or rotate. MCP access tokens last 15 minutes and MCP refresh tokens retain their 365-day lifetime and existing rotation rules. Every use checks the current device/account state. Provider tokens are discarded after sign-in and never become Cantinarr API or MCP credentials.

Group changes apply at the **next SSO sign-in**. Disabling a user at the provider, changing groups, or signing out of the provider does not terminate established Cantinarr sessions. For immediate removal, revoke Cantinarr devices, unlink the identity or delete the account. Role synchronization, provider deprovisioning, provider-wide logout and back-channel logout are not implemented.

Web authentication starts and returns at the configured external origin. A login opened through a LAN address moves there before storing its temporary verifier. Linking or testing from a different web origin opens that origin's settings; sign in there and repeat the action. Verifiers stay in tab storage, so finish in the same tab. Native apps open the system browser and return through `cantinarr://oidc`; the verifier survives an app restart in secure storage. Cancellation and failed sign-in preserve any existing session. A successful invitation-based account/server switch replaces the previous session only after the new one is accepted.

Attempts expire after ten minutes; callback handoffs are single-use and expire after sixty seconds. The app must exchange a handoff with its separate verifier. Intercepting the URL alone cannot obtain a session. Server restarts, configuration saves and unlinking invalidate pending flows; start again after one of these operations. Both authorization attempts and handoffs are bounded in memory.

The MCP authorization page also offers the provider. After SSO it names the signed-in user and still requires **Authorize** for the requested MCP client. Cantinarr's own OAuth issuer, client validation, PKCE and token audiences remain separate from the external provider. Configure MCP clients against the canonical external address so the browser stays on that origin.

No deployment environment variables or version compatibility floors change. Older clients continue using their existing methods unless an administrator deliberately requires SSO. The desktop scaffolding is outside the supported OIDC client set.

Protocol references: [OIDC Core](https://openid.net/specs/openid-connect-core-1_0.html) and [OAuth for Native Apps](https://www.rfc-editor.org/rfc/rfc8252.html). Automated tests use a local IdP for discovery, authorization, token exchange, JWKS and UserInfo. Real-provider interoperability and physical-device returns belong to the [manual authentication cases](testing/catalog/auth-users-security.md).
