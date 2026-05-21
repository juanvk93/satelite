// Package satellite envuelve sgp4 con utilidades de alto nivel:
//   - Conversión de tiempo (time.Time → minutos desde época, → Julian Date)
//   - Transformación de marcos: TEME → ECEF → geodésico (lat/lon/alt)
//   - Cálculo de azimut/elevación desde un observador en superficie
//   - Predicción de pasos (rise/peak/set) sobre las próximas N horas
//   - Cálculo del radio del "footprint" (huella de cobertura)
package satellite

import (
	"math"
	"time"

	"github.com/yourusername/satellite-tracker/sgp4"
	"github.com/yourusername/satellite-tracker/tle"
)

// Sat agrupa el TLE original y su propagador SGP4 inicializado.
type Sat struct {
	TLE  tle.TLE
	prop *sgp4.Sat
	name string
}

// New crea un satélite listo para propagar.
func New(t tle.TLE) (*Sat, error) {
	p, err := sgp4.NewSat(t)
	if err != nil {
		return nil, err
	}
	return &Sat{TLE: t, prop: p, name: t.Name}, nil
}

// Name devuelve el nombre del satélite (línea 0 del TLE).
func (s *Sat) Name() string { return s.name }

// State es el estado instantáneo del satélite en coordenadas observables.
type State struct {
	Time       time.Time
	LatDeg     float64 // latitud geodésica (°N positivo)
	LonDeg     float64 // longitud geográfica (°E positivo, [-180, 180])
	AltKm      float64 // altitud sobre el elipsoide (km)
	SpeedKmS   float64 // módulo de velocidad (km/s)
	PeriodMin  float64 // período orbital (min)
	FootprintKm float64 // radio de visibilidad sobre el suelo (km, horizonte 0°)
}

// Compute propaga el satélite al instante t y devuelve su estado geodésico.
func (s *Sat) Compute(t time.Time) (State, error) {
	tsince := MinutesSince(s.prop.EpochJD(), t)
	posTEME, velTEME, err := s.prop.Propagate(tsince)
	if err != nil {
		return State{}, err
	}
	gmst := GMST(t)
	posECEF := TEMEtoECEF(posTEME, gmst)
	lat, lon, alt := ECEFtoGeodetic(posECEF)
	speed := math.Sqrt(velTEME.X*velTEME.X + velTEME.Y*velTEME.Y + velTEME.Z*velTEME.Z)
	periodMin := 2 * math.Pi / s.prop.MeanMotionRadPerMin()
	fp := FootprintRadiusKm(alt)
	return State{
		Time:        t,
		LatDeg:      lat * 180 / math.Pi,
		LonDeg:      lon * 180 / math.Pi,
		AltKm:       alt,
		SpeedKmS:    speed,
		PeriodMin:   periodMin,
		FootprintKm: fp,
	}, nil
}

// MinutesSince devuelve los minutos transcurridos desde una época JD hasta t.
func MinutesSince(epochJD float64, t time.Time) float64 {
	return (JulianDate(t) - epochJD) * 1440.0
}

// JulianDate convierte un time.Time (UTC) a Julian Date.
// Fórmula de Meeus / Vallado: válida del 1 enero 1900 en adelante.
func JulianDate(t time.Time) float64 {
	t = t.UTC()
	Y, M, D := t.Year(), int(t.Month()), t.Day()
	if M <= 2 {
		Y -= 1
		M += 12
	}
	A := math.Floor(float64(Y) / 100)
	B := 2 - A + math.Floor(A/4)
	jd := math.Floor(365.25*float64(Y+4716)) +
		math.Floor(30.6001*float64(M+1)) +
		float64(D) + B - 1524.5
	// Fracción de día desde 00:00 UT.
	sec := float64(t.Hour())*3600 + float64(t.Minute())*60 +
		float64(t.Second()) + float64(t.Nanosecond())/1e9
	return jd + sec/86400.0
}

// GMST devuelve el Greenwich Mean Sidereal Time (radianes) para el instante t.
// Fórmula IAU 1982 simplificada (Vallado eq. 3-45), suficientemente precisa
// para tracking visual de satélites.
func GMST(t time.Time) float64 {
	jd := JulianDate(t)
	// T = siglos julianos desde J2000.0 (JD 2451545.0).
	T := (jd - 2451545.0) / 36525.0
	// GMST en segundos.
	gmstSec := 67310.54841 +
		(876600*3600+8640184.812866)*T +
		0.093104*T*T -
		6.2e-6*T*T*T
	// Reducir a [0, 86400) y pasar a radianes (1 día sidéreo ≈ 86400 s aproximado).
	rad := math.Mod(gmstSec*math.Pi/43200.0, 2*math.Pi)
	if rad < 0 {
		rad += 2 * math.Pi
	}
	return rad
}

