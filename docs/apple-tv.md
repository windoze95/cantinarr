# Open titles in Infuse on Apple TV

Cantinarr can open a movie or series page in Infuse on a paired Apple TV.
Use it from the iOS app, Android app, or a web browser. The Cantinarr server
sends the command, so the phone does not have to be an Apple device or share
the TV's network. No new iOS entitlement, signing capability, App Store Connect
permission, or Apple account connection is needed.

## Pair a TV

1. Install Infuse on the Apple TV and turn the TV on. Connect its media
   libraries in Infuse as usual.
2. As a Cantinarr admin, open **Settings > Admin > Apple TVs > Add Apple TV**.
3. Select **Search for TVs**. If discovery finds nothing, enter the TV's IP
   address or hostname and search again. An empty discovery result only means
   no TV answered that search; container networking and separate subnets can
   prevent local discovery.
4. Choose the TV and enter the four-digit PIN displayed on it. Pairing expires
   after five minutes and can be cancelled.
5. Open the saved TV and select **Check connection** to verify that the
   server can authenticate and Infuse is installed. Rename the TV or update
   its address here when needed. Save address changes before checking them.

All Cantinarr admins can control paired TVs. In a TV's settings, choose which
other adults can use it and save. Kids accounts cannot list or control TVs,
even if an older grant exists. Removing a person from the list revokes their
TV access. **Forget TV** removes the server's saved credentials and all its
grants; it does not remove Infuse or change its libraries. To revoke the
pairing on the TV as well, remove Cantinarr under the TV's remote/device settings.

## Open a title

A movie or show's detail page offers **Open on [TV name]** when Cantinarr has
verified the title in one of your connected media-server accounts and you
have a TV available. With multiple TVs, **Open on Apple TV** asks you to choose
one. This action is separate from **Open in Infuse**, which opens Infuse on
the phone itself.

After sending, check the TV. If tvOS displays **Open in “Infuse”?**, use
**Confirm Open** in Cantinarr while that prompt is visible. If the title page
is already open, select **Done**. Confirmation is one use and expires after
30 seconds; Cantinarr never presses Select automatically. A sent command is
an acknowledgement from the TV protocol, not verification of the screen.

The link opens a title page without requesting playback. Infuse uses the
libraries and account configured on that TV; Cantinarr does not switch its
profile or grant it media access. A title absent from the TV's Infuse libraries
can open a metadata-only page. Cantinarr rechecks the caller's current media
access before sending, but that does not prove the TV has the same library.

## Server requirements and troubleshooting

Both official container images include the pinned pyatv 0.18.0 helper. It uses
Companion pairing only. The helper communicates privately with Cantinarr over
stdin/stdout and opens no HTTP service. It needs ordinary outbound LAN access
for mDNS discovery and the TV's advertised Companion TCP port. Discovery uses
UDP 5353; the Companion port is discovered again for each operation. No new
published container port or host networking mode is required. Direct-address
discovery must still be able to reach the TV's discovery service.

An IPv4 address or hostname is supported. If a hostname cannot resolve from
inside the server's container, use the TV's IPv4 address. A DHCP reservation
can keep it stable. Credentials are matched to the discovered device identity;
an unrelated device at an old address will not receive the pairing key. After
a TV reset or revoked pairing, choose **Pair again**. Re-pairing the same TV
preserves its Cantinarr identity and user grants.

Pairing credentials are encrypted with Cantinarr's existing encryption key
in its database. PINs and incomplete pairings stay in memory. Restarting the
server cancels pending pairings and confirmations. Keep the existing `/config`
volume and encryption key when upgrading. Multiple server replicas do not
share pending operations; pairing and confirmation requests must reach the
same server process.

For a manual Linux installation, install Python 3.11, its venv support, and
the native build dependencies for the locked packages (a C compiler, Python
headers, libffi headers, and the C++ runtime). From this checkout's root, as
the administrator of that server:

```sh
python3.11 -m venv /opt/cantinarr-appletv
/opt/cantinarr-appletv/bin/pip install --require-hashes -r server/tools/apple_tv/requirements.lock
install -d /usr/lib/cantinarr/apple_tv
install -m 0644 server/tools/apple_tv/cantinarr_appletv.py /usr/lib/cantinarr/apple_tv/
install -m 0755 server/tools/apple_tv/helper.sh /usr/local/bin/cantinarr-appletv-helper
cantinarr-appletv-helper --self-test
```

The helper must be executable on the Cantinarr process's `PATH`. Its fixed
wrapper uses `/opt/cantinarr-appletv/bin/python`. The setup screen reports a
missing helper without preventing the rest of Cantinarr from working.

The [Infuse URL API](https://support.firecore.com/hc/en-us/articles/215090997-API-for-Third-Party-Apps-Services)
defines the movie and series links. The [pyatv apps API](https://pyatv.dev/development/apps/)
provides Companion app discovery and URL launching. The helper also checks
Companion rejection frames that pyatv 0.18.0 can otherwise treat as successful
replies; the dependency pin and worker tests cover that compatibility shim.
