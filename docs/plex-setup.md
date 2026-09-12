# Sign in with Plex

Plex sign-in lets someone use their Plex account to enter Cantinarr on web, iOS, Android, or the MCP authorization page. Both **Enable Plex sign-in** and **Allow automatic signup** start off. Library access and sign-in are separate: removing a Plex share later does not remove an established Cantinarr identity.

## Enable sign-in and review existing users

1. Open **Settings > Plex sign-in**.
2. Choose **Load fresh Plex accounts** to review existing Cantinarr media-account links. Each row shows the Cantinarr user, Plex username, numeric Plex account ID, email, and associated servers. Nothing is selected automatically.
3. Select only mappings you recognize, then **Confirm selected**. Cantinarr reads Plex again before saving. An ambiguous mapping, conflicting identity, changed server, or unavailable upstream account data stays unconfirmed. A typed Plex email alone is never a candidate or a sign-in identity.
4. Turn on **Enable Plex sign-in** and **Save settings**. Existing confirmed identities can now sign in. Disabling this switch revokes Plex devices and MCP grants and cancels pending attempts.

Users can also sign in to Cantinarr with an existing method and open **Settings > Account > Linked sign-in > Link with Plex**. Approval proves Plex's numeric account ID. Names and email addresses are display information, so changing them cannot move an identity to another Cantinarr account. One Plex identity can belong to one Cantinarr user, and each user can have one Plex identity. Linking preserves roles, kids policies, requests and grants.

New admin imports and explicit **Link account** selections record a login identity when fresh Plex data identifies one account unambiguously. A Plex import whose username is already taken asks the administrator to select the intended Cantinarr user explicitly through **Link account**. Unregistered invitations without a numeric account ID cannot establish a login identity yet. Media-account linking can succeed while login-identity confirmation needs review; the app reports these results separately.

The existing signed-in **Sign in with Plex** flow in the media-access guide also records the proven identity after rechecking the Cantinarr session that began it. Sharing or typing an email does not.

## Automatic signup

Turn on **Allow automatic signup** only if people already using your configured Plex servers should be able to create Cantinarr accounts themselves. Each signup reads current Plex account and server-share data. It requires an **accepted server share** or **verified ownership** of at least one configured Plex server. A friendship, pending invitation, removed share, or unreadable server does not prove access.

Signup creates an ordinary user with the normal account defaults and a unique username. It adopts only the server access verified in that request, without sending invitations, changing library scopes, or modifying Plex sharing. An existing media-account link owned by another Cantinarr user is a conflict requiring administrator review, never permission to take over that user.

## Browser approval and recovery

Choose **Continue with Plex**, approve Cantinarr in the browser tab or system browser, and return to Cantinarr. The initiating app polls every three seconds. **Reopen Plex**, **Check now**, **Retry sign-in**, and **Cancel** let you recover from a blocked browser launch or interrupted approval.

A pending attempt survives a refresh in the same browser tab or a native app restart. It retains its original server and purpose. Browser tab storage and native secure storage hold the private verifier; another tab cannot finish the attempt. Attempts expire after ten minutes or Plex's earlier PIN expiry. A completion ticket expires after one minute and can be exchanged once. Server restarts invalidate pending attempts; start again if that happens. A failed or cancelled sign-in keeps the current Cantinarr connection.

Plex tokens stay on the server. After account verification, Cantinarr deletes the temporary Plex device/token, including for refused sign-ins; if device deletion fails, it attempts token sign-out. A Plex outage is reported as unavailable, separately from denied access. Plex network calls use the server's external outbound transport.

Plex sign-in also works on a LAN HTTP origin; the MCP page computes its S256 challenge locally when the browser does not expose Web Crypto hashing. On the MCP authorization page, approval returns to the existing **Authorize** consent step. Consent remains bound to the original MCP client, redirect URI, scope, resource, state and PKCE parameters. Plex provenance follows the authorization code, device and refresh token.

## Unlinking and single sign-on policy

Users inspect and unlink Plex under **Linked sign-in**; administrators use **Users > account menu > Linked sign-in**. Unlinking revokes that user's Plex sessions and pending attempts while preserving library access. To unlink your own identity, first have another permitted password, passkey, or configured OIDC identity.

[Require single sign-on](oidc-setup.md) continues to require OIDC for regular users. Enabling it revokes their local and Plex sessions and pending grants. Plex does not satisfy the OIDC requirement for an invitation or automatic signup. Administrators retain recovery access; the Plex button is labeled **administrator recovery** under this policy.

The protocol follows Plex's [strong PIN authentication flow](https://forums.plex.tv/t/authenticating-with-plex/609370).

## Library invitations and sign-in review

Sending a Plex library invitation records the media-account link immediately; there is no second media-link step. **Users** reports each server independently: **Awaiting Plex acceptance** until the recipient accepts in their own Plex account, then **Active on server** after a fresh successful read. An unavailable read, or an older server without invitation-state support, is **Server access unconfirmed**. Pull to refresh Users after acceptance. Account names come from the current Plex response when available.

A media-account link does not establish a Plex sign-in identity. If **Load fresh Plex accounts** cannot find a unique account, check **Plex > Manage Library Access** and have the recipient accept the existing invitation, then load fresh accounts again and explicitly confirm the recognized mapping. Email alone never authorizes sign-in. Plex sign-in must also be enabled separately. A same-named Plex Home managed profile is distinct from the recipient's own Plex account.

The account-link picker marks pending invitations and offers **Refresh accounts** to reload the current directory without changing shares.
