package v1

import "testing"

func TestApplyNulls(t *testing.T) {
	keep := "tetap"
	var title, url, project *string = nil, nil, &keep
	applyNulls([]byte(`{"title":null,"body":"x","project":"baru"}`),
		map[string]**string{"title": &title, "url": &url, "project": &project})
	if title == nil || *title != "" {
		t.Fatalf("title null harus jadi \"\" (dikosongkan), got %v", title)
	}
	if url != nil {
		t.Fatalf("url tidak dikirim harus tetap nil, got %v", *url)
	}
	if project != &keep {
		t.Fatal("project bukan null tidak boleh diubah")
	}
}
