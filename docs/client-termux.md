# Client (Android / Termux) setup

The client runs in [Termux](https://termux.dev) on the phone. It dials *out* to
your VPS over QUIC, so the phone needs **no inbound ports** and no root.

## 1. Install Termux

Install Termux from **F-Droid** (the Play Store build is outdated):
<https://f-droid.org/packages/com.termux/>

Then:

```bash
pkg update && pkg upgrade
termux-setup-storage      # lets you copy files in/out via shared storage
```

## 2. Get the client binary

### Option A — build inside Termux (works on every phone, incl. 32-bit)

```bash
pkg install golang git
git clone https://github.com/codingpenin/meghlock
cd meghlock
go build -o meghlock-client ./cmd/client
```

### Option B — cross-compile on your PC (arm64 phones), then copy over

Most modern phones are arm64:

```bash
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -o meghlock-client ./cmd/client
```

Copy it to the phone (e.g. into `Download/`), then in Termux:

```bash
cp ~/storage/downloads/meghlock-client .
chmod +x meghlock-client
```

> 32-bit phones (`GOARCH=arm`) need a C toolchain to cross-compile — just use
> Option A inside Termux instead.

## 3. Configure

Copy [cmd/client/client.conf](../cmd/client/client.conf) next to the binary and edit it:

```ini
[client]
server_address = "YOUR_VPS_IP:4433"     # the VPS quic_address
password       = "CHANGE-ME-long-random" # MUST match the server
client_id      = "client_android_2256"   # MUST match [server.clients.<id>]
auto_reconnect = true

# Mode:
# 'reverse' -> Expose local service to public VPS (Rathole / FRP mode)
# 'exit'    -> Mobile/local proxy exit gateway
mode           = reverse
local_target   = 127.0.0.1:8080

dns_servers    = 1.1.1.1:53, 8.8.8.8:53
max_incoming_streams = 32
insecure_skip_verify = true

[client.limits]
per_host_concurrency = 3
queue_wait_seconds   = 300
```

The three values that **must** line up with the server:
`server_address` → server's `quic_address`, `password`, and `client_id` →
a `[server.clients.<client_id>]` block.

## 4. Run

```bash
./meghlock-client -c config.toml
```

Expected logs:

```
[CLIENT] Starting QUIC client...
[CLIENT] Authenticated as: client_android_2256
[CLIENT] QUIC connection active
```

The server side should now show the client authenticated and the proxy port
listening. Point a browser's HTTPS proxy at `VPS_IP:<port>` to test.

## 5. Keep it running reliably

Android aggressively sleeps/kills background apps. To keep the tunnel up:

**Hold a wake lock** (stops the CPU sleeping):

```bash
pkg install termux-api
termux-wake-lock
```

**Run it detached** so closing the terminal doesn't stop it:

```bash
pkg install tmux
tmux new -s tunnel
./meghlock-client -c config.toml      # Ctrl-b then d to detach; 'tmux attach -t tunnel' to return
```

**Auto-start on boot** with Termux:Boot (install from F-Droid). Create
`~/.termux/boot/start-tunnel.sh`:

```bash
#!/data/data/com.termux/files/usr/bin/sh
termux-wake-lock
cd ~/meghlock
exec ./meghlock-client -c config.toml
```

```bash
chmod +x ~/.termux/boot/start-tunnel.sh
```

Also: in Android Settings → Apps → Termux, disable battery optimization /
allow background activity, so the OS doesn't kill it.

## Notes

- The phone uses its **default network** (mobile data or Wi-Fi, whatever is
  active). Disable Wi-Fi if you specifically want the mobile-data IP.
- If the phone changes IP/network, the client auto-reconnects (capped backoff).
- One phone = one exit IP for all users. To raise capacity / avoid per-site
  rate-limits, add a second phone as another `[server.clients.<id>]` on its own
  ports.
