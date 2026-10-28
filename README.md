# ⚡ Portila

[![CI](https://github.com/portila/portila/actions/workflows/ci.yml/badge.svg)](https://github.com/portila/portila/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/portila/portila?style=flat&color=3b82f6)](https://github.com/portila/portila/releases)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.24-00ADD8?style=flat&logo=go)](https://golang.org)
[![Platform](https://img.shields.io/badge/platform-Linux%20|%20Windows%20|%20macOS%20|%20Android%20|%20FreeBSD-brightgreen)](#-supported-platforms)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> **A high-performance, ultra-low latency NAT traversal and reverse proxy tool built in Go over QUIC (HTTP/3).**
> A modern, memory-safe alternative to Rathole and FRP with zero bloat and native Android readiness.

```
            TCP Traffic / Proxy            QUIC (UDP, TLS 1.3)          Local Services / Internet
  Visitors ─────────────────────▶  Portila VPS Server ─────────────▶  Portila Client ─────────────▶  [ :8080 Web ]
                                      (Public Hub)      0-RTT Tunnel  (PC / Termux / Android)         [ :22 SSH  ]
```

---

## 📖 About Portila

**Portila** was engineered to solve the real-world shortcomings of legacy tunneling tools (FRP, ngrok, and TCP-based reverse proxies):

1. **Why QUIC over raw TCP?**
   - Traditional proxies suffer from **Head-of-Line (HoL) blocking**: a single dropped packet on TCP stalls every tunneled connection multiplexed over that connection.
   - Portila runs on **RFC 9000 QUIC (HTTP/3 transport)** with TLS 1.3 encryption by default. Each client connection is an independent QUIC stream. Packet drops on one connection never impede others.
   - **0-RTT connection resumption** and **instant connection migration** mean seamless operation when devices switch between Wi-Fi and 4G/5G mobile data.

2. **Dual-Mode Flexibility:**
   - **Mode `reverse` (Rathole / FRP):** Securely expose internal web applications, SSH, homelabs, or databases located behind restrictive NATs/firewalls without opening router ports.
   - **Mode `exit` (Tailscale / Egress Gateway):** Turn any remote device (including an Android smartphone with an unmetered SIM) into an encrypted egress exit node via standard HTTP `CONNECT` proxying.

3. **Zero-Bloat Philosophy:**
   - Only **1 core dependency** (`quic-go`). No bulky TOML parsers, no CGO dependencies, no unnecessary runtime overhead.
   - Built-in lightweight INI configuration engine with CLI flag overrides.

---

## 💻 Supported Platforms

Portila is compiled and verified across all major operating systems and CPU architectures:

| Platform | Architecture | Server (`portila-server`) | Client (`portila-client`) | Mobile AAR |
| :--- | :--- | :---: | :---: | :---: |
| **Linux** | `amd64` (x86_64) | ✅ | ✅ | — |
| **Linux** | `arm64` (aarch64) | ✅ | ✅ | — |
| **Linux** | `armv7` (Raspberry Pi) | ✅ | ✅ | — |
| **Windows** | `amd64` (x64) | ✅ | ✅ | — |
| **Windows** | `arm64` (Surface Pro / WoA) | ✅ | ✅ | — |
| **macOS** | Apple Silicon (`arm64`) | ✅ | ✅ | — |
| **macOS** | Intel (`amd64`) | ✅ | ✅ | — |
| **Android** | `arm64` / `v7a` (Termux CLI) | ✅ | ✅ | — |
| **Android** | Native Kotlin / Java App | — | — | ✅ (`portila-mobile.aar`) |
| **FreeBSD** | `amd64` (BSD Routers / OPNsense) | ✅ | ✅ | — |

---

- **🚀 Ultra-Low Latency QUIC Transport:** Built on RFC 9000 QUIC via `quic-go`. Delivers 0-RTT connection resumption, multiplexing without Head-of-Line blocking, and instant recovery on mobile network switching.
- **🔄 Dual Operation Modes:**
  - **Reverse Tunnel (Rathole / FRP Mode):** Expose any local service (`127.0.0.1:3000`, SSH `22`, databases, game servers) behind NAT/CGNAT to public VPS ports.
  - **Exit Node Gateway (Tailscale / Mobile Mode):** Use a phone's cellular connection as an encrypted HTTP CONNECT proxy exit node.
- **🎯 Multi-Service Routing:** Map multiple public VPS ports to different local target services on a single QUIC tunnel.
- **🛡️ Production Hardened:**
  - **Slowloris & Port Scanner Protection:** Automatically drops idle TCP probes after a 5s deadline without burning stream quotas.
  - **Constant-Time Auth & Rate Limiting:** SHA-256 password authentication with brute-force throttling.
  - **TCP Half-Close Semantics:** Clean bidirectional streaming with zero truncated payloads.
- **📱 Android & Gomobile Ready:**
  - Custom pure-Go DNS resolver bypassing Android's absent `/etc/resolv.conf`.
  - Exported [`pkg/mobile`](pkg/mobile) package for direct `.aar` compilation into native Kotlin/Java Android apps.
- **⚡ Zero-Bloat & Zero-Dependency:** 
  - Standard library INI/CONF parser (`# comments`, unquoted values, no JSON comma fatigue).
  - Clean CLI flags for 1-line execution without config files.
  - Only 1 core protocol dependency (`quic-go`).

---

## 🚀 Quick Start

### 1. Build Binaries
```bash
# Build server and client
go build -o portila-server ./cmd/server
go build -o portila-client ./cmd/client
```

### 2. Run Server (on public VPS)
Edit `server.conf` (or pass flags):
```ini
[server]
quic_address = 0.0.0.0:4433
password     = my-super-secret-token

[my_device]
enabled = true
ports   = 8080, 2222
```
Start server:
```bash
./portila-server -c server.conf
```

### 3. Run Client (behind NAT / home / mobile)

#### Mode A: Reverse Proxy (Rathole / FRP style)
Expose local port `3000` to VPS port `8080`, and local SSH `22` to VPS port `2222`:
```ini
[client]
server_address = YOUR_VPS_IP:4433
password       = my-super-secret-token
client_id      = my_device
mode           = reverse

[client.tunnels]
8080 = 127.0.0.1:3000   # Web app
2222 = 127.0.0.1:22     # SSH
```
Start client:
```bash
./portila-client -c client.conf
```
*Or directly via 1-line CLI:*
```bash
./portila-client -server YOUR_VPS_IP:4433 -pass my-super-secret-token -id my_device -mode reverse -target 127.0.0.1:3000
```

#### Mode B: Mobile Exit Node (Egress Gateway)
In `client.conf`, set `mode = exit`. Point any browser/app HTTPS proxy to `YOUR_VPS_IP:8080` to exit through the client's network.

---

## 📁 Architecture Overview

```text
portila/
├── cmd/
│   ├── server/        # VPS relay hub entrypoint (server.conf)
│   └── client/        # NAT agent entrypoint (client.conf)
├── pkg/
│   ├── client/        # Client engine, host pacing & pure-Go DNS
│   ├── server/        # Server session manager & port routing
│   ├── config/        # Zero-dependency INI/CONF + JSON parser
│   ├── protocol/      # Binary framing & StreamRW abstractions
│   ├── relay/         # Full TCP half-close stream pipe (relay.Pipe)
│   ├── tls/           # Ephemeral TLS 1.3 self-signed cert generator
│   └── mobile/        # Gomobile bindings for Android/iOS (.aar)
├── test/              # Integration & edge-case battle tests
├── docs/              # Deployment guides (server.md, client-termux.md)
└── example/           # Local concurrency simulation & benchmark site
```

---

## 🧪 Testing & Verification

Run the battle-tested test suite across all packages:
```bash
go test -v ./pkg/... ./test
```

Includes edge-case suites:
- `TestSlowlorisPortProbe` (verifies 5s dead-connection dropping)
- `TestMultiPortServiceRouting` (verifies concurrent multi-service proxying)
- `TestReversePayloadIntegrity` (verifies 5MB transfer with 100% SHA-256 hash match)
- `TestTunnelConcurrency` (verifies 20+ concurrent streams without drops)

---

## 📄 License
MIT License. Copyright (c) 2026 Portila Authors.
