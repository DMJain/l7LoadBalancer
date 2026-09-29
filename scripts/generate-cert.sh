#!/usr/bin/env bash
#
# generate-cert.sh produces a self-signed SAN certificate for local dev and
# benchmarks (S5.T1). The same certificate is used by the LB frontend and the
# dummy backends; the SAN list covers every docker-compose service name plus
# loopback, so one cert works everywhere.
#
# SANs are mandatory, not optional: Go's crypto/tls client rejects certificates
# that carry only a Common Name (since Go 1.15), so a CN-only cert fails TLS
# verification in Go clients. Every required name is a SAN here.
#
# Usage: scripts/generate-cert.sh [output-dir]   (default: certs)
# Output: <dir>/server.crt and <dir>/server.key. The certs/ directory is
# gitignored (generated, machine-specific, and never a source artifact).

set -euo pipefail

cd "$(dirname "$0")/.."
out="${1:-certs}"
mkdir -p "$out"

if ! command -v openssl >/dev/null 2>&1; then
  echo "generate-cert.sh: openssl is required but not found on PATH" >&2
  exit 1
fi

conf="$(mktemp)"
trap 'rm -f "$conf"' EXIT

cat >"$conf" <<'EOF'
[req]
distinguished_name = dn
x509_extensions = v3_req
prompt = no

[dn]
CN = localhost

[v3_req]
subjectAltName = @alt_names

[alt_names]
DNS.1 = localhost
DNS.2 = lb
DNS.3 = backend1
DNS.4 = backend2
DNS.5 = backend3
DNS.6 = backend4
IP.1 = 127.0.0.1
IP.2 = ::1
EOF

openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout "$out/server.key" -out "$out/server.crt" -config "$conf"

echo "wrote $out/server.crt and $out/server.key"
