// Package tle parsea ficheros TLE (Two-Line Element set) en el formato
// estándar de NORAD/NASA. Un TLE describe la órbita de un objeto en torno
// a la Tierra mediante un conjunto reducido de elementos keplerianos
// "medios" (mean elements), adecuados para el propagador SGP4.
//
// Formato canónico (3 líneas, la primera es opcional con el nombre):
//
//	ISS (ZARYA)
//	1 25544U 98067A   24001.50000000  .00012345  00000-0  22334-3 0  9990
//	2 25544  51.6400 123.4567 0001234  12.3456 347.8901 15.50000000 12345
//
// Cada línea tiene 69 caracteres exactos (incluyendo el dígito de checksum
// modulo-10 al final). Los campos están en posiciones fijas: el formato
// original viene de tarjetas perforadas, lo que explica las columnas rígidas.
package tle

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// TLE representa un conjunto de elementos orbitales medios listos para
// alimentar al propagador SGP4. Las unidades siguen el convenio TLE:
//   - Ángulos en grados
//   - Movimiento medio en revoluciones por día
//   - Derivadas en sus unidades respectivas (rev/día², rev/día³)
//   - BSTAR es un coeficiente de arrastre adimensional dividido por 2*Re*ρ0,
//     en realidad un pseudo-coeficiente balístico de SGP4.
type TLE struct {
	Name string // Nombre del satélite (línea 0, opcional)

	// Línea 1
	SatNum         int     // Número de catálogo NORAD (e.g. 25544 para la ISS)
	Classification string  // U=Unclassified, C=Classified, S=Secret
	IntlDesignator string  // Designador internacional COSPAR (e.g. "98067A")
	EpochYear      int     // Año de la época (2 dígitos expandidos a 4)
	EpochDay       float64 // Día del año (fraccional, base 1.0 = 1 enero 00:00 UTC)
	MeanMotionDot  float64 // ṅ/2: primera derivada del movimiento medio (rev/día²)
	MeanMotionDDot float64 // n̈/6: segunda derivada (rev/día³); casi siempre 0
	BStar          float64 // Coef. de arrastre BSTAR (1/EarthRadii); puede ser negativo
	Ephemeris      int     // Tipo de efemérides (0 normalmente)
	ElementSetNum  int     // Número de set de elementos

	// Línea 2
	Inclination      float64 // Inclinación i (grados)
	RAAN             float64 // Ascensión recta del nodo ascendente Ω (grados)
	Eccentricity     float64 // Excentricidad e (adimensional, 0 ≤ e < 1)
	ArgPerigee       float64 // Argumento del perigeo ω (grados)
	MeanAnomaly      float64 // Anomalía media M (grados)
	MeanMotion       float64 // Movimiento medio n (rev/día)
	RevolutionNumber int     // Nº de revolución en la época (módulo 100000)
}

// Parse interpreta un texto que contiene uno o más TLEs (formato 2 o 3 líneas).
// Devuelve un slice con todos los TLE encontrados; descarta líneas en blanco
// y los TLE con error individual (devolviendo el primer error).
func Parse(data string) ([]TLE, error) {
	var (
		tles  []TLE
		buf   [3]string
		count int
	)
	sc := bufio.NewScanner(strings.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \r\n\t")
		if line == "" {
			continue
		}
		// Detectamos la línea por su primer carácter:
		//   "1 " inicio de línea 1
		//   "2 " inicio de línea 2
		//   cualquier otra cosa = nombre
		switch {
		case strings.HasPrefix(line, "1 ") && len(line) >= 69:
			buf[1] = line
			count = 2
		case strings.HasPrefix(line, "2 ") && len(line) >= 69 && count == 2:
			buf[2] = line
			t, err := parseTLE(buf[0], buf[1], buf[2])
			if err != nil {
				return tles, fmt.Errorf("TLE %q: %w", buf[0], err)
			}
			tles = append(tles, t)
			buf = [3]string{}
			count = 0
		default:
			buf[0] = line
			count = 1
		}
	}
	if err := sc.Err(); err != nil {
		return tles, err
	}
	if len(tles) == 0 {
		return nil, fmt.Errorf("no se encontraron TLEs válidos")
	}
	return tles, nil
}

