Written by AI (GPT-6.1 Sol), 2026-10-16.

Refresh the daily ECMWF forecast. Leave climate and place names unchanged.

1. Call this Site's `begin_forecast_refresh` MCP tool as its owner. It returns a one-hour credential, the Linux program URL, and the Site URL. Never print the credential or write it into a file, command argument, source, or report.
2. Download the program with curl, make it executable, and run `niceweather forecast --out ./artifacts --publish <returned Site URL> --token-stdin` on the cloud Linux machine. No packages or build tools are needed. Allow the process to finish; poll running commands rather than imposing a short timeout.
3. To pass the credential privately, launch a small Python wrapper in a TTY: read it with `getpass.getpass()`, then call `subprocess.run([...program arguments...], input=credential.encode(), check=True)`. Enter the tool-returned credential through hidden stdin only. It stays in memory.
4. Read back `/data/forecast/current.json` and `/api/place?lat=36.72&lon=-4.42`. Check the new `generatedAt`, 15 days and 12 months. Report the elapsed time, downloaded bytes and uploaded bytes from the program, without credentials. On failure, preserve the live forecast and report the actual error.

The program embeds its land mask, time zones and GRIB2/CCSDS decoder. It downloads ECMWF IFS data, applies the existing scoring, uploads files, and switches the forecast pointer last. Its libaec license notice is at `/data/runner/LICENSE.libaec`.
