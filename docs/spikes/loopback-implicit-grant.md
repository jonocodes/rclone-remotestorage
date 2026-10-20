# Spike: can a CLI get a remoteStorage token through a loopback redirect?

Run 2026-10-19. Code: [`spike/loopback/`](../../spike/loopback/). Answers open question 1 of [the research note](../research/rclone-remotestorage-backend.md#8-open-questions).

**Verdict: yes, for every server checked, including 5apps.** A CLI sends `client_id=http://127.0.0.1:53682`, `redirect_uri=http://127.0.0.1:53682/`. The token arrives in the fragment, and a static page served by the listener captures it with 4 lines of JS and POSTs it back. This was proven end to end against armadietto (scripted) and against 5apps, the main hosted provider (manual run with a real account, 2026-10-19).

## How it works

1. WebFinger `user@host` → storage root plus the implicit-grant auth URL. This accepts the draft rel, legacy `remotestorage` rels and the legacy `auth-endpoint` property.
2. Listen on `127.0.0.1:53682`, the same port as rclone's `oauthutil`.
3. Open `<auth>?client_id=<origin>&redirect_uri=<origin>/&response_type=token&scope=*:rw&state=<random>`.
4. The server redirects to `http://127.0.0.1:53682/#access_token=…&state=…`. `GET /` serves a page that does `fetch("/fragment", {method: "POST", body: location.hash.slice(1)})`.
5. `POST /fragment` checks `state`, surfaces `error`/`error_description`, and accepts the token.
6. The token is verified with `GET <storage_root>/` and a bearer token.

## Results

| Server | Loopback redirect accepted? | How we know |
|---|---|---|
| armadietto (monolithic, `-10`) | **Yes, tested.** `just spike-loopback` passes: 302 to the loopback URL, fragment captured by headless Chromium, root listing 200. | Live. It also only checks that `redirect_uri` parses as a URL, with no origin rule (`lib/controllers/oauth.js` `invalidOAuthRequest`; the modular server is the same in `lib/routes/oauth.js` `validOAuthRequest`). |
| php-remote-storage | **Yes, per source.** It requires `client_id` and `redirect_uri` to share an origin (scheme+host+port) and rejects a `?` in `redirect_uri`. http is allowed ("XXX we also should enforce HTTPS"). | `src/OAuth/OAuthModule.php` `validateRequest`/`validateClient`/`determineOrigin`. Not run live. |
| mysteryshack | **Yes, per source.** It derives `client_id` from the redirect URI's origin itself. | `src/mysteryshack/web/oauth.rs` L128-L140. Not run live. |
| RSilo | **Yes, per source.** It requires an http(s) `client_id` with the same origin as `redirect_uri`. | `src/routes/consent.ts` L54-L76. |
| 5apps (`-13`) | **Yes, tested.** After sign-in and consent, the browser landed on the loopback page, which reported "Authorized. You can close this window and return to rclone." | Manual run by the project owner with a real account: `just spike-loopback-live <user>@5apps.com` (2026-10-19). Before sign-in, every request to `/rs/oauth/<user>` gets a 302 to `/users/sign_in` with no parameter check, which is why this needed an account. |

## Design consequences for the backend

- **Rules for `client_id` and `redirect_uri`:** set `client_id` to the loopback origin with no path, and `redirect_uri` to that origin plus `/` with no query string. This satisfies php-remote-storage's no-`?` rule and the same-origin rule enforced by php-remote-storage and RSilo. 5apps accepts it as well.
- **Consent screens will show "127.0.0.1:53682"** as the app. That is accurate but unfriendly, and it is the same for every rclone user, so servers that remember grants per `client_id` will lump them together. It is acceptable for now. Spec issue #202 (Client ID Metadata Documents) would fix it later.
- **rclone integration:** `oauthutil`'s server only reads `?code=`, so the backend needs either its own listener (as in the spike, about 60 lines) or a small upstream hook. Starting with its own listener avoids blocking on upstream.
- **Headless/remote machines:** rclone's `rclone authorize` pattern still applies. Run the loopback flow on a machine with a browser and paste the resulting token. A paste-token config option is needed anyway.

## Still open

- Whether any server rejects `http://` (as opposed to `https://`) redirect URIs in production configs. armadietto's `blockUnsecureRequest` applies to the *server's* own scheme, not the redirect.
