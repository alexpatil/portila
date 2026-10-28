# Contributing to Portila

Thank you for your interest in contributing to **Portila**! We welcome community contributions, bug fixes, performance enhancements, and documentation improvements.

---

## 🛠️ Development Setup

### Prerequisites
- **Go 1.24+** installed.
- Git.

### Clone the Repository
```bash
git clone https://github.com/portila/portila.git
cd portila
```

### Run Tests
Always ensure all unit and integration tests pass with race detection:
```bash
go test -v -race ./pkg/... ./test
```

### Build Binaries Locally
```bash
go build -o bin/portila-server ./cmd/server
go build -o bin/portila-client ./cmd/client
```

---

## 📐 Architecture & Coding Guidelines

1. **Zero Unnecessary Dependencies:**
   - Keep dependencies minimal. We intentionally only depend on `github.com/quic-go/quic-go`.
   - Do NOT introduce external configuration, logging, or utility libraries unless discussed beforehand.
2. **Standard Library First:**
   - Configuration is handled via our lightweight custom parser (`pkg/config`).
   - Logging uses standard `log`.
3. **Protocol Integrity:**
   - Any changes to framing in `pkg/protocol` must maintain backwards compatibility or bump the ALPN version identifier in `pkg/protocol/protocol.go`.
4. **Code Quality:**
   - Format code using `go fmt ./...`.
   - Run `go vet ./...` before submitting a pull request.
   - Include test cases for any new bug fix or feature under `pkg/<module>/*_test.go` or `test/`.

---

## 🚀 Submitting a Pull Request (PR)

1. Fork the repository and create your feature branch from `main`:
   ```bash
   git checkout -b feature/amazing-feature
   ```
2. Commit your changes with clear, descriptive commit messages:
   ```bash
   git commit -m "feat(relay): optimize buffer pooling in bidirectional copy"
   ```
3. Push to your fork:
   ```bash
   git push origin feature/amazing-feature
   ```
4. Open a Pull Request targeting the `main` branch.

---

## 🐛 Reporting Bugs

If you find a bug or unexpected behavior:
- Check existing issues to see if it has already been reported.
- Open a new issue using our **Bug Report** template, providing:
  - OS and architecture (e.g., Linux amd64, Android arm64).
  - Portila version / commit hash.
  - Reproducible steps and relevant logs (with passwords/tokens redacted).