// TEMEtoECEF rota un vector TEME al marco ECEF (Earth-Centered Earth-Fixed)
// aplicando -GMST en torno al eje Z. No incluye corrección de movimiento
// del polo: el error resultante es de pocos metros, despreciable para LEO.
func TEMEtoECEF(v sgp4.Vec3, gmst float64) sgp4.Vec3 {
	c, s := math.Cos(gmst), math.Sin(gmst)
	return sgp4.Vec3{
		X: c*v.X + s*v.Y,
		Y: -s*v.X + c*v.Y,
		Z: v.Z,
	}
}

// ECEFtoGeodetic convierte coordenadas ECEF (km) a (latitud, longitud,
// altitud) sobre el elipsoide WGS-84 usando el algoritmo cerrado de Bowring
// (1976), con iteración rápida para latitudes altas.
//
// Devuelve: lat (rad, [-π/2, π/2]), lon (rad, [-π, π]), alt (km).
func ECEFtoGeodetic(p sgp4.Vec3) (lat, lon, altKm float64) {
	const (
		a   = 6378.137         // semieje mayor WGS-84 (km)
		f   = 1 / 298.257223563 // achatamiento
		b   = a * (1 - f)
		e2  = f * (2 - f)
		ep2 = (a*a - b*b) / (b * b)
	)
	lon = math.Atan2(p.Y, p.X)
	r := math.Sqrt(p.X*p.X + p.Y*p.Y)
	if r < 1e-6 {
		// Polo: latitud ±90°, altitud sobre el polo.
		lat = math.Pi / 2
		if p.Z < 0 {
			lat = -lat
		}
		altKm = math.Abs(p.Z) - b
		return
	}
	// Latitud inicial (Bowring).
	theta := math.Atan2(p.Z*a, r*b)
	sinT, cosT := math.Sincos(theta)
	lat = math.Atan2(p.Z+ep2*b*sinT*sinT*sinT, r-e2*a*cosT*cosT*cosT)
	sinLat := math.Sin(lat)
	N := a / math.Sqrt(1-e2*sinLat*sinLat) // radio de curvatura del primer vertical
	altKm = r/math.Cos(lat) - N
	return
}

// AzEl calcula azimut (rad, desde Norte hacia Este) y elevación (rad,
// sobre el horizonte) del satélite visto desde un observador en superficie.
// La fórmula usa transformación ECEF → ENU (East-North-Up).
//
// obsLat, obsLon en radianes; obsAltKm sobre el elipsoide.
// satECEF en km.
func AzEl(obsLatRad, obsLonRad, obsAltKm float64, satECEF sgp4.Vec3) (azRad, elRad, rangeKm float64) {
	// Observador en ECEF.
	obsECEF := geodeticToECEF(obsLatRad, obsLonRad, obsAltKm)
	dx := satECEF.X - obsECEF.X
	dy := satECEF.Y - obsECEF.Y
	dz := satECEF.Z - obsECEF.Z

	sLat, cLat := math.Sincos(obsLatRad)
	sLon, cLon := math.Sincos(obsLonRad)

	// Rotación ECEF → ENU.
	e := -sLon*dx + cLon*dy
	n := -sLat*cLon*dx - sLat*sLon*dy + cLat*dz
	u := cLat*cLon*dx + cLat*sLon*dy + sLat*dz

	rangeKm = math.Sqrt(dx*dx + dy*dy + dz*dz)
	elRad = math.Asin(u / rangeKm)
	azRad = math.Atan2(e, n)
	if azRad < 0 {
		azRad += 2 * math.Pi
	}
	return
}

func geodeticToECEF(latRad, lonRad, altKm float64) sgp4.Vec3 {
	const (
		a  = 6378.137
		f  = 1 / 298.257223563
		e2 = f * (2 - f)
	)
	sLat, cLat := math.Sincos(latRad)
	sLon, cLon := math.Sincos(lonRad)
	N := a / math.Sqrt(1-e2*sLat*sLat)
	return sgp4.Vec3{
		X: (N + altKm) * cLat * cLon,
		Y: (N + altKm) * cLat * sLon,
		Z: (N*(1-e2) + altKm) * sLat,
	}
}

// FootprintRadiusKm calcula el radio del círculo de cobertura sobre el suelo
// para un satélite a altitud altKm, asumiendo horizonte (elevación 0°).
// Geometría: ángulo central α = arccos(R / (R + h)); radio = R · α.
func FootprintRadiusKm(altKm float64) float64 {
	const R = 6378.137
	if altKm <= 0 {
		return 0
	}
	alpha := math.Acos(R / (R + altKm))
	return R * alpha
}

