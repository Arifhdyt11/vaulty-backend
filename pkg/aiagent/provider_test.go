package aiagent

import (
	"reflect"
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	got := NormalizeTags([]string{"#Docker", "docker", "Laravel Queue", "  ", "c++", "go"}, 4)
	want := []string{"docker", "laravel-queue", "c", "go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestPadEmbedding(t *testing.T) {
	v, err := PadEmbedding([]float32{0.6, 0.8})
	if err != nil || len(v) != EmbeddingDims || v[0] != 0.6 || v[1] != 0.8 || v[EmbeddingDims-1] != 0 {
		t.Fatalf("pad salah: len=%d err=%v", len(v), err)
	}
	if _, err := PadEmbedding(make([]float32, EmbeddingDims+1)); err == nil {
		t.Fatal("dimensi > 1536 harus error")
	}
}
