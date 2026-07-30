package asaas

import "math"

// Asaas represents money as decimal reais (JSON numbers, e.g. 19.9), never
// integer centavos — every other value in this codebase (and the wire
// contract to api) is integer centavos. These two helpers are the single
// conversion point, mirroring pix-gateway/internal/inter's own
// centavosToReais/reaisToCentavos for the same reason.
func centavosToReais(centavos int64) float64 {
	return float64(centavos) / 100
}

func reaisToCentavos(reais float64) int64 {
	return int64(math.Round(reais * 100))
}