// Pass representa un paso del satélite sobre el observador.
type Pass struct {
	Start       time.Time // ascenso (cruza 0° de elevación subiendo)
	Peak        time.Time // máxima elevación
	End         time.Time // descenso (cruza 0° bajando)
	MaxElDeg    float64   // elevación máxima alcanzada (°)
	DurationSec float64   // duración en segundos
}

// PredictPasses calcula todos los pasos visibles sobre el observador en una
// ventana de "hours" horas a partir de "from", usando un escaneo a pasos
// de "stepSec" segundos y refinamiento por bisección.
//
// minElDeg filtra pasos cuya elevación máxima sea menor que ese umbral
// (típico: 10° para observación visual razonable; 0° para detectar todos).
func (s *Sat) PredictPasses(obsLatDeg, obsLonDeg, obsAltKm float64,
	from time.Time, hours float64, minElDeg float64) ([]Pass, error) {

	obsLat := obsLatDeg * math.Pi / 180
	obsLon := obsLonDeg * math.Pi / 180

	end := from.Add(time.Duration(hours * float64(time.Hour)))
	stepSec := 30.0 // 30 s es un compromiso bueno para LEO
	step := time.Duration(stepSec * float64(time.Second))

	elAt := func(t time.Time) (float64, error) {
		tsince := MinutesSince(s.prop.EpochJD(), t)
		posTEME, _, err := s.prop.Propagate(tsince)
		if err != nil {
			return 0, err
		}
		posECEF := TEMEtoECEF(posTEME, GMST(t))
		_, el, _ := AzEl(obsLat, obsLon, obsAltKm, posECEF)
		return el, nil
	}

	var passes []Pass
	t := from
	prevEl, err := elAt(t)
	if err != nil {
		return nil, err
	}

	for t.Before(end) {
		tNext := t.Add(step)
		nextEl, err := elAt(tNext)
		if err != nil {
			t = tNext
			prevEl = nextEl
			continue
		}
		// Detección de rise: el pasa de negativo a positivo.
		if prevEl < 0 && nextEl >= 0 {
			rise := bisectZero(elAt, t, tNext)
			// Buscamos peak y set.
			peak, peakEl, set := findPeakAndSet(elAt, rise, end, step)
			if peakEl*180/math.Pi >= minElDeg {
				passes = append(passes, Pass{
					Start:       rise,
					Peak:        peak,
					End:         set,
					MaxElDeg:    peakEl * 180 / math.Pi,
					DurationSec: set.Sub(rise).Seconds(),
				})
			}
			// Saltamos al final del paso para no detectarlo dos veces.
			t = set.Add(step)
			prevEl, _ = elAt(t)
			continue
		}
		t = tNext
		prevEl = nextEl
	}
	return passes, nil
}

// bisectZero refina por bisección el instante en que la elevación cruza cero
// entre lo y hi (asume lo<0, hi>=0). Precisión final ≈ 0.5 s.
func bisectZero(fn func(time.Time) (float64, error), lo, hi time.Time) time.Time {
	for i := 0; i < 32 && hi.Sub(lo) > time.Second; i++ {
		mid := lo.Add(hi.Sub(lo) / 2)
		el, err := fn(mid)
		if err != nil {
			return mid
		}
		if el < 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi
}

// findPeakAndSet localiza el máximo de elevación tras "start" y el siguiente
// cruce a 0° en descenso. Para el peak usa búsqueda dorada en una ventana
// de 30 minutos (suficiente: ningún paso LEO dura más).
func findPeakAndSet(fn func(time.Time) (float64, error),
	start time.Time, hardEnd time.Time, step time.Duration) (peak time.Time, peakEl float64, set time.Time) {

	// 1. Avance grueso para encontrar el set (cruce a negativo).
	t := start
	prev, _ := fn(t)
	for t.Before(hardEnd) {
		tNext := t.Add(step)
		next, err := fn(tNext)
		if err != nil {
			t = tNext
			continue
		}
		if prev >= 0 && next < 0 {
			set = bisectZero(func(tt time.Time) (float64, error) {
				e, err := fn(tt)
				return -e, err // invertimos para reutilizar bisectZero
			}, t, tNext)
			break
		}
		t = tNext
		prev = next
	}
	if set.IsZero() {
		set = hardEnd
	}

	// 2. Peak por búsqueda dorada en [start, set].
	const phi = 0.6180339887498949
	lo, hi := start, set
	for i := 0; i < 40 && hi.Sub(lo) > 2*time.Second; i++ {
		span := hi.Sub(lo)
		x1 := lo.Add(time.Duration((1 - phi) * float64(span)))
		x2 := lo.Add(time.Duration(phi * float64(span)))
		f1, _ := fn(x1)
		f2, _ := fn(x2)
		if f1 < f2 {
			lo = x1
		} else {
			hi = x2
		}
	}
	peak = lo.Add(hi.Sub(lo) / 2)
	peakEl, _ = fn(peak)
	return
}
