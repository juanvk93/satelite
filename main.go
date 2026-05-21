// main.go es el punto de entrada del binario WebAssembly.
// Expone tres funciones globales a JavaScript:
//
//	window.SatTracker.loadTLE(text)
//	   -> {ok, count, error?}: parsea TLE y mantiene los satélites en memoria.
//
//	window.SatTracker.computeAll(timestampMs)
//	   -> [{name, lat, lon, altKm, speedKmS, periodMin, footprintKm}, ...]
//
//	window.SatTracker.predictPasses(satName, lat, lon, altKm, fromMs, hours, minElDeg)
//	   -> {passes: [{startMs, peakMs, endMs, maxElDeg, durationSec}], error?}
//
// La interfaz usa JSON-stringificable (números, strings, arrays, objetos JS)
// porque syscall/js no soporta paso directo de structs Go. La conversión
// se hace explícitamente en cada función.
//
//go:build js && wasm

package main

import (
	"fmt"
	"sort"
	"syscall/js"
	"time"

	"github.com/yourusername/satellite-tracker/satellite"
	"github.com/yourusername/satellite-tracker/tle"
)

// Estado global del módulo: lista de satélites cargados, indexada por nombre.
var (
	sats   = make(map[string]*satellite.Sat)
	sorted []string // orden estable de inserción
)

func main() {
	// Registra el namespace SatTracker en window.
	api := js.Global().Get("Object").New()
	api.Set("loadTLE", js.FuncOf(loadTLE))
	api.Set("computeAll", js.FuncOf(computeAll))
	api.Set("predictPasses", js.FuncOf(predictPasses))
	api.Set("listSats", js.FuncOf(listSats))
	js.Global().Set("SatTracker", api)

	// Notifica al frontend que el WASM está listo.
	if cb := js.Global().Get("onWasmReady"); !cb.IsUndefined() {
		cb.Invoke()
	}

	// Bloquea para mantener vivo el runtime de Go.
	select {}
}

// loadTLE(text string) -> {ok: bool, count: int, error?: string}
func loadTLE(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return jsErr("falta argumento: texto TLE")
	}
	text := args[0].String()
	tles, err := tle.Parse(text)
	if err != nil {
		return jsErr(err.Error())
	}
	// Reset del catálogo. (Si quisiéramos acumular, sería un merge por SatNum.)
	sats = make(map[string]*satellite.Sat)
	sorted = sorted[:0]
	var loadErrors []string
	for _, t := range tles {
		s, err := satellite.New(t)
		if err != nil {
			loadErrors = append(loadErrors, fmt.Sprintf("%s: %v", t.Name, err))
			continue
		}
		name := t.Name
		if name == "" {
			name = fmt.Sprintf("NORAD %d", t.SatNum)
		}
		if _, dup := sats[name]; dup {
			// En caso de duplicado, suffix con el número NORAD.
			name = fmt.Sprintf("%s (%d)", name, t.SatNum)
		}
		sats[name] = s
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	res := js.Global().Get("Object").New()
	res.Set("ok", true)
	res.Set("count", len(sats))
	if len(loadErrors) > 0 {
		res.Set("warnings", strSliceToJS(loadErrors))
	}
	return res
}

// listSats() -> [name, ...]
func listSats(this js.Value, args []js.Value) any {
	return strSliceToJS(sorted)
}

// computeAll(timestampMs float64) -> [{name, lat, lon, altKm, speedKmS, periodMin, footprintKm}]
func computeAll(this js.Value, args []js.Value) any {
	if len(args) < 1 {
		return jsErr("falta argumento: timestamp ms")
	}
	ts := args[0].Float()
	t := time.UnixMilli(int64(ts)).UTC()
	out := js.Global().Get("Array").New(0)
	for _, name := range sorted {
		s := sats[name]
		st, err := s.Compute(t)
		obj := js.Global().Get("Object").New()
		obj.Set("name", name)
		if err != nil {
			obj.Set("error", err.Error())
			out.Call("push", obj)
			continue
		}
		obj.Set("lat", st.LatDeg)
		obj.Set("lon", st.LonDeg)
		obj.Set("altKm", st.AltKm)
		obj.Set("speedKmS", st.SpeedKmS)
		obj.Set("periodMin", st.PeriodMin)
		obj.Set("footprintKm", st.FootprintKm)
		out.Call("push", obj)
	}
	return out
}

// predictPasses(satName, lat, lon, altKm, fromMs, hours, minElDeg)
// -> {passes: [...], error?}
func predictPasses(this js.Value, args []js.Value) any {
	if len(args) < 7 {
		return jsErr("argumentos: name, lat, lon, altKm, fromMs, hours, minElDeg")
	}
	name := args[0].String()
	lat := args[1].Float()
	lon := args[2].Float()
	alt := args[3].Float()
	from := time.UnixMilli(int64(args[4].Float())).UTC()
	hours := args[5].Float()
	minEl := args[6].Float()

	s, ok := sats[name]
	if !ok {
		return jsErr(fmt.Sprintf("satélite no encontrado: %q", name))
	}
	passes, err := s.PredictPasses(lat, lon, alt, from, hours, minEl)
	if err != nil {
		return jsErr(err.Error())
	}
	arr := js.Global().Get("Array").New(0)
	for _, p := range passes {
		o := js.Global().Get("Object").New()
		o.Set("startMs", float64(p.Start.UnixMilli()))
		o.Set("peakMs", float64(p.Peak.UnixMilli()))
		o.Set("endMs", float64(p.End.UnixMilli()))
		o.Set("maxElDeg", p.MaxElDeg)
		o.Set("durationSec", p.DurationSec)
		arr.Call("push", o)
	}
	res := js.Global().Get("Object").New()
	res.Set("passes", arr)
	return res
}

// ------------- helpers -------------

func jsErr(msg string) js.Value {
	o := js.Global().Get("Object").New()
	o.Set("ok", false)
	o.Set("error", msg)
	return o
}

func strSliceToJS(in []string) js.Value {
	arr := js.Global().Get("Array").New(len(in))
	for i, s := range in {
		arr.SetIndex(i, s)
	}
	return arr
}
