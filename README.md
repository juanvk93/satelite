# ORBITAL — Real-Time Satellite Tracker

Aplicación web de tracking de satélites en tiempo real, compilada a
WebAssembly y servible como contenido 100 % estático (GitHub Pages, S3,
Netlify, IPFS, lo que sea). Sin backend, sin dependencias en runtime más
allá de Leaflet (cargado desde CDN).

- **Algoritmo**: SGP4 implementado desde cero en Go siguiendo Hoots &
  Roehrich (Spacetrack Report #3, 1980) y Vallado et al. (AIAA 2006-6753).
- **Marcos de referencia**: TEME → ECEF → geodésicas WGS-84; rotación
  por GMST IAU 1982.
- **Datos**: TLE de CelesTrak — grupo *stations* (ISS, Tiangong, CSS, etc.).
- **UI**: estética "consola de control de misión", paleta phosphor amber.

## Estructura

```
.
├── main.go                 # entry-point WASM, expone API a JS
├── go.mod
├── sgp4/
│   ├── constants.go        # constantes físicas WGS-72
│   └── sgp4.go             # propagador near-Earth
├── tle/
│   └── tle.go              # parser TLE de dos líneas
├── satellite/
│   └── satellite.go        # high-level: TEME→ECEF→geodésicas, az/el, pasos
├── docs/                   # ← GitHub Pages sirve esta carpeta
│   ├── index.html
│   ├── styles.css
│   ├── main.js             # bridge JS ↔ WASM, render Leaflet
│   ├── main.wasm           # (generado por build.sh)
│   └── wasm_exec.js        # (copiado de GOROOT por build.sh)
└── scripts/
    ├── build.sh            # compila WASM y copia wasm_exec.js
    └── serve.sh            # servidor local en :8080 con MIME wasm
```

## Compilación

Requisitos: **Go ≥ 1.21**.

```bash
git clone https://github.com/yourusername/satellite-tracker
cd satellite-tracker
./scripts/build.sh
```

El script ejecuta básicamente:

```bash
GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o docs/main.wasm .
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" docs/
```

> En Go < 1.24, `wasm_exec.js` está en `$(go env GOROOT)/misc/wasm/`.
> El script detecta ambas ubicaciones automáticamente.

El binario resultante (`docs/main.wasm`) pesa ~1.6 MB; con `gzip` (que
GitHub Pages aplica por defecto a respuestas estáticas) baja a ~400 KB.

## Prueba local

```bash
./scripts/serve.sh           # http://localhost:8080
# o si lo prefieres:
cd docs && python3 -m http.server 8080
```

> ⚠ No abrir `index.html` con `file://`: los navegadores no permiten
> instanciar `.wasm` desde el esquema `file`. Siempre por HTTP.

## Despliegue a GitHub Pages

1. Hacer push del repo a GitHub.
2. Settings → Pages.
3. **Source**: *Deploy from a branch*.
4. **Branch**: `main` · **Folder**: `/docs`.
5. Esperar 1 minuto. La app estará en `https://<usuario>.github.io/<repo>/`.

Como el binario `main.wasm` y `wasm_exec.js` se commitean dentro de
`/docs`, no hace falta CI: cada push refresca la web.

## API JavaScript expuesta por el WASM

El WASM registra `window.SatTracker` con cuatro funciones:

| Función | Argumentos | Retorno |
|---|---|---|
| `loadTLE(text)` | string con TLEs concatenados | `{ok, count, warnings?, error?}` |
| `listSats()` | — | `string[]` |
| `computeAll(timestampMs)` | epoch ms (UTC) | `[{name, lat, lon, altKm, speedKmS, periodMin, footprintKm, error?}]` |
| `predictPasses(name, lat, lon, altKm, fromMs, hours, minElDeg)` | observador en grados/km | `{passes:[{startMs, peakMs, endMs, maxElDeg, durationSec}], error?}` |

## Decisiones y compromisos

- **SGP4 puro, sin SDP4.** SGP4 cubre LEO (período < 225 min); para
  satélites geoestacionarios, GPS o lunisolar perturbados se necesita
  SDP4. El grupo *stations* de CelesTrak es todo LEO, así que SGP4
  basta. Si quisieras extender el catálogo a *gps-ops* o *geo*, habría
  que añadir SDP4 (Hoots §7).
- **WGS-72 dentro de SGP4, WGS-84 fuera.** SGP4 fue calibrado contra
  WGS-72; cambiar de constantes empeora la precisión (Vallado 2006).
  Para el último paso ECEF → geodésica se usa WGS-84 porque es el
  estándar de coordenadas de mapas.
- **Sin librerías Go externas.** Cero dependencias en `go.mod`.
- **CORS.** El fetch de TLE se hace desde JS porque CelesTrak añade
  `Access-Control-Allow-Origin: *`. Hacerlo desde Go con
  `net/http` por syscall/js daría problemas en navegadores con
  políticas estrictas.
- **Web Worker.** No se usa: la propagación de ~10 satélites cada
  segundo cuesta ~3 ms en WASM, despreciable. La predicción de 24 h
  cuesta ~200 ms y se envuelve en `setTimeout(0)` para no congelar el
  primer frame.

## Precisión esperada

SGP4 reproduce las efemérides "two-line" de NORAD a unos pocos km en LEO
para épocas < 1 semana. Para tracking visual y predicción de pasos basta.
Para apuntamiento de antena (azimuth a < 0.1°) hay que actualizar el TLE
diariamente.

## Licencia

MIT.
