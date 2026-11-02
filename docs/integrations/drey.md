# drey integration

**Separation.** Everything drey-specific in this document lives in this appendix. The dricket core has no dependency on drey, builds and runs without it, and treats drey like any other client. drey is the primary consumer and drives the first requirements; dricket itself is a general ticketing system.

**What belongs to drey, not to dricket.**

- the user interface and its wording
- the bundled `dk` version and the update message
- the prefix `drey` and the mapping from drey projects to prefixes
- the choice of credentials for end users

**How drey uses dricket.** drey is a client of `dk ... --json`. End users never see files. The frontend never writes into the repo directly, so identity, state and validation stay in one place.

**Rules for drey.**

- drey bundles a fixed dk version. `min_version` in the config prevents drift. On exit 16 drey shows an update message.
- Every edit carries the version token (`--if-version`). On exit 12 drey shows "Changed in the meantime" and offers to load the new state.
- Exit codes map to readable messages: 11 "Already taken by X", 13 "Your claim has expired", 14 "This transition is not allowed", 15 "Repository unreachable".
- The view shows "Last synced". It refetches on open and at an interval. ⚠️ Interval open (proposal 30 to 60 s, unmeasured).
- Raw file editing exists only as a debug path for developers.

**Open hurdle.** Users without a Git account need credentials for the ticket repo. Two ways: drey holds a service account and writes on behalf of users (then the pusher identity is lost; the real user appears only in the log), or each user gets their own account. ⚠️ Not decided, depends on the user group. Recommendation: service account for simple users, own accounts for admins, and always record the acting user in the log.


_Moved here from the concept so that the core documentation stays free of any single consumer._
