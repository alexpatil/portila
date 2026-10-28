<div align="center">

# 🌀 Portila

**High-performance, ultra-low latency reverse tunnel and mobile exit gateway built in Go over QUIC (HTTP/3).**

A modern, memory-safe alternative to Rathole and FRP with zero bloat.

[![CI](https://github.com/alexpatil/portila/actions/workflows/ci.yml/badge.svg)](https://github.com/alexpatil/portila/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/alexpatil/portila?style=flat&color=3b82f6)](https://github.com/alexpatil/portila/releases)
[![Go Version](https://img.shields.io/badge/Go-%3E%3D1.24-00ADD8?style=flat&logo=go)](https://golang.org)
[![Platform](https://img.shields.io/badge/Platform-Linux%20|%20macOS%20|%20Windows%20|%20Android-brightgreen)](#-platforms)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

</div>

---

## ✨ Highlights

* **QUIC (HTTP/3) Transport:** 0-RTT connection resumption, multiplexing without Head-of-Line blocking, and instant Wi-Fi/cellular connection migration.
* **Dual Mode:**
  * **Reverse Tunnel:** Expose local services (Web apps, SSH, databases) behind NAT/firewalls to public VPS ports.
  * **Exit Gateway:** Route traffic through remote devices or mobile 4G/5G connections via encrypted HTTP CONNECT proxy.
* **Multi-Port Routing:** Map multiple public VPS ports to different local target services on a single tunnel.
* **Production Hardened:** Automatic Slowloris protection, constant-time password authentication, and rate limiting.
* **Zero Dependencies:** Pure Go standard library + official `quic-go`. No external config libraries or runtime bloat.

---

## 🚀 Quick Start

### 1. Download

Download and extract the latest release for your OS from **[GitHub Releases](https://github.com/alexpatil/portila/releases/latest)**:

| OS | Download |
| :--- | :--- |
| **Linux (x86_64)** | `portila_v0.1.0_linux_amd64.tar.gz` |
| **Linux (ARM64)** | `portila_v0.1.0_linux_arm64.tar.gz` |
| **macOS (Apple Silicon)** | `portila_v0.1.0_darwin_arm64.tar.gz` |
| **macOS (Intel)** | `portila_v0.1.0_darwin_amd64.tar.gz` |
| **Windows** | `portila_v0.1.0_windows_amd64.zip` |

> 💡 *Each release package contains pre-built binaries (`portila-server`, `portila-client`) and default config templates (`server.conf`, `client.conf`). No Go installation needed.*

### 2. Server (VPS)

Edit `server.conf`:
```ini
[server]
quic_address = 0.0.0.0:4433
password     = your-secret-token

[my_device]
enabled = true
ports   = 8080, 2222
```

Run server:
```bash
./portila-server -c server.conf
```

### 3. Client (Local / Behind NAT)

**Reverse Mode (Expose local services):**
```ini
[client]
server_address = YOUR_VPS_IP:4433
password       = your-secret-token
client_id      = my_device
mode           = reverse

[client.tunnels]
8080 = 127.0.0.1:3000   # Web app -> VPS:8080
2222 = 127.0.0.1:22     # SSH     -> VPS:2222
```

Run client:
```bash
./portila-client -c client.conf
```

*Or run directly via CLI flag (no config file needed):*
```bash
./portila-client -server YOUR_VPS_IP:4433 -pass your-secret-token -id my_device -mode reverse -target 127.0.0.1:3000
```

---

## 💻 Platforms

| OS | Architectures | Server | Client | Mobile |
| :--- | :--- | :---: | :---: | :---: |
| **Linux** | `amd64`, `arm64`, `armv7` (Raspberry Pi) | ✅ | ✅ | — |
| **macOS** | Apple Silicon (`arm64`), Intel (`amd64`) | ✅ | ✅ | — |
| **Windows** | `amd64`, `arm64` | ✅ | ✅ | — |
| **Android** | Termux CLI / Native Gomobile (`.aar`) | ✅ | ✅ | ✅ |
| **FreeBSD** | `amd64` (Routers / OPNsense) | ✅ | ✅ | — |

---

### 🛠️ Building from Source (Optional)

If you prefer building from source:
```bash
git clone https://github.com/alexpatil/portila.git && cd portila
go build -o portila-server ./cmd/server
go build -o portila-client ./cmd/client
```

---

## 🧪 Testing

Run the test suite:
```bash
go test -v -race ./pkg/... ./test
```

---

## 📄 License

[MIT License](LICENSE) © 2026 Portila Authors.
