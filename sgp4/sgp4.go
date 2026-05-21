package sgp4

import (
	"fmt"
	"math"

	"github.com/yourusername/satellite-tracker/tle"
)

// Vec3 representa un vector cartesiano 3D (posición o velocidad) en TEME.
type Vec3 struct{ X, Y, Z float64 }

// Sat contiene los elementos derivados y las constantes precalculadas para
// un satélite concreto. Se inicializa una vez por TLE con NewSat y luego se
// propaga llamando a Propagate con el tiempo transcurrido desde la época.
type Sat struct {
	// Elementos originales convertidos a unidades canónicas (radianes,
	// minutos, radios terrestres).
	bstar    float64
	ecco     float64
	inclo    float64 // inclinación (rad)
	nodeo    float64 // RAAN inicial (rad)
	argpo    float64 // arg. del perigeo inicial (rad)
	mo       float64 // anomalía media inicial (rad)
	no       float64 // movimiento medio (rad/min) - "n_0"
	a        float64 // semieje mayor (Re)
	alta     float64 // altura de apogeo  (Re, sobre superficie)
	altp     float64 // altura de perigeo (Re, sobre superficie)
	epochJD  float64 // época en Julian Date (días desde noon -4713 enero 1)

	// Constantes precomputadas (notación de Hoots).
	cosio, sinio   float64
	cosio2         float64
	eta            float64
	c1, c2, c4, c5 float64
	d2, d3, d4     float64
	mdot, nodedot  float64
	argpdot        float64
	nodecf         float64
	t2cof, t3cof   float64
	t4cof, t5cof   float64
	xmcof          float64
	omgcof         float64
	delmo, sinmao  float64
	x1mth2, x7thm1 float64
	isimp          bool // true si perigeo < 220 km (simplificación)
}

