// Package sgp4 implementa el propagador de órbitas SGP4 (Simplified General
// Perturbations 4), descrito por Hoots & Roehrich en el Spacetrack Report #3
// (1980) y revisado por Vallado, Crawford, Hujsak & Kelso en "Revisiting
// Spacetrack Report #3" (AIAA 2006-6753).
//
// SGP4 es la implementación canónica que NORAD usa para propagar los TLE
// distribuidos públicamente. Modela las perturbaciones más importantes
// sobre una órbita kepleriana:
//
//   - J₂, J₃, J₄: armónicos zonales del campo gravitatorio terrestre
//     (achatamiento polar y asimetrías norte-sur).
//   - Arrastre atmosférico: modelo simplificado de densidad exponencial
//     parametrizado por el coeficiente BSTAR.
//   - Acoplamiento secular y de período largo entre estos efectos.
//
// SGP4 está diseñado para órbitas con período < 225 min (LEO típica).
// Para órbitas de mayor período (MEO, HEO, GEO) NORAD usa SDP4, que añade
// perturbaciones lunisolares y resonancia con la Tierra. Esta implementación
// sólo cubre SGP4; los satélites de "stations" (ISS, Tiangong, etc.) son
// todos LEO y caen dentro de ese rango.
//
// Unidades internas: el modelo usa unidades canónicas (radios terrestres
// para distancia, minutos para tiempo) por consistencia con la formulación
// original de Hoots. La salida del propagador (Propagate) se entrega en km
// y km/s en el marco TEME (True Equator Mean Equinox of date).
package sgp4

// Constantes WGS-72. SGP4 fue calibrado con WGS-72, así que aunque WGS-84
// sea más preciso, usar las constantes "incorrectas" da resultados más
// precisos porque cancelan errores del modelo. Esto está explícitamente
// recomendado en Vallado 2006.
const (
	// Constante gravitatoria geocéntrica reducida en unidades canónicas:
	// k_e = sqrt(GM_⊕) en (radios terrestres)^1.5 / minuto.
	// Su raíz cuadrada aparece en n_0 = k_e / a^1.5.
	ke = 0.0743669161331734132

	// Radio ecuatorial terrestre (km), WGS-72.
	earthRadiusKm = 6378.135

	// Armónicos zonales del geopotencial WGS-72. Los signos siguen el
	// convenio de Hoots (J₂ > 0 = achatamiento).
	j2 = 1.082616e-3
	j3 = -2.53881e-6
	j4 = -1.65597e-6

	// Combinaciones derivadas que aparecen tantas veces que se nombran:
	k2 = 0.5 * j2          // ½·J₂
	k4 = -0.375 * j4       // -⅜·J₄
	a3ovk2 = -j3 / k2      // -J₃/(½·J₂); modula efectos de período largo

	// Parámetros del modelo de arrastre atmosférico (densidad exponencial):
	//   ρ(h) = ρ₀·exp[-(h - q₀)/H]
	// q₀ es la altitud de referencia en perigeo (km), s es la altitud por
	// debajo de la cual el arrastre cambia de régimen (km), expresadas
	// como factores adimensionales (1 + altura/Re).
	q0     = 120.0 // km
	s0     = 78.0  // km
	qoms2t = 1.880279159015270643865e-9 // (q₀-s₀)^4 en (Re)^4
	s      = 1.0 + s0/earthRadiusKm

	// Velocidad de rotación de la Tierra (rev/día sidéreo) — útil al
	// transformar TEME → ECEF en el paso de coordenadas terrestres.
	earthRotRevPerDay = 1.00273790934
)
