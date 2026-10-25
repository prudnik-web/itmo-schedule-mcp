# One-time Google setup

The standalone app uses **your own Google OAuth client**. It does not need your
Google password, a service account, or a shared Google project maintained by
someone else. Creating the client is the main one-time setup step.

1. Open the [Google Cloud console](https://console.cloud.google.com/), create or
   select a project, and enable the **Google Calendar API**.
2. Open **Google Auth platform → Branding**. Configure an app name such as
   **My ITMO Calendar Sync** and your support/contact email.
3. Under **Audience**, choose **External** for a personal Gmail account. While
   testing, add your own Google account as a test user. A Workspace organization
   may provide an Internal option; follow your organization's policy.
4. Under **Data Access**, add the default scopes:
   - `https://www.googleapis.com/auth/calendar.app.created`
   - `https://www.googleapis.com/auth/calendar.calendarlist.readonly`
5. If you intend to use `--migrate-primary`, also add:
   - `https://www.googleapis.com/auth/calendar.events.owned`
6. Under **Clients**, create a client of type **Desktop app**, then download its
   JSON file to a private location outside the source checkout. Do not select
   Web application; the desktop flow uses a local loopback callback.
7. For ongoing unattended use, review the publishing status under **Audience**.
   External apps in **Testing** receive Calendar refresh tokens that expire after
   seven days. Switch to **In production** before authorizing for long-term use,
   and follow Google's personal-use rules and any verification requirements
   shown for your project. Changing status does not make a previously issued
   short-lived token permanent; authorize again afterward.
8. Run the app's `setup` command and provide the JSON path when asked. Select the
   Google account whose calendar you want to use and grant the requested access.

The default permissions let the app create and manage its own secondary calendar
and find it in your calendar list. Primary-calendar cleanup requests the additional
permission to edit events on calendars you own. Cleanup itself is disabled unless
you explicitly select it.

OAuth uses state validation and PKCE with a callback on `127.0.0.1`. The refresh
token is saved in protected local state so later syncs do not require browser
interaction. If you revoke access or the token expires, run:

```sh
itmo-calendar-sync auth --client /path/to/google-desktop-client.json
```

On a headless server, use the SSH tunnel instructions in
[calendar sync](calendar-sync.md#headless-server).

References: [Google's Desktop client setup](https://developers.google.com/workspace/calendar/api/quickstart/go),
[Calendar scopes](https://developers.google.com/workspace/calendar/api/auth),
[refresh-token expiration](https://developers.google.com/identity/protocols/oauth2#expiration),
and [Desktop loopback support](https://developers.google.com/identity/protocols/oauth2/resources/loopback-migration).