// NewSat construye un propagador a partir de un TLE ya parseado.
// Aplica las inicializaciones de SGP4 ("init mode") según Vallado 2006.
func NewSat(t tle.TLE) (*Sat, error) {
	const (
		twoPi  = 2 * math.Pi
		minDay = 1440.0 // minutos en un día
	)
	sat := &Sat{
		bstar: t.BStar,
		ecco:  t.Eccentricity,
		inclo: t.Inclination * math.Pi / 180,
		nodeo: t.RAAN * math.Pi / 180,
		argpo: t.ArgPerigee * math.Pi / 180,
		mo:    t.MeanAnomaly * math.Pi / 180,
		// Convertimos n de rev/día a rad/min:  n[rad/min] = n[rev/día] · 2π / 1440.
		no: t.MeanMotion * twoPi / minDay,
	}
	sat.epochJD = epochToJulian(t.EpochYear, t.EpochDay)

	if sat.ecco < 0 || sat.ecco >= 1 {
		return nil, fmt.Errorf("excentricidad fuera de rango: %g", sat.ecco)
	}

	// ---- 1. Recuperación del semieje mayor "kozai" sin perturbar ----
	// La tercera ley de Kepler en unidades canónicas:  n² · a³ = k_e²
	// Pero el "n" del TLE viene afectado por J₂; lo invertimos en dos pasos
	// (a_1 → a_0) siguiendo Hoots ec. (1)-(2).
	cosio := math.Cos(sat.inclo)
	cosio2 := cosio * cosio
	sat.cosio, sat.sinio, sat.cosio2 = cosio, math.Sin(sat.inclo), cosio2

	x3thm1 := 3.0*cosio2 - 1.0
	eosq := sat.ecco * sat.ecco
	betao2 := 1.0 - eosq
	betao := math.Sqrt(betao2)

	a1 := math.Pow(ke/sat.no, 2.0/3.0)
	delta1 := 1.5 * k2 * x3thm1 / (a1 * a1 * betao * betao2)
	ao := a1 * (1.0 - delta1*(1.0/3.0+delta1*(1.0+134.0/81.0*delta1)))
	delo := 1.5 * k2 * x3thm1 / (ao * ao * betao * betao2)
	// Movimiento medio y semieje "no-prime" / "ao-prime" usados de aquí en adelante.
	sat.no = sat.no / (1.0 + delo)
	sat.a = ao / (1.0 - delo)

	// Alturas de apogeo y perigeo (Re sobre superficie).
	sat.altp = sat.a*(1.0-sat.ecco) - 1.0
	sat.alta = sat.a*(1.0+sat.ecco) - 1.0

	// Si el perigeo está por debajo de ~220 km, se usa la formulación
	// simplificada (sin términos de orden alto en arrastre).
	sat.isimp = sat.altp < (220.0 / earthRadiusKm)

	// ---- 2. Modelo de arrastre: cálculo de C1, C2, C4, C5 ----
	// Si el perigeo está por debajo de s0 (~78 km), se ajusta s para que
	// el modelo de densidad siga teniendo sentido (Hoots p. 12).
	// "sVal" sustituye a la constante 's' del paquete para este satélite.
	sVal := s
	qoms24 := qoms2t
	perige := (sat.a*(1.0-sat.ecco) - 1.0) * earthRadiusKm // perigeo en km
	if perige < 156 {
		sfourKm := perige - 78.0
		if perige < 98 {
			sfourKm = 20.0
		}
		qoms24 = math.Pow((120.0-sfourKm)/earthRadiusKm, 4)
		sVal = 1.0 + sfourKm/earthRadiusKm
	}
	pinvsq := 1.0 / (sat.a * sat.a * betao2 * betao2)

	// tsi = 1/(a - s) en Re; eta = a·e/(a - s)
	tsi := 1.0 / (sat.a - sVal)
	sat.eta = sat.a * sat.ecco * tsi
	etasq := sat.eta * sat.eta
	eeta := sat.ecco * sat.eta
	psisq := math.Abs(1.0 - etasq)
	coef := qoms24 * math.Pow(tsi, 4)
	coef1 := coef / math.Pow(psisq, 3.5)

	c2 := coef1 * sat.no * (sat.a*(1.0+1.5*etasq+eeta*(4.0+etasq)) +
		0.375*k2*tsi/psisq*x3thm1*(8.0+3.0*etasq*(8.0+etasq)))
	sat.c2 = c2
	sat.c1 = sat.bstar * c2

	sinio := sat.sinio
	c3 := 0.0
	if sat.ecco > 1e-4 {
		c3 = coef * tsi * a3ovk2 * sat.no * sinio / sat.ecco
	}
	x1mth2 := 1.0 - cosio2
	sat.x1mth2 = x1mth2

	sat.c4 = 2.0 * sat.no * coef1 * sat.a * betao2 * (sat.eta*(2.0+0.5*etasq) +
		sat.ecco*(0.5+2.0*etasq) -
		k2*tsi/(sat.a*psisq)*(-3.0*x3thm1*(1.0-2.0*eeta+etasq*(1.5-0.5*eeta))+
			0.75*x1mth2*(2.0*etasq-eeta*(1.0+etasq))*math.Cos(2.0*sat.argpo)))
	sat.c5 = 2.0 * coef1 * sat.a * betao2 * (1.0 + 2.75*(etasq+eeta) + eeta*etasq)

	// ---- 3. Tasas seculares debidas a J₂ y J₄ ----
	// xmdot, nodedot, argpdot son los famosos drifts del nodo, perigeo y
	// anomalía media inducidos por el achatamiento terrestre.
	temp1 := 1.5 * k2 * pinvsq * sat.no
	temp2 := 0.5 * temp1 * k2 * pinvsq
	temp3 := -0.46875 * k4 * pinvsq * pinvsq * sat.no

	sat.mdot = sat.no + 0.5*temp1*betao*x3thm1 + 0.0625*temp2*betao*
		(13.0-78.0*cosio2+137.0*cosio2*cosio2)

	x1m5th := 1.0 - 5.0*cosio2
	sat.argpdot = -0.5*temp1*x1m5th + 0.0625*temp2*(7.0-114.0*cosio2+395.0*cosio2*cosio2) +
		temp3*(3.0-36.0*cosio2+49.0*cosio2*cosio2)
	xhdot1 := -temp1 * cosio
	sat.nodedot = xhdot1 + (0.5*temp2*(4.0-19.0*cosio2)+2.0*temp3*(3.0-7.0*cosio2))*cosio
	sat.xmcof = 0.0
	if eeta > 1e-9 {
		sat.xmcof = -2.0 / 3.0 * coef * sat.bstar / eeta
	}
	sat.nodecf = 3.5 * betao2 * xhdot1 * sat.c1
	sat.t2cof = 1.5 * sat.c1
	sat.x7thm1 = 7.0*cosio2 - 1.0

	// Cof. para integración del long-period periodics, sólo si no es "simp".
	if !sat.isimp {
		c1sq := sat.c1 * sat.c1
		sat.d2 = 4.0 * sat.a * tsi * c1sq
		temp := sat.d2 * tsi * sat.c1 / 3.0
		sat.d3 = (17.0*sat.a + sVal) * temp
		sat.d4 = 0.5 * temp * sat.a * tsi * (221.0*sat.a + 31.0*sVal) * sat.c1
		sat.t3cof = sat.d2 + 2.0*c1sq
		sat.t4cof = 0.25 * (3.0*sat.d3 + sat.c1*(12.0*sat.d2+10.0*c1sq))
		sat.t5cof = 0.2 * (3.0*sat.d4 + 12.0*sat.c1*sat.d3 + 6.0*sat.d2*sat.d2 + 15.0*c1sq*(2.0*sat.d2+c1sq))
	}

	sat.omgcof = sat.bstar * c3 * math.Cos(sat.argpo)
	sat.delmo = math.Pow(1.0+sat.eta*math.Cos(sat.mo), 3)
	sat.sinmao = math.Sin(sat.mo)

	return sat, nil
}

