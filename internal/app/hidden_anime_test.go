package app

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestHiddenAnimeAPI(t *testing.T) {
	a := testApp(t)

	// Initially empty
	w := apiTest(a, "GET", "/api/discovery/hidden", nil, true)
	if w.Code != 200 {
		t.Fatalf("GET hidden: status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		OK   bool  `json:"ok"`
		Data []Obj `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || !resp.OK || len(resp.Data) != 0 {
		t.Fatalf("unexpected initial list: %v (%v)", resp, err)
	}

	// Hide two anime (including one with colon in animeId)
	for _, item := range []Obj{
		{"animeId": "512345", "title": "测试番剧 A", "imageUrl": "https://lain.bgm.tv/pic/cover/l/01/02.jpg"},
		{"animeId": "anilist:998877", "title": "测试番剧 B", "imageUrl": ""},
	} {
		pw := apiTest(a, "POST", "/api/discovery/hidden", Obj{"anime": item}, true)
		if pw.Code != 200 {
			t.Fatalf("POST hidden (%v): status %d: %s", item["animeId"], pw.Code, pw.Body.String())
		}
	}

	// Verify both returned
	w = apiTest(a, "GET", "/api/discovery/hidden", nil, true)
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.Data) != 2 {
		t.Fatalf("expected 2 hidden items, got %d (%v)", len(resp.Data), err)
	}

	// Delete the colon-ID item
	dw := apiTest(a, "DELETE", "/api/discovery/hidden/"+url.PathEscape("anilist:998877"), nil, true)
	if dw.Code != 200 {
		t.Fatalf("DELETE hidden: status %d: %s", dw.Code, dw.Body.String())
	}

	w = apiTest(a, "GET", "/api/discovery/hidden", nil, true)
	resp.Data = nil
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.Data) != 1 || str(resp.Data[0]["animeId"]) != "512345" {
		t.Fatalf("expected 1 remaining hidden item 512345, got %v", resp.Data)
	}

	// Clear all
	cw := apiTest(a, "DELETE", "/api/discovery/hidden", nil, true)
	if cw.Code != 200 {
		t.Fatalf("DELETE all hidden: status %d: %s", cw.Code, cw.Body.String())
	}
	w = apiTest(a, "GET", "/api/discovery/hidden", nil, true)
	resp.Data = nil
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.Data) != 0 {
		t.Fatalf("expected 0 hidden items after clear, got %v", resp.Data)
	}
}
