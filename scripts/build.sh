#!/usr/bin/env bash
# Compila el proyecto Go a WebAssembly y lo coloca en /docs.
# Requiere: Go >= 1.21
#
# Uso:
#   ./scripts/build.sh
#
# Salida:
#   docs/main.wasm        (binario)
#   docs/wasm_exec.js     (runtime de Go, copiado de $(go env GOROOT))

set -euo pipefail

cd "$(dirname "$0")/.."

ROOT="$(pwd)"
OUT_DIR="${ROOT}/docs"
GOROOT="$(go env GOROOT)"

echo "→ Compilando WASM..."
GOOS=js GOARCH=wasm go build \
    -ldflags="-s -w" \
    -trimpath \
    -o "${OUT_DIR}/main.wasm" \
    .

echo "→ Copiando wasm_exec.js desde GOROOT=${GOROOT}"
# A partir de Go 1.21+ se mueve a misc/wasm; en versiones antiguas estaba en misc/wasm también.
WASM_EXEC_SRC=""
for p in \
    "${GOROOT}/lib/wasm/wasm_exec.js" \
    "${GOROOT}/misc/wasm/wasm_exec.js"; do
    if [[ -f "$p" ]]; then
        WASM_EXEC_SRC="$p"
        break
    fi
done
if [[ -z "$WASM_EXEC_SRC" ]]; then
    echo "ERROR: no se encuentra wasm_exec.js en GOROOT" >&2
    exit 1
fi
cp "$WASM_EXEC_SRC" "${OUT_DIR}/wasm_exec.js"

SIZE_KB=$(du -k "${OUT_DIR}/main.wasm" | cut -f1)
echo "✓ Build completo: ${OUT_DIR}/main.wasm (${SIZE_KB} KB)"
echo "✓ wasm_exec.js   : ${OUT_DIR}/wasm_exec.js"