// Propagate calcula posición y velocidad del satélite en el marco TEME
// (km y km/s) a un tiempo dado en MINUTOS desde la época del TLE.
// Para obtener tsince a partir de un time.Time, ver satellite.MinutesSince(...).
func (sat *Sat) Propagate(tsince float64) (pos, vel Vec3, err error) {
	const twoPi = 2 * math.Pi

	// ---- Secular: M, ω, Ω, a, e evolucionan linealmente con t ----
	xmdf := sat.mo + sat.mdot*tsince
	argpdf := sat.argpo + sat.argpdot*tsince
	nodedf := sat.nodeo + sat.nodedot*tsince
	argpm := argpdf
	mm := xmdf
	t2 := tsince * tsince
	nodem := nodedf + sat.nodecf*t2

	tempa := 1.0 - sat.c1*tsince
	tempe := sat.bstar * sat.c4 * tsince
	templ := sat.t2cof * t2

	if !sat.isimp {
		delomg := sat.omgcof * tsince
		delm := sat.xmcof * (math.Pow(1.0+sat.eta*math.Cos(xmdf), 3) - sat.delmo)
		temp := delomg + delm
		mm = xmdf + temp
		argpm = argpdf - temp
		t3 := t2 * tsince
		t4 := t3 * tsince
		tempa = tempa - sat.d2*t2 - sat.d3*t3 - sat.d4*t4
		tempe = tempe + sat.bstar*sat.c5*(math.Sin(mm)-sat.sinmao)
		templ = templ + sat.t3cof*t3 + t4*(sat.t4cof+tsince*sat.t5cof)
	}

	nm := sat.no
	em := sat.ecco
	inclm := sat.inclo

	am := math.Pow(ke/nm, 2.0/3.0) * tempa * tempa
	nm = ke / math.Pow(am, 1.5)
	em = em - tempe

	// Sanea valores fuera de rango (efecto numérico cerca de reentrada).
	if em < 1e-6 {
		em = 1e-6
	} else if em > 0.999 {
		em = 0.999
	}

	mm = mm + sat.no*templ
	xlm := mm + argpm + nodem
	nodem = math.Mod(nodem, twoPi)
	argpm = math.Mod(argpm, twoPi)
	xlm = math.Mod(xlm, twoPi)
	mm = math.Mod(xlm-argpm-nodem, twoPi)

	// ---- Periodics de largo período ----
	sinim := math.Sin(inclm)
	cosim := math.Cos(inclm)

	ep := em
	xincp := inclm
	argpp := argpm
	nodep := nodem
	mp := mm

	axnl := ep * math.Cos(argpp)
	temp := 1.0 / (am * (1.0 - ep*ep))
	aynl := ep*math.Sin(argpp) + temp*a3ovk2*sinim
	xl := mp + argpp + nodep + temp*a3ovk2*axnl*(3.0+5.0*cosim)/(1.0+cosim)

	// ---- Resuelve la ecuación de Kepler (E - e·sinE = M) por Newton ----
	u := math.Mod(xl-nodep, twoPi)
	eo1 := u
	tem5 := 9999.9
	for ktr := 0; ktr < 10 && math.Abs(tem5) >= 1e-12; ktr++ {
		sineo1 := math.Sin(eo1)
		coseo1 := math.Cos(eo1)
		tem5 = 1.0 - coseo1*axnl - sineo1*aynl
		tem5 = (u - aynl*coseo1 + axnl*sineo1 - eo1) / tem5
		if math.Abs(tem5) >= 0.95 {
			if tem5 > 0 {
				tem5 = 0.95
			} else {
				tem5 = -0.95
			}
		}
		eo1 += tem5
	}

	// ---- Conversión a coordenadas orbitales ----
	ecose := axnl*math.Cos(eo1) + aynl*math.Sin(eo1)
	esine := axnl*math.Sin(eo1) - aynl*math.Cos(eo1)
	el2 := axnl*axnl + aynl*aynl
	pl := am * (1.0 - el2)
	if pl < 0 {
		return Vec3{}, Vec3{}, fmt.Errorf("semilatus rectum negativo: pl=%g", pl)
	}
	rl := am * (1.0 - ecose)
	rdotl := math.Sqrt(am) / rl * esine
	rvdotl := math.Sqrt(pl) / rl
	betal := math.Sqrt(1.0 - el2)
	temp = esine / (1.0 + betal)
	sinu := am / rl * (math.Sin(eo1) - aynl - axnl*temp)
	cosu := am / rl * (math.Cos(eo1) - axnl + aynl*temp)
	su := math.Atan2(sinu, cosu)
	sin2u := (cosu + cosu) * sinu
	cos2u := 1.0 - 2.0*sinu*sinu

	// ---- Correcciones cortas de J₂ ----
	temp = 1.0 / pl
	temp1 := 0.5 * k2 * temp
	temp2 := temp1 * temp

	mrt := rl*(1.0-1.5*temp2*betal*(3.0*cosim*cosim-1.0)) + 0.5*temp1*sat.x1mth2*cos2u
	su = su - 0.25*temp2*sat.x7thm1*sin2u
	xnode := nodep + 1.5*temp2*cosim*sin2u
	xinc := xincp + 1.5*temp2*cosim*sinim*cos2u
	mvt := rdotl - nm*temp1*sat.x1mth2*sin2u/ke
	rvdot := rvdotl + nm*temp1*(sat.x1mth2*cos2u+1.5*(3.0*cosim*cosim-1.0))/ke

	// ---- Orientación final TEME ----
	sinsu, cossu := math.Sincos(su)
	snod, cnod := math.Sincos(xnode)
	sini, cosi := math.Sincos(xinc)
	xmx := -snod * cosi
	xmy := cnod * cosi
	ux := xmx*sinsu + cnod*cossu
	uy := xmy*sinsu + snod*cossu
	uz := sini * sinsu
	vx := xmx*cossu - cnod*sinsu
	vy := xmy*cossu - snod*sinsu
	vz := sini * cossu

	// Posición en Re → km, velocidad en Re/min → km/s
	r := earthRadiusKm
	rkm := mrt * r
	pos = Vec3{X: rkm * ux, Y: rkm * uy, Z: rkm * uz}
	vKm := r / 60.0
	vel = Vec3{
		X: (mvt*ux + rvdot*vx) * vKm,
		Y: (mvt*uy + rvdot*vy) * vKm,
		Z: (mvt*uz + rvdot*vz) * vKm,
	}
	if mrt < 1.0 {
		return pos, vel, fmt.Errorf("satélite en reentrada (mrt=%g)", mrt)
	}
	return pos, vel, nil
}