// parseTLE convierte tres líneas crudas (nombre + L1 + L2) en una estructura TLE.
// Las posiciones de cada campo están definidas por el documento Spacetrack
// Report #3 (Hoots & Roehrich, 1980).
func parseTLE(name, l1, l2 string) (TLE, error) {
	if len(l1) < 69 || len(l2) < 69 {
		return TLE{}, fmt.Errorf("líneas demasiado cortas: %d, %d", len(l1), len(l2))
	}

	t := TLE{Name: strings.TrimSpace(name)}
	var err error

	// --- Línea 1 ---
	if t.SatNum, err = atoiTrim(l1[2:7]); err != nil {
		return t, fmt.Errorf("SatNum: %w", err)
	}
	t.Classification = string(l1[7])
	t.IntlDesignator = strings.TrimSpace(l1[9:17])

	// Año de época: 2 dígitos. Convención NORAD: >=57 → 19xx, <57 → 20xx.
	// (1957 = lanzamiento del Sputnik, ningún satélite es anterior.)
	yy, err := atoiTrim(l1[18:20])
	if err != nil {
		return t, fmt.Errorf("EpochYear: %w", err)
	}
	if yy < 57 {
		t.EpochYear = 2000 + yy
	} else {
		t.EpochYear = 1900 + yy
	}
	if t.EpochDay, err = atofTrim(l1[20:32]); err != nil {
		return t, fmt.Errorf("EpochDay: %w", err)
	}
	if t.MeanMotionDot, err = atofTrim(l1[33:43]); err != nil {
		return t, fmt.Errorf("MeanMotionDot: %w", err)
	}
	if t.MeanMotionDDot, err = parseExpFloat(l1[44:52]); err != nil {
		return t, fmt.Errorf("MeanMotionDDot: %w", err)
	}
	if t.BStar, err = parseExpFloat(l1[53:61]); err != nil {
		return t, fmt.Errorf("BStar: %w", err)
	}
	if t.Ephemeris, err = atoiTrim(l1[62:63]); err != nil {
		t.Ephemeris = 0
	}
	if t.ElementSetNum, err = atoiTrim(l1[64:68]); err != nil {
		t.ElementSetNum = 0
	}

	// --- Línea 2 ---
	if t.Inclination, err = atofTrim(l2[8:16]); err != nil {
		return t, fmt.Errorf("Inclination: %w", err)
	}
	if t.RAAN, err = atofTrim(l2[17:25]); err != nil {
		return t, fmt.Errorf("RAAN: %w", err)
	}
	// Excentricidad: 7 dígitos con punto decimal implícito al principio
	// ("0001234" significa 0.0001234). Es así para ahorrar el carácter del punto.
	ecc, err := atofTrim(l2[26:33])
	if err != nil {
		return t, fmt.Errorf("Eccentricity: %w", err)
	}
	t.Eccentricity = ecc * 1e-7
	if t.ArgPerigee, err = atofTrim(l2[34:42]); err != nil {
		return t, fmt.Errorf("ArgPerigee: %w", err)
	}
	if t.MeanAnomaly, err = atofTrim(l2[43:51]); err != nil {
		return t, fmt.Errorf("MeanAnomaly: %w", err)
	}
	if t.MeanMotion, err = atofTrim(l2[52:63]); err != nil {
		return t, fmt.Errorf("MeanMotion: %w", err)
	}
	if t.RevolutionNumber, err = atoiTrim(l2[63:68]); err != nil {
		t.RevolutionNumber = 0
	}
	return t, nil
}

// parseExpFloat decodifica el formato "asumido decimal" típico de TLE:
// "-12345-3" significa -0.12345 × 10⁻³. Es un formato heredado para
// representar números pequeños sin gastar el carácter de la 'E'.
func parseExpFloat(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" || s == "00000-0" || s == "00000+0" {
		return 0, nil
	}
	// Se separan mantisa y exponente. Mantisa: signo opcional + dígitos.
	// Exponente: signo (+ o -) en la penúltima posición + 1 dígito.
	if len(s) < 2 {
		return 0, fmt.Errorf("formato inesperado: %q", s)
	}
	// El exponente son los dos últimos caracteres: ej. "-3", "+0".
	expStr := s[len(s)-2:]
	mantStr := s[:len(s)-2]

	// La mantisa lleva un punto decimal implícito al inicio.
	// "-12345" → "-0.12345"
	var sign string
	switch mantStr[0] {
	case '-':
		sign = "-"
		mantStr = mantStr[1:]
	case '+':
		mantStr = mantStr[1:]
	}
	mant, err := strconv.ParseFloat(sign+"0."+mantStr, 64)
	if err != nil {
		return 0, fmt.Errorf("mantisa %q: %w", mantStr, err)
	}
	exp, err := strconv.Atoi(strings.TrimPrefix(expStr, "+"))
	if err != nil {
		return 0, fmt.Errorf("exponente %q: %w", expStr, err)
	}
	return mant * powTen(exp), nil
}

func powTen(n int) float64 {
	r := 1.0
	if n >= 0 {
		for i := 0; i < n; i++ {
			r *= 10
		}
	} else {
		for i := 0; i < -n; i++ {
			r /= 10
		}
	}
	return r
}

func atoiTrim(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}
func atofTrim(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}
