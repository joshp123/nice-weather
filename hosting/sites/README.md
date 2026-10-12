Written by AI (GPT-6 Astra), 2026-10-12.

Nice Weather uses the existing owner-private ChatGPT Sites project at
`https://nice-weather.jjpcodes.chatgpt.site`. Hosting and artifact storage use
Sites-managed included features; no personal Cloudflare account or paid plan is
connected. The project reports `requires_byop: false`.

The Worker contains the exact frontend build. Sites-managed R2 stores immutable
weather artifacts. GET, HEAD, conditional requests and single byte ranges retain
the Go server's content types and cache policy. The data pipeline runs elsewhere;
Sites does not acquire or score weather.

Build from the repository's declared runtime:

```sh
devenv shell -- npm run build
devenv shell -- node hosting/sites/build.mjs dist /path/to/sites-source-checkout
```

Use the Sites skill's source-repository helper to push this exact generated
checkout and package its `dist`, then save and deploy that source revision. The
project ID and R2 binding are in `.openai/hosting.json` beside the hosting scripts.

Publish data without republishing the frontend:

```sh
devenv shell -- node hosting/sites/publish.mjs artifacts https://nice-weather.jjpcodes.chatgpt.site
```

The publisher reads `publishToken` and `siteAccessToken` as JSON from hidden stdin.
The first matches the Sites runtime secret `WEATHER_PUBLISH_TOKEN`; the second is
the Site's supported service-access credential. Store neither in source. It sends
only active forecast, place, shortlist and accepted ERA5 climate releases. It
resumes immutable files by SHA-256 and writes pointers after all files succeed.
After successful publication, an authenticated pruning request retains the
forecast pointer's current and previous runs and their shortlists. Older generated
forecast files are removed from Sites storage and local publication storage;
each pass reports reclaimed runs, files and bytes. Places and accepted climate
evidence are kept independently.

The optional `nice.jjpcodes.com` binding and its three DNS records were removed
because Route53 charges for public queries. The existing DNS zone remains under
`/Users/josh/code/opentofu-infra/websites/jjpcodes.com`.
Forecast scheduling belongs to the existing `mini-server-1` Nix host: one outbound
daily job runs `forecast-world`, then this publisher. No website listener runs on
the host. The first complete acquisition, forecast build and Sites publication
succeeded on 2026-10-12.

The Linux job uses a compiled binary and this publisher script. Source stays in
this repository. Update them after accepting the source and forecast contract:

```sh
devenv shell -- env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o .cache/niceweather-linux ./cmd/niceweather
scp .cache/niceweather-linux root@mini-server-1:/var/lib/nice-weather-finder/bin/niceweather.next
scp hosting/sites/publish.mjs root@mini-server-1:/var/lib/nice-weather-finder/publish.mjs.next
ssh root@mini-server-1 'chown nice-weather:nice-weather /var/lib/nice-weather-finder/bin/niceweather.next /var/lib/nice-weather-finder/publish.mjs.next; chmod 0755 /var/lib/nice-weather-finder/bin/niceweather.next; chmod 0644 /var/lib/nice-weather-finder/publish.mjs.next; mv /var/lib/nice-weather-finder/bin/niceweather.next /var/lib/nice-weather-finder/bin/niceweather; mv /var/lib/nice-weather-finder/publish.mjs.next /var/lib/nice-weather-finder/publish.mjs'
```

Nix declares Node, GDAL and ecCodes and loads the sealed publishing credential
outside the Nix store. Runtime artifacts, source caches and staging files live
under `/var/lib/nice-weather-finder`. The daily timer is enabled at 21:30 UTC;
missed scheduled runs resume when the host returns.