// EpochJD devuelve la época del TLE como Julian Date.
func (sat *Sat) EpochJD() float64 { return sat.epochJD }

// MeanMotionRadPerMin devuelve el movimiento medio recuperado en rad/min,
// útil para calcular el período orbital: T_min = 2π / n.
func (sat *Sat) MeanMotionRadPerMin() float64 { return sat.no }

// SemiMajorAxisKm devuelve el semieje mayor recuperado en km.
func (sat *Sat) SemiMajorAxisKm() float64 { return sat.a * earthRadiusKm }

// EarthRadiusKm es el radio terrestre usado por SGP4 (WGS-72).
func EarthRadiusKm() float64 { return earthRadiusKm }

// epochToJulian convierte (año, día fraccional del año) → Julian Date.
// Se calcula el JD del 1 enero 00:00 UTC del año dado y se suma (día - 1).
func epochToJulian(year int, day float64) float64 {
	// JD del 0 enero del año (es decir, 31 diciembre del año anterior 00:00 UTC).
	// Fórmula directa Gregorian → JD (Meeus).
	Y := year
	M := 1
	D := 1
	if M <= 2 {
		Y -= 1
		M += 12
	}
	A := math.Floor(float64(Y) / 100)
	B := 2 - A + math.Floor(A/4)
	jd0 := math.Floor(365.25*float64(Y+4716)) +
		math.Floor(30.6001*float64(M+1)) +
		float64(D) + B - 1524.5
	return jd0 + (day - 1.0)
}
