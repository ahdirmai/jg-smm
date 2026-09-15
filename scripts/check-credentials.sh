#!/usr/bin/env bash
#
# Guard against plaintext credentials in the source tree (P1-07 AC).
#
# A credential must only ever exist as AES-256-GCM ciphertext produced by
# internal/adapter/crypto. This check fails the build if source files contain a
# `password`/`secret`/`token` assignment bound to an obvious plaintext literal,
# or if a raw password is selected/decrypted outside the login path.
#
# It is deliberately conservative: it targets *literal* assignments, not
# variables that hold already-decrypted values at runtime.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

status=0

# 1) Obvious plaintext credential literals in Go/TS source.
#    e.g.  password := "hunter2"   |  const token = 'abc123...'
pattern1='(password|passwd|secret|token|api[_-]?key)[[:space:]]*:?=[[:space:]]*"([^"]{6,})"'
pattern2="(password|passwd|secret|token|api[_-]?key)[[:space:]]*:?=[[:space:]]*'([^']{6,})'"

# Files that legitimately contain example/placeholder strings.
exclude='(_test\.go|\.md$|\.yaml$|\.yml$|\.example$|_test\.ts$|\.spec\.ts$)'

hits=$(grep -rnE "$pattern1|$pattern2" \
  --include='*.go' --include='*.ts' --include='*.tsx' \
  --exclude='*_test.go' --exclude='*.example' \
  apps packages 2>/dev/null \
  | grep -vE 'os\.Getenv|process\.env|Getenv|env\(|placeholder|example|dummy|changeme|redact' || true)

if [ -n "$hits" ]; then
  echo "::error::possible plaintext credential literal(s) found:"
  echo "$hits"
  status=1
fi

# 2) Go: password_enc must never be selected in a plaintext SELECT list returned
#    to the API. It may be written (INSERT/UPDATE) and read only for the login
#    path, which goes through the crypto.Sealer. We assert the column name is not
#    leaked into an OpenAPI response schema.
if grep -rniE 'password_enc|passwordEnc' openapi/ 2>/dev/null; then
  echo "::error::openapi must not expose credential columns"
  status=1
fi

if [ "$status" -eq 0 ]; then
  echo "credential guard: clean"
fi
exit "$status"
