/* =====================================================================
 * ORBITAL - main.js
 *
 * - Carga el binario WASM (main.wasm) usando wasm_exec.js
 * - Hace fetch de TLE desde CelesTrak (en JS para resolver CORS desde
 *   el navegador, ya que CelesTrak añade la cabecera apropiada)
 * - Pasa el texto TLE crudo al WASM via window.SatTracker.loadTLE
 * - Cada 1 s pide la posición de todos los satélites y refresca el mapa
 * - Maneja selección de satélite, predicción de pasos y geolocalización
 * ===================================================================== */

(() => {
  "use strict";

  // ---------- estado global -----------
  const state = {
    wasmReady: false,
    selected: null,         // nombre del satélite seleccionado
    sats: [],               // últimas posiciones [{name, lat, lon, ...}]
    markers: new Map(),     // name -> L.marker
    labels: new Map(),      // name -> L.divIcon adicional (oculto si zoom bajo)
    footprint: null,        // L.circle del satélite seleccionado
    obsMarker: null,        // L.marker del observador
    tickIntervalId: null,
    lastTickAt: 0,
  };

  // ---------- DOM refs ----------
  const $ = (id) => document.getElementById(id);
  const els = {
    boot: $("boot"), bootGo: $("bootGo"), bootTLE: $("bootTLE"), bootBar: $("bootBar"),
    utcClock: $("utcClock"),
    satCount: $("satCount"),
    tickMs: $("tickMs"),
    statusLed: $("statusLed"),
    satList: $("satList"),
    search: $("search"),
    reloadTLE: $("reloadTLE"),
    obsLat: $("obsLat"), obsLon: $("obsLon"), obsAlt: $("obsAlt"), minEl: $("minEl"),
    useGeo: $("useGeo"),
    predict: $("predict"),
    tgtName: $("tgtName"), tgtId: $("tgtId"),
    rdLat: $("rdLat"), rdLon: $("rdLon"), rdAlt: $("rdAlt"),
    rdVel: $("rdVel"), rdPer: $("rdPer"), rdFp: $("rdFp"),
    passes: $("passes"), passInfo: $("passInfo"),
    lastTleUpdate: $("lastTleUpdate"),
  };

  // ---------- TLE source ----------
  // CelesTrak group "stations" incluye ISS, Tiangong, CSS, y otras estaciones.
  const TLE_URL = "https://celestrak.org/NORAD/elements/gp.php?GROUP=stations&FORMAT=tle";

  // ===== utilidades de formato =====
  const fmt = (n, d=2) => (n==null||isNaN(n)) ? "—" : Number(n).toFixed(d);
  const pad = (n) => String(n).padStart(2,"0");
  const fmtUtc = (d) => `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}`;
  const fmtDateUtc = (d) => `${d.getUTCFullYear()}-${pad(d.getUTCMonth()+1)}-${pad(d.getUTCDate())} ${fmtUtc(d)}Z`;
  const setStatus = (txt, level="ok") => {
    els.statusLed.textContent = txt;
    els.statusLed.className = "telem__v telem__v--" + level;
  };

  // ============================================================
  // 1. Reloj UTC permanente (independiente del estado WASM)
  // ============================================================
  setInterval(() => { els.utcClock.textContent = fmtUtc(new Date()); }, 1000);
  els.utcClock.textContent = fmtUtc(new Date());

  // ============================================================
  // 2. Mapa Leaflet (proyección equirectangular - tiles claros invertidos
  //    no se usan; usamos el tile "CartoDB DarkMatter" que casa con la
  //    estética y es gratuito).
  // ============================================================
  const map = L.map("map", {
    center: [20, 0],
    zoom: 2,
    minZoom: 2,
    maxZoom: 6,
    worldCopyJump: true, // copia satélites al cruzar el antimeridiano
    zoomControl: true,
    attributionControl: true,
  });
  L.tileLayer("https://{s}.basemaps.cartocdn.com/dark_nolabels/{z}/{x}/{y}{r}.png", {
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OSM</a> &copy; <a href="https://carto.com/attributions">CARTO</a>',
    subdomains: "abcd",
    maxZoom: 7,
  }).addTo(map);

  // ============================================================
  // 3. WASM bootstrap
  // ============================================================
  function setBootLine(elem, ok, text) {
    elem.textContent = text;
    elem.style.color = ok ? "#6cf0a6" : "#ff5e6c";
  }
  function setBootProgress(pct) {
    els.bootBar.style.width = Math.min(100, Math.max(0, pct)) + "%";
  }

  // El hook lo llama main.go cuando termina su init.
  window.onWasmReady = function () {
    state.wasmReady = true;
    setBootLine(els.bootGo, true, "[ DONE ]");
    setBootProgress(50);
    // Cargamos TLE inmediatamente.
    loadTLEFromCelesTrak().catch(err => {
      console.error(err);
      setBootLine(els.bootTLE, false, "[ FAIL ]");
      setStatus("TLE_ERR", "err");
    });
  };

  async function bootWASM() {
    if (typeof Go !== "function") {
      throw new Error("wasm_exec.js no cargado");
    }
    const go = new Go();
    setBootProgress(15);
    try {
      // streaming si está disponible (mejor: empieza a compilar mientras descarga)
      const wasmResp = await fetch("main.wasm");
      if (!wasmResp.ok) throw new Error("HTTP " + wasmResp.status);
      setBootProgress(35);
      const result = WebAssembly.instantiateStreaming
        ? await WebAssembly.instantiateStreaming(wasmResp, go.importObject)
        : await WebAssembly.instantiate(await wasmResp.arrayBuffer(), go.importObject);
      // Lanza go.run sin esperarlo (el runtime de Go se queda vivo en select{})
      go.run(result.instance);
    } catch (e) {
      setBootLine(els.bootGo, false, "[ FAIL ]");
      throw e;
    }
  }

  // ============================================================
  // 4. Fetch TLE desde CelesTrak y pasarlo al WASM
  //    (hacerlo desde JS evita problemas de CORS en Go syscall/js)
  // ============================================================
  async function loadTLEFromCelesTrak() {
    setStatus("FETCH_TLE", "warn");
    const resp = await fetch(TLE_URL, { cache: "no-store" });
    if (!resp.ok) throw new Error("CelesTrak HTTP " + resp.status);
    const text = await resp.text();
    setBootProgress(75);
    const result = window.SatTracker.loadTLE(text);
    if (!result.ok) throw new Error("loadTLE: " + result.error);
    setBootLine(els.bootTLE, true, `[ ${result.count} sats ]`);
    setBootProgress(100);
    els.satCount.textContent = result.count;
    els.lastTleUpdate.textContent = "TLE actualizado " + fmtDateUtc(new Date());
    renderSatList();
    startTicker();
    setStatus("TRACKING", "ok");
    setTimeout(() => els.boot.classList.add("is-hidden"), 350);
    setTimeout(() => els.boot.remove(), 1200);
  }

  // ============================================================
  // 5. Render lista de satélites (catálogo)
  // ============================================================
  function renderSatList() {
    const names = window.SatTracker.listSats();
    els.satList.innerHTML = "";
    const filter = els.search.value.trim().toLowerCase();
    for (const name of names) {
      if (filter && !name.toLowerCase().includes(filter)) continue;
      const li = document.createElement("li");
      li.className = "sat-item" + (name === state.selected ? " is-active" : "");
      li.innerHTML = `
        <span class="sat-item__dot"></span>
        <span class="sat-item__name">${escapeHtml(name)}</span>
      `;
      li.addEventListener("click", () => selectSat(name));
      els.satList.appendChild(li);
    }
  }
  els.search.addEventListener("input", renderSatList);

  // ============================================================
  // 6. Selección de satélite
  // ============================================================
  function selectSat(name) {
    state.selected = name;
    renderSatList();
    // Actualiza marcadores ya pintados.
    for (const [n, m] of state.markers) {
      const el = m.getElement();
      if (!el) continue;
      const div = el.querySelector(".sat-marker");
      if (div) div.classList.toggle("is-selected", n === name);
    }
    // Centra el mapa en el satélite.
    const sat = state.sats.find(s => s.name === name);
    if (sat) map.panTo([sat.lat, sat.lon], { animate: true });
    els.tgtName.textContent = name;
    els.tgtId.textContent = "selecciona PREDICT para próximos pasos";
  }

  // ============================================================
  // 7. Ticker: cada segundo recalcula posiciones
  // ============================================================
  function startTicker() {
    if (state.tickIntervalId) clearInterval(state.tickIntervalId);
    tick();
    state.tickIntervalId = setInterval(tick, 1000);
  }

  function tick() {
    if (!state.wasmReady) return;
    const t0 = performance.now();
    const now = Date.now();
    let sats;
    try {
      sats = window.SatTracker.computeAll(now);
    } catch (e) {
      console.error(e);
      setStatus("WASM_ERR", "err");
      return;
    }
    // Convertimos el array JS-Array en array nativo (los proxies pueden ser lentos).
    const arr = [];
    const len = sats.length;
    for (let i = 0; i < len; i++) {
      const o = sats[i];
      arr.push({
        name: o.name,
        lat: o.lat,
        lon: o.lon,
        altKm: o.altKm,
        speedKmS: o.speedKmS,
        periodMin: o.periodMin,
        footprintKm: o.footprintKm,
        error: o.error,
      });
    }
    state.sats = arr;
    refreshMarkers(arr);
    refreshReadout(arr);
    const elapsed = performance.now() - t0;
    els.tickMs.textContent = elapsed.toFixed(0);
    state.lastTickAt = now;
  }

  // ============================================================
  // 8. Marcadores
  // ============================================================
  function makeSatIcon(selected) {
    return L.divIcon({
      className: "",
      iconSize: [0, 0],
      html: `<div class="sat-marker${selected ? " is-selected" : ""}"></div>`,
    });
  }
  function makeLabel(name) {
    return L.divIcon({
      className: "",
      iconSize: [0, 0],
      html: `<div class="sat-label">${escapeHtml(name)}</div>`,
    });
  }

  function refreshMarkers(arr) {
    const seen = new Set();
    for (const s of arr) {
      if (s.error) continue;
      seen.add(s.name);
      let m = state.markers.get(s.name);
      if (!m) {
        m = L.marker([s.lat, s.lon], {
          icon: makeSatIcon(s.name === state.selected),
          keyboard: false,
          riseOnHover: true,
        }).addTo(map);
        m.on("click", () => selectSat(s.name));
        state.markers.set(s.name, m);

        // Etiqueta sólo si el mapa está suficientemente zoomado.
        const label = L.marker([s.lat, s.lon], {
          icon: makeLabel(s.name),
          interactive: false,
          keyboard: false,
        });
        state.labels.set(s.name, label);
        if (map.getZoom() >= 4) label.addTo(map);
      } else {
        m.setLatLng([s.lat, s.lon]);
        const lab = state.labels.get(s.name);
        if (lab) lab.setLatLng([s.lat, s.lon]);
      }
    }
    // Eliminar marcadores que ya no existen
    for (const [name, m] of state.markers) {
      if (!seen.has(name)) {
        map.removeLayer(m);
        state.markers.delete(name);
        const lab = state.labels.get(name);
        if (lab) { map.removeLayer(lab); state.labels.delete(name); }
      }
    }
    // Footprint del satélite seleccionado.
    const sel = arr.find(x => x.name === state.selected);
    if (sel && !sel.error) {
      if (!state.footprint) {
        state.footprint = L.circle([sel.lat, sel.lon], {
          radius: sel.footprintKm * 1000,
          color: "#6cf0a6",
          weight: 1,
          opacity: 0.7,
          fillColor: "#6cf0a6",
          fillOpacity: 0.06,
          interactive: false,
        }).addTo(map);
      } else {
        state.footprint.setLatLng([sel.lat, sel.lon]);
        state.footprint.setRadius(sel.footprintKm * 1000);
      }
    } else if (state.footprint) {
      map.removeLayer(state.footprint);
      state.footprint = null;
    }
  }

  // Mostrar/ocultar etiquetas según zoom
  map.on("zoomend", () => {
    const show = map.getZoom() >= 4;
    for (const [name, lab] of state.labels) {
      if (show && !map.hasLayer(lab)) lab.addTo(map);
      else if (!show && map.hasLayer(lab)) map.removeLayer(lab);
    }
  });

  // ============================================================
  // 9. Panel readout
  // ============================================================
  function refreshReadout(arr) {
    if (!state.selected) return;
    const s = arr.find(x => x.name === state.selected);
    if (!s) return;
    if (s.error) {
      els.tgtId.textContent = "ERROR: " + s.error;
      return;
    }
    els.rdLat.textContent = fmt(s.lat, 4);
    els.rdLon.textContent = fmt(s.lon, 4);
    els.rdAlt.textContent = fmt(s.altKm, 1);
    els.rdVel.textContent = fmt(s.speedKmS, 3);
    els.rdPer.textContent = fmt(s.periodMin, 2);
    els.rdFp.textContent  = fmt(s.footprintKm, 0);
  }

  // ============================================================
  // 10. Observer + predicción de pasos
  // ============================================================
  function updateObserver() {
    const lat = parseFloat(els.obsLat.value);
    const lon = parseFloat(els.obsLon.value);
    if (isNaN(lat) || isNaN(lon)) return null;
    if (!state.obsMarker) {
      state.obsMarker = L.marker([lat, lon], {
        icon: L.divIcon({ className: "", iconSize: [0,0], html: '<div class="obs-marker"></div>' }),
        interactive: false,
      }).addTo(map);
    } else {
      state.obsMarker.setLatLng([lat, lon]);
    }
    return { lat, lon, alt: parseFloat(els.obsAlt.value) || 0 };
  }
  els.obsLat.addEventListener("change", updateObserver);
  els.obsLon.addEventListener("change", updateObserver);
  els.obsAlt.addEventListener("change", updateObserver);

  els.useGeo.addEventListener("click", () => {
    if (!navigator.geolocation) return alert("Geolocalización no disponible");
    navigator.geolocation.getCurrentPosition(
      pos => {
        els.obsLat.value = pos.coords.latitude.toFixed(4);
        els.obsLon.value = pos.coords.longitude.toFixed(4);
        els.obsAlt.value = ((pos.coords.altitude || 0) / 1000).toFixed(3);
        updateObserver();
      },
      err => alert("No se pudo obtener la ubicación: " + err.message)
    );
  });

  els.predict.addEventListener("click", () => {
    if (!state.selected) {
      alert("Selecciona un satélite primero (lista de la izquierda).");
      return;
    }
    const obs = updateObserver();
    if (!obs) return;
    const minEl = parseFloat(els.minEl.value) || 0;
    setStatus("PREDICT", "warn");
    // computeAll bloquea ~10 ms; predictPasses puede tardar 200-500 ms por sat.
    // Lo metemos en un setTimeout(0) para no congelar la UI.
    setTimeout(() => {
      const t0 = performance.now();
      const res = window.SatTracker.predictPasses(
        state.selected, obs.lat, obs.lon, obs.alt,
        Date.now(), 24.0, minEl
      );
      const ms = performance.now() - t0;
      if (!res || res.error) {
        els.passes.innerHTML = `<div class="passes__empty">Error: ${escapeHtml(res?.error||"")}</div>`;
        setStatus("TRACKING", "ok");
        return;
      }
      renderPasses(res.passes, ms);
      setStatus("TRACKING", "ok");
    }, 10);
  });

  function renderPasses(passes, calcMs) {
    const len = passes.length;
    els.passInfo.textContent = `(${len} en 24 h · ${calcMs.toFixed(0)} ms)`;
    if (len === 0) {
      els.passes.innerHTML = `<div class="passes__empty">sin pasos visibles en las próximas 24 h</div>`;
      return;
    }
    let html = "";
    for (let i = 0; i < len; i++) {
      const p = passes[i];
      const start = new Date(p.startMs);
      const dur = Math.round(p.durationSec);
      html += `<div class="pass">
        <div class="pass__when">${pad(start.getUTCHours())}:${pad(start.getUTCMinutes())}<small>:${pad(start.getUTCSeconds())} UTC</small></div>
        <div class="pass__meta">
          ${start.getUTCFullYear()}-${pad(start.getUTCMonth()+1)}-${pad(start.getUTCDate())}<br>
          duración ${Math.floor(dur/60)}m ${dur%60}s
        </div>
        <div class="pass__el">${p.maxElDeg.toFixed(0)}°</div>
      </div>`;
    }
    els.passes.innerHTML = html;
  }

  // ============================================================
  // 11. Reload TLE
  // ============================================================
  els.reloadTLE.addEventListener("click", async () => {
    setStatus("FETCH_TLE", "warn");
    try {
      await loadTLEFromCelesTrak();
    } catch (e) {
      console.error(e);
      setStatus("TLE_ERR", "err");
      alert("Error recargando TLE: " + e.message);
    }
  });

  // ============================================================
  // helpers
  // ============================================================
  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, c => ({
      "&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"
    }[c]));
  }

  // -------------- arranque ----------------
  updateObserver();
  bootWASM().catch(err => {
    console.error("Boot WASM error:", err);
    setBootLine(els.bootGo, false, "[ FAIL: " + err.message + " ]");
    setStatus("WASM_ERR", "err");
  });

})();
