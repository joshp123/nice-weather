Written by AI on 2026.10.16 by model GPT-6.1 Sol.

Run the climate measures probe once in a linked Sites cloud automation. It
processes one 3° tile, covering Spain and its southern coast, for local year
2020. Do not run the full climate build, deploy the app, refresh the forecast,
change a current pointer, or prune storage.

1. Call the linked Site's owner-only `begin_forecast_refresh` tool. Use its
   temporary upload grant only; do not run its forecast instructions. Keep the
   credential in memory and hidden stdin, never in commands, files or output.
2. Download `https://nice.jjpcodes.com/data/runner/climate-measures-probe/probe.py`.
   Launch it in a cloud TTY. Enter the grant into its hidden getpass prompt.
   Let it finish. Poll the running command without imposing a short deadline.
3. Read `https://nice.jjpcodes.com/data/forecast/climate-measures-probe/result.json`
   and the measures URL it returns. Verify the downloaded measures' SHA-256
   and length. Report the cloud wall time, peak RSS, bytes read and written,
   number of land cells and days. Keep source I/O separate from bootstrap
   download and uploaded bytes. The report must come from this cloud run.

The existing grant is limited to `forecast/`, so the scratch batch lives below
`forecast/climate-measures-probe/`. It never replaces live forecast files. The
normal forecast cleanup may later remove this scratch prefix. Save the measured
result in git before cleaning up the scratch automation. The full builder's
normal storage layout is `climate-measures/rRR-cCCC/YYYY.bin.gz` and uses an
owner publisher credential, read from standard input. A dedicated climate grant
would require a separately authorized Worker change and deployment.
