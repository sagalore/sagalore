# Characters and participating

A character is not a login. The addon `CHARACTER` line reports name, realm, level, race, class, and optional BattleTag. The API attaches that character to the logged-in Battle.net account.

- If the line includes a BattleTag, it must match the account. The addon does not assign a BattleTag the account does not already have.
- A name and realm sticks to the first account that linked it.
- `characters.participating` defaults to false. Characters that already existed when sagas were moved into the bootstrap group are set to true, so the current player is not asked again for toons already in use.
- Personal is account-scoped: membership is the Personal `group_members` row. There is no opt-in dialog, and Sync clears leftover Personal include-invites. Personal sync does **not** set `participating=true` — that flag is only the friend-group include switch ("Include Magni in Gutar?"). Sync sets Personal up before it creates those friend-group include invites.
- For friend groups, later characters stay out until they accept. The dialog is "Include [name] in [group]?" Accept marks that character participating. Decline sticks for that invite. Not now snoozes the dialog for 12 hours in SavedVariables.
- Join links (`/join/{token}` and `POST /v1/join/{token}`) mark **only the selected** characters participating when the account joins (or when a gated request is approved). Unchecked alts stay false so other friend groups can still prompt include-invites.

`PATCH /v1/characters/{id}` with `{"participating": true}` is the same switch for a client that is not the addon. `GET /v1/me/characters` lists the account roster for join-link UIs and other clients.

WoW Forever characters are not listed by Blizzard's profile API. Sagalore does not block on that. An optional later flag may record that the profile API confirmed a character. It is not required to play.
