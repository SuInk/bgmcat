package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAllowedMangaImageURL(t *testing.T) {
	cases := []struct {
		url     string
		allowed bool
	}{
		{"https://uploads.mangadex.org/covers/abc/def.512.jpg", true},
		{"https://cmdxd98sb0x3yprd.mangadex.network/data/hash/1.png", true},
		{"http://uploads.mangadex.org/covers/abc/def.jpg", false},
		{"https://127.0.0.1:8080/secret.png", false},
		{"https://evil.example.com/uploads.mangadex.org/a.jpg", false},
	}
	for _, tc := range cases {
		_, got := isAllowedMangaImageURL(tc.url)
		if got != tc.allowed {
			t.Fatalf("isAllowedMangaImageURL(%q) = %v, want %v", tc.url, got, tc.allowed)
		}
	}
}

func TestNormalizeMangaObjectPrefersChinese(t *testing.T) {
	raw := Obj{
		"id": "0a580438-bc72-4503-940b-12a5da881b56",
		"attributes": Obj{
			"title": Obj{"ja-ro": "Kimi wa Yotsuba no Clover"},
			"altTitles": []any{
				Obj{"en": "You Are a Four-Leaf Clover"},
				Obj{"zh": "恋人手中四叶草"},
			},
			"description": Obj{"en": "English desc", "zh": "中文简介"},
			"status":      "ongoing",
			"year":        2024,
			"lastChapter": "42",
			"tags": []any{
				Obj{"attributes": Obj{"name": Obj{"en": "Romance", "zh": "恋爱"}}},
			},
		},
		"relationships": []any{
			Obj{"type": "cover_art", "attributes": Obj{"fileName": "cover.jpg"}},
			Obj{"type": "author", "attributes": Obj{"name": "Koushi"}},
		},
	}

	got := normalizeMangaObject(raw)
	if str(got["title"]) != "恋人手中四叶草" {
		t.Fatalf("expected Chinese title, got %q", str(got["title"]))
	}
	if str(got["description"]) != "中文简介" {
		t.Fatalf("expected Chinese description, got %q", str(got["description"]))
	}
	if !strings.HasPrefix(str(got["coverUrl"]), "/api/manga/image?url=") {
		t.Fatalf("expected proxied coverUrl, got %q", str(got["coverUrl"]))
	}
}

func TestMangaProgressCRUD(t *testing.T) {
	a := testApp(t)
	wSave := apiTest(a, "POST", "/api/manga/progress", Obj{
		"mangaId":      "0a580438-bc72-4503-940b-12a5da881b56",
		"title":        "恋人手中四叶草",
		"coverUrl":     "/api/manga/image?url=test",
		"chapterId":    "chap-12345678",
		"chapterLabel": "第 12 话",
		"pageIndex":    5,
		"totalPages":   24,
	}, true)
	if wSave.Code != 200 {
		t.Fatalf("save progress status=%d body=%s", wSave.Code, wSave.Body.String())
	}

	wList := apiTest(a, "GET", "/api/manga/progress", nil, true)
	if wList.Code != 200 || !strings.Contains(wList.Body.String(), "恋人手中四叶草") {
		t.Fatalf("list progress unexpected: status=%d body=%s", wList.Code, wList.Body.String())
	}

	wBadProxy := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/manga/image?url=http://127.0.0.1:8080/a.png", nil)
	req.Header.Set("Authorization", "Bearer "+str(a.Settings()["token"]))
	a.Handler().ServeHTTP(wBadProxy, req)
	if wBadProxy.Code != 403 {
		t.Fatalf("expected 403 for disallowed image host, got %d", wBadProxy.Code)
	}

	wDel := apiTest(a, "DELETE", "/api/manga/progress/0a580438-bc72-4503-940b-12a5da881b56", nil, true)
	if wDel.Code != 200 {
		t.Fatalf("delete progress status=%d", wDel.Code)
	}
}
