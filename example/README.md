# Example: local end-to-end demo

A self-contained way to see the tunnel behave like a real site on localhost —
including login, the shared **exit IP**, and pages that pull several cached
CSS/JS assets.

## 1. Load simulation (no browser needed)

Boots a meghlock server + client in-process, serves the demo site over HTTPS,
and drives N browser-like users (each logs in, then loads pages whose assets are
fetched in parallel and cached) through the CONNECT proxy.

```bash
# Polite: at most 3 connections to the same site at once (default)
go run ./example/loadtest -users 10 -pages 5 -perhost 3

# Contrast: no per-site cap — watch site peak concurrency spike (the "overload"
# burst from one IP that gets you rate-limited/blocked)
go run ./example/loadtest -users 10 -pages 5 -perhost 0
```

Flags: `-users`, `-pages`, `-perhost` (per-site cap), `-streams` (global limit),
`-work` (per-asset delay), `-think` (pause between pages).

The report shows total requests, failures, page-load latency percentiles, the
site's **peak concurrency** (proves per-site pacing), and that every user exits
through a **single IP**.

## 2. Browse the demo site manually

Runs just the site over HTTPS so you can open it in a browser (directly, or by
setting your browser's HTTPS proxy to a running meghlock server).

```bash
go run ./example/serve -addr :8443
# then open https://localhost:8443  (accept the self-signed cert)
```

Login accounts are printed at startup (e.g. `alice` / `password1`). Pages live at
`/p/<name>` (dashboard, profile, billing, reports, settings); each shows the exit
IP the site sees and pulls shared + page-specific assets.
