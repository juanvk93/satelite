#!/usr/bin/env bash
# Sirve /docs en localhost:8080 con el MIME type correcto para .wasm.
# Uso:
#   ./scripts/serve.sh [puerto]
set -euo pipefail
cd "$(dirname "$0")/../docs"
PORT="${1:-8080}"

if command -v python3 >/dev/null; then
    echo "→ Sirviendo en http://localhost:${PORT}"
    # Python 3 sirve .wasm como application/wasm desde 3.9+
    exec python3 -m http.server "$PORT"
elif command -v go >/dev/null; then
    cat > /tmp/sat-serve.go <<'EOF'
package main
import ("log"; "net/http"; "os"; "strings")
func main() {
    port := "8080"
    if len(os.Args) > 1 { port = os.Args[1] }
    fs := http.FileServer(http.Dir("."))
    h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if strings.HasSuffix(r.URL.Path, ".wasm") {
            w.Header().Set("Content-Type", "application/wasm")
        }
        fs.ServeHTTP(w, r)
    })
    log.Printf("serving on :%s", port)
    log.Fatal(http.ListenAndServe(":"+port, h))
}
EOF
    exec go run /tmp/sat-serve.go "$PORT"
else
    echo "ERROR: ni python3 ni go disponibles" >&2
    exit 1
fi
