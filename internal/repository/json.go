package repository

import "encoding/json"

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func jsonUnmarshal(b []byte, v any) error {
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

// Kolom vector(1536) butuh dimensi tetap; model dengan dimensi lebih kecil (mis. bge-m3 1024)
// di-pad nol. Cosine distance tidak berubah oleh padding nol, dan ganti model tidak butuh migrasi.
const vectorDim = 1536

func padVector(v []float32) []float32 {
	if len(v) >= vectorDim {
		return v[:vectorDim]
	}
	out := make([]float32, vectorDim)
	copy(out, v)
	return out
}
