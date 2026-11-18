package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSharesCreateUnlockStreamDownloadExpiryAndRevoke(t *testing.T) {
	a := testApp(t)
	a.authFailDelay = 0
	dlRoot, err := a.downloadRoot()
	if err != nil {
		t.Fatal(err)
	}

	// Prepare a multi-episode anime folder with natural episode ordering (2 before 10).
	animeDir := filepath.Join(dlRoot, "FX战士久留美")
	if err := os.MkdirAll(animeDir, 0755); err != nil {
		t.Fatal(err)
	}
	ep2Bytes := []byte("ep02-video-bytes-payload")
	ep10Bytes := []byte("ep10-video-bytes-payload-longer")
	if err := os.WriteFile(filepath.Join(animeDir, "[SFSub] FX战士久留美 - 10 [1080P].mp4"), ep10Bytes, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(animeDir, "[SFSub] FX战士久留美 - 02 [1080P].mp4"), ep2Bytes, 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Unauthenticated caller cannot create or list shares.
	if w := apiTest(a, "GET", "/api/shares", nil, false); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on unauthed GET /api/shares, got %d", w.Code)
	}

	// 2. Admin creates a passcode-protected, stream-only (allowDownload=false) multi-episode share.
	w := apiTest(a, "POST", "/api/shares", Obj{
		"title":          "FX战士久留美",
		"expiresInHours": 24,
		"passcode":       "cat8",
		"allowDownload":  false,
		"targets": []Obj{
			{"kind": "local", "path": "FX战士久留美"},
		},
	}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("create share failed: %d %s", w.Code, w.Body.String())
	}
	var createResp struct {
		OK   bool `json:"ok"`
		Data struct {
			Code          string `json:"code"`
			Title         string `json:"title"`
			HasPasscode   bool   `json:"hasPasscode"`
			Passcode      string `json:"passcode"`
			AllowDownload bool   `json:"allowDownload"`
			ItemCount     int    `json:"itemCount"`
			Items         []struct {
				ID       string `json:"id"`
				Label    string `json:"label"`
				FileName string `json:"fileName"`
				Playable bool   `json:"playable"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatal(err)
	}
	code := createResp.Data.Code
	if code == "" || createResp.Data.ItemCount != 2 || !createResp.Data.HasPasscode || createResp.Data.Passcode != "cat8" || createResp.Data.AllowDownload {
		t.Fatalf("unexpected created share: %+v", createResp.Data)
	}
	// Verify natural sort (02 comes before 10) and inferred episode labels.
	if !strings.Contains(createResp.Data.Items[0].FileName, "02") || createResp.Data.Items[0].Label != "第 02 集" {
		t.Fatalf("expected item 0 to be ep 02, got %+v", createResp.Data.Items[0])
	}
	if !strings.Contains(createResp.Data.Items[1].FileName, "10") || createResp.Data.Items[1].Label != "第 10 集" {
		t.Fatalf("expected item 1 to be ep 10, got %+v", createResp.Data.Items[1])
	}

	// 3. Guest fetches public share without passcode -> locked=true, items hidden!
	w = apiTest(a, "GET", "/api/public/shares/"+code, nil, false)
	if w.Code != http.StatusOK {
		t.Fatalf("public share metadata failed: %d %s", w.Code, w.Body.String())
	}
	var lockedResp struct {
		Data struct {
			Locked    bool  `json:"locked"`
			ItemCount int   `json:"itemCount"`
			Items     []Obj `json:"items"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &lockedResp)
	if !lockedResp.Data.Locked || lockedResp.Data.ItemCount != 2 || len(lockedResp.Data.Items) != 0 {
		t.Fatalf("expected locked share with 0 exposed items, got %+v", lockedResp.Data)
	}

	// 4. Guest cannot stream while locked.
	if w = apiTest(a, "GET", "/api/public/shares/"+code+"/items/0/stream", nil, false); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 streaming locked share, got %d", w.Code)
	}

	// 5. Wrong passcode returns 401; correct passcode returns unlockToken + items.
	if w = apiTest(a, "POST", "/api/public/shares/"+code+"/unlock", Obj{"passcode": "wrong"}, false); w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on wrong passcode, got %d", w.Code)
	}
	w = apiTest(a, "POST", "/api/public/shares/"+code+"/unlock", Obj{"passcode": "cat8"}, false)
	if w.Code != http.StatusOK {
		t.Fatalf("unlock failed: %d %s", w.Code, w.Body.String())
	}
	var unlockResp struct {
		Data struct {
			Locked      bool   `json:"locked"`
			UnlockToken string `json:"unlockToken"`
			Items       []Obj  `json:"items"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &unlockResp)
	st := unlockResp.Data.UnlockToken
	if unlockResp.Data.Locked || st == "" || len(unlockResp.Data.Items) != 2 {
		t.Fatalf("unexpected unlock response: %+v", unlockResp.Data)
	}

	// 6. Playback metadata sanitizes local host path (never leaks dlRoot).
	w = apiTest(a, "GET", "/api/public/shares/"+code+"/items/0/playback?st="+st, nil, false)
	if w.Code != http.StatusOK {
		t.Fatalf("public playback info failed: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), dlRoot) {
		t.Fatalf("public playback response leaked host filesystem path: %s", w.Body.String())
	}

	// 7. Stream succeeds with ?st=..., while /download is blocked (403) because allowDownload=false.
	w = apiTest(a, "GET", "/api/public/shares/"+code+"/items/0/stream?st="+st, nil, false)
	if w.Code != http.StatusOK || w.Body.String() != string(ep2Bytes) {
		t.Fatalf("public stream mismatch: code=%d body=%q", w.Code, w.Body.String())
	}
	w = apiTest(a, "GET", "/api/public/shares/"+code+"/items/0/download?st="+st, nil, false)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when allowDownload=false, got %d", w.Code)
	}

	// 8. Admin enables allowDownload via PATCH -> /download now succeeds with attachment header.
	w = apiTest(a, "PATCH", "/api/shares/"+code, Obj{"allowDownload": true}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("patch allowDownload failed: %d %s", w.Code, w.Body.String())
	}
	w = apiTest(a, "GET", "/api/public/shares/"+code+"/items/1/download?st="+st, nil, false)
	if w.Code != http.StatusOK || w.Body.String() != string(ep10Bytes) {
		t.Fatalf("public download mismatch: code=%d body=%q", w.Code, w.Body.String())
	}
	if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("expected attachment Content-Disposition, got %q", w.Header().Get("Content-Disposition"))
	}

	// 9. Expired share returns 410 Gone.
	_, _ = a.Store.Patch("shares", code, Obj{"expiresAt": time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)})
	if w = apiTest(a, "GET", "/api/public/shares/"+code, nil, false); w.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone on expired share, got %d", w.Code)
	}

	// 10. Revoking share deletes it -> 404 Not Found.
	if w = apiTest(a, "DELETE", "/api/shares/"+code, nil, true); w.Code != http.StatusOK {
		t.Fatalf("delete share failed: %d", w.Code)
	}
	if w = apiTest(a, "GET", "/api/public/shares/"+code, nil, false); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after revoke, got %d", w.Code)
	}
}
