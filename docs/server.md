# Server (VPS) setup

The server runs on a Linux VPS. It accepts the phone's QUIC connection and opens
a public TCP **proxy port** per client that browsers point at.

## 1. Build

On the VPS (needs Go 1.24+):

```bash
git clone https://github.com/portila/portila
cd portila
CGO_ENABLED=0 go build -o portila-server ./cmd/server
```

Or cross-compile on your PC and copy the binary over:

```bash
# amd64 VPS
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o portila-server ./cmd/server
# arm64 VPS
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o portila-server ./cmd/server
scp portila-server user@vps:/opt/portila/
```

## 2. Configure

Copy [cmd/server/server.conf](../cmd/server/server.conf) next to the binary and edit it:

```ini
[server]
quic_address = "0.0.0.0:4433"        # UDP port the client connects to
password     = "CHANGE-ME-long-random"  # must match the client

# One block per client device. The client's client_id must match this name.
# 'ports' are the public TCP ports mapped to this client.
[client_android_2256]
enabled = true
ports   = 44602
name    = "my device"

[server.limits]
max_concurrent_users = 1000
queue_wait_seconds   = 300   # how long a request waits for a free slot
```

Key points:
- **`password`** must be identical on the client. Pick a long random one.
- **`[server.clients.<id>]`** — the `<id>` must equal the client's `client_id`.
- **`ports`** are the proxy ports; each browser/app uses `VPS_IP:<port>`.
- TLS is auto self-signed (`[server.tls]`) — nothing to install.

## 3. Open the firewall

The phone needs the QUIC **UDP** port; browsers need the **TCP** proxy ports.

```bash
sudo ufw allow 4433/udp        # QUIC (quic_address)
sudo ufw allow 44602/tcp       # each proxy port from [server.clients.*].ports
```

On cloud providers, also allow these in the security group / firewall panel.

## 4. Run

```bash
./portila-server -c server.conf
```

You should see the phone connect:

```
🔐 QUIC listener: 0.0.0.0:4433
✓ Client authenticated: ... (ID: client_android_2256)
🎯 Port 44602 → client_android_2256 tunnel
```

## 5. Run as a service (stay up across reboots)

`/etc/systemd/system/portila.service`:

```ini
[Unit]
Description=Portila tunnel server
After=network-online.target

[Service]
WorkingDirectory=/opt/portila
ExecStart=/srv/portila/portila-server -c /srv/portila/server.conf
Restart=always
RestartSec=3
User=portila

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now portila
sudo journalctl -u portila -f      # logs
```

```bash for create user
sudo useradd --system --home /srv/portila --shell /usr/sbin/nologin portila
sudo chown -R portila:portila /srv/portila
```

```bash for restart
sudo systemctl daemon-reload
sudo systemctl restart portila
```

```bash for permission
sudo chmod +x /srv/portila/portila-server-arm64
```

## Using it from a browser

Configure your browser/OS to use this server as an **HTTP proxy** at
`VPS_IP:44602` (set the same value for the HTTP and HTTPS/SSL proxy fields). The
browser sends `CONNECT` for `https://` URLs, which is tunneled to the phone.
Visit any `https://` site — it exits through the phone. Plain `http://` is not
supported (CONNECT only).

## Security

Each proxy port is an open forward proxy: anyone who can reach `VPS_IP:44602`
can route through your phone. Before exposing it:

- Restrict the proxy ports to your users' IPs:
  `sudo ufw allow from <user-ip> to any port 44602 proto tcp` (and remove the
  open rule), **or**
- Keep the proxy ports closed publicly and reach them over a VPN/SSH tunnel.

One shared `password` authenticates the **phone↔server** link only — it does not
limit who connects to the proxy ports. Treat the ports as the thing to lock down.
