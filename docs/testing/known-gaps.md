# Known regression-contract gaps

Every `GAP` case requires a durable, accepted tracking decision here. A GAP documents required behavior that is not shipped; it never grants a release exception. In-scope P0 gaps block release, while P1 gaps require the same explicit recorded exception as any other failing P1 case. Rows are permanent: change `Open` to `Resolved` when the behavior ships or `Withdrawn` after an accepted decision removes the requirement, and add the resolving reference instead of deleting history.

The four initial entries were accepted as part of adopting this master catalog and are governed by the [catalog maintenance contract](README.md#catalog-maintenance-contract). Replace or supplement that decision link with an implementation issue when work is scheduled.

| Case ID | Status | Owning surface | Current limitation | Accepted decision / resolution |
|---|---|---|---|---|
| `PLEX-076` | Open | Plex manual-invite lifecycle | No truthful reconciliation/marking flow after the manual Plex fallback. | [Initial catalog adoption](README.md#catalog-maintenance-contract) |
| `PLEX-080` | Open | Plex admin realtime | Other admins' waiter counts do not receive a dedicated invite event. | [Initial catalog adoption](README.md#catalog-maintenance-contract) |
| `PLEX-081` | Open | Plex requester realtime | An open requester guide does not consume an invite-state event. | [Initial catalog adoption](README.md#catalog-maintenance-contract) |
| `PLEX-084` | Open | Plex shared-email lifecycle | A waiting requester cannot withdraw their shared Plex email. | [Initial catalog adoption](README.md#catalog-maintenance-contract) |
