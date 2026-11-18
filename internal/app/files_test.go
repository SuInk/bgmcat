package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilesLocalBrowseMkdirDeleteAndStream(t *testing.T) {
	a := testApp(t)
	dlRoot, err := a.downloadRoot()
	if err != nil {
		t.Fatal(err)
	}

	// 1. 新建子目录
	w := apiTest(a, "POST", "/api/files/local/mkdir", Obj{"parent": "", "name": "葬送的芙莉莲"}, true)
	if w.Code != 200 {
		t.Fatalf("mkdir failed: %d %s", w.Code, w.Body.String())
	}

	// 2. 写入视频文件与字幕文件
	subDir := filepath.Join(dlRoot, "葬送的芙莉莲")
	mp4Data := "fake-mp4-binary-payload"
	if err := os.WriteFile(filepath.Join(subDir, "01.mp4"), []byte(mp4Data), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "01.ass"), []byte("[Script Info]"), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. 列举根目录，目录在最前
	w = apiTest(a, "GET", "/api/files/local", nil, true)
	if w.Code != 200 {
		t.Fatalf("list root failed: %d %s", w.Code, w.Body.String())
	}
	var rootResp struct {
		OK   bool `json:"ok"`
		Data struct {
			Entries []Obj `json:"entries"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rootResp)
	if len(rootResp.Data.Entries) != 1 || !flag(rootResp.Data.Entries[0]["isDir"]) {
		t.Fatalf("unexpected root entries: %+v", rootResp.Data.Entries)
	}

	// 4. 列举子目录并校验 mediaId
	w = apiTest(a, "GET", "/api/files/local?path="+url.QueryEscape("葬送的芙莉莲"), nil, true)
	if w.Code != 200 {
		t.Fatalf("list subdir failed: %d %s", w.Code, w.Body.String())
	}
	var subResp struct {
		Data struct {
			Entries []Obj `json:"entries"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &subResp)
	if len(subResp.Data.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %+v", subResp.Data.Entries)
	}
	var mp4MediaID string
	for _, entry := range subResp.Data.Entries {
		if str(entry["name"]) == "01.mp4" {
			if !flag(entry["playable"]) {
				t.Fatalf("expected 01.mp4 playable: %+v", entry)
			}
			mp4MediaID = str(entry["mediaId"])
		}
	}
	if mp4MediaID == "" {
		t.Fatal("missing mediaId for 01.mp4")
	}

	// 5. 通过合成 mediaId 获取播放信息与字节流
	w = apiTest(a, "GET", "/api/downloads/"+url.PathEscape(mp4MediaID)+"/playback", nil, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"deliveryMode":"direct"`) {
		t.Fatalf("playback failed: %d %s", w.Code, w.Body.String())
	}
	w = apiTest(a, "GET", "/api/downloads/"+url.PathEscape(mp4MediaID)+"/stream", nil, true)
	if w.Code != 200 || w.Body.String() != mp4Data {
		t.Fatalf("stream failed: %d %q", w.Code, w.Body.String())
	}

	// 6. 路径穿越与删根目录必须拒绝
	if w = apiTest(a, "GET", "/api/files/local?path=../../etc", nil, true); w.Code == 200 {
		t.Fatal("expected traversal rejection")
	}
	if w = apiTest(a, "DELETE", "/api/files/local?path=", nil, true); w.Code == 200 {
		t.Fatal("expected root deletion rejection")
	}

	// 7. 删除子目录
	w = apiTest(a, "DELETE", "/api/files/local?path="+url.QueryEscape("葬送的芙莉莲"), nil, true)
	if w.Code != 200 {
		t.Fatalf("delete failed: %d %s", w.Code, w.Body.String())
	}
}

func TestFilesCloudBrowseMkdirDeleteAndStream(t *testing.T) {
	a := testApp(t)

	// 未连接 Google Drive 时返回 200 且 connected=false
	w := apiTest(a, "GET", "/api/files/cloud", nil, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"connected":false`) {
		t.Fatalf("disconnected cloud response: %d %s", w.Code, w.Body.String())
	}

	// 写入凭据并模拟 Google Drive API
	_ = a.Store.Put("secrets", "google", Obj{
		"access_token":  "test-token",
		"refresh_token": "refresh-token",
		"expires_at":    time.Now().Add(time.Hour).Unix(),
	})
	trashedID := ""
	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		headers := make(http.Header)
		switch {
		case r.Method == "GET" && r.URL.Path == "/drive/v3/about":
			return &http.Response{
				StatusCode: 200,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader(`{"user":{"emailAddress":"cat@example.com"},"storageQuota":{"usage":"1073741824","limit":"16106127360"}}`)),
			}, nil
		case r.Method == "GET" && r.URL.Path == "/drive/v3/files":
			return &http.Response{
				StatusCode: 200,
				Header:     headers,
				Body: io.NopCloser(strings.NewReader(`{"files":[
					{"id":"folder_s1","name":"Season 1","mimeType":"application/vnd.google-apps.folder","modifiedTime":"2026-10-01T10:00:00Z"},
					{"id":"file_ep01","name":"[Nekomoe] 01.mp4","mimeType":"video/mp4","size":"123456","modifiedTime":"2026-10-02T10:00:00Z"}
				]}`)),
			}, nil
		case r.Method == "POST" && r.URL.Path == "/drive/v3/files":
			return &http.Response{
				StatusCode: 200,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader(`{"id":"folder_new","name":"SP","mimeType":"application/vnd.google-apps.folder"}`)),
			}, nil
		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/drive/v3/files/"):
			trashedID = strings.TrimPrefix(r.URL.Path, "/drive/v3/files/")
			return &http.Response{
				StatusCode: 200,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader(`{"id":"` + trashedID + `","trashed":true}`)),
			}, nil
		case r.Method == "GET" && r.URL.Path == "/drive/v3/files/file_ep01" && r.URL.Query().Get("alt") == "media":
			headers.Set("Content-Length", "16")
			return &http.Response{
				StatusCode: 200,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader("cloud-video-data")),
			}, nil
		default:
			t.Fatalf("unexpected Drive call: %s %s", r.Method, r.URL.String())
			return nil, nil
		}
	})

	w = apiTest(a, "GET", "/api/files/cloud?parent=root", nil, true)
	if w.Code != 200 {
		t.Fatalf("cloud list failed: %d %s", w.Code, w.Body.String())
	}
	var cloudResp struct {
		Data struct {
			Connected bool  `json:"connected"`
			Entries   []Obj `json:"entries"`
			Quota     Obj   `json:"quota"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &cloudResp)
	if !cloudResp.Data.Connected || len(cloudResp.Data.Entries) != 2 {
		t.Fatalf("unexpected cloud entries: %+v", cloudResp.Data)
	}
	if str(cloudResp.Data.Quota["userEmail"]) != "cat@example.com" {
		t.Fatalf("missing quota info: %+v", cloudResp.Data.Quota)
	}
	cloudMediaID := str(cloudResp.Data.Entries[1]["mediaId"])
	if cloudMediaID == "" {
		t.Fatalf("missing cloud mediaId: %+v", cloudResp.Data.Entries[1])
	}

	// 测试直连流式读取云端媒体
	w = apiTest(a, "GET", "/api/downloads/"+url.PathEscape(cloudMediaID)+"/stream", nil, true)
	if w.Code != 200 || w.Body.String() != "cloud-video-data" {
		t.Fatalf("cloud stream failed: %d %q", w.Code, w.Body.String())
	}

	// 测试新建云盘目录与移入回收站
	if w = apiTest(a, "POST", "/api/files/cloud/mkdir", Obj{"parent": "root", "name": "SP"}, true); w.Code != 200 {
		t.Fatalf("cloud mkdir failed: %d %s", w.Code, w.Body.String())
	}
	if w = apiTest(a, "DELETE", "/api/files/cloud/file_ep01", nil, true); w.Code != 200 || trashedID != "file_ep01" {
		t.Fatalf("cloud delete failed: %d %s trashed=%s", w.Code, w.Body.String(), trashedID)
	}
}

func TestFilesLocalPreviewDownloadTrashRestoreAndEmpty(t *testing.T) {
	a := testApp(t)
	dlRoot, err := a.downloadRoot()
	if err != nil {
		t.Fatal(err)
	}

	subDir := filepath.Join(dlRoot, "败犬女主")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	noteContent := "AI 记录发送时间功能：每条消息记录精确到毫秒的发送时间戳。"
	if err := os.WriteFile(filepath.Join(subDir, "AI 记录发送时间功能"), []byte(noteContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "cover.pdf"), []byte("%PDF-1.7\nfake pdf content"), 0644); err != nil {
		t.Fatal(err)
	}

	// 未鉴权访问必须 401
	if w := apiTest(a, "GET", "/api/files/local/content?path="+url.QueryEscape("败犬女主/AI 记录发送时间功能"), nil, false); w.Code != 401 {
		t.Fatalf("expected 401 unauthed, got %d", w.Code)
	}

	// ?token= 查询参数鉴权预览无扩展名中文文本
	tok := str(a.Settings()["token"])
	w := apiTest(a, "GET", "/api/files/local/content?path="+url.QueryEscape("败犬女主/AI 记录发送时间功能")+"&token="+url.QueryEscape(tok), nil, false)
	if w.Code != 200 || w.Body.String() != noteContent {
		t.Fatalf("text preview failed: %d %q", w.Code, w.Body.String())
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("expected text/plain, got %q", w.Header().Get("Content-Type"))
	}
	if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") {
		t.Fatalf("expected inline disposition, got %q", w.Header().Get("Content-Disposition"))
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("expected sandbox CSP for non-pdf, got %q", w.Header().Get("Content-Security-Policy"))
	}

	// PDF 预览不设 sandbox CSP（否则浏览器内置 PDF 阅读器拒绝渲染），下载模式设 attachment
	wPdf := apiTest(a, "GET", "/api/files/local/content?path="+url.QueryEscape("败犬女主/cover.pdf"), nil, true)
	if wPdf.Code != 200 || wPdf.Header().Get("Content-Type") != "application/pdf" || wPdf.Header().Get("Content-Security-Policy") != "" {
		t.Fatalf("pdf preview headers mismatch: code=%d ct=%q csp=%q", wPdf.Code, wPdf.Header().Get("Content-Type"), wPdf.Header().Get("Content-Security-Policy"))
	}
	wDl := apiTest(a, "GET", "/api/files/local/content?path="+url.QueryEscape("败犬女主/cover.pdf")+"&download=1", nil, true)
	if wDl.Code != 200 || !strings.HasPrefix(wDl.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download disposition mismatch: %q", wDl.Header().Get("Content-Disposition"))
	}

	// 删除默认移入本地 .trash 回收站，并支持列举与原路径还原
	wDel := apiTest(a, "DELETE", "/api/files/local?path="+url.QueryEscape("败犬女主/AI 记录发送时间功能"), nil, true)
	if wDel.Code != 200 || !strings.Contains(wDel.Body.String(), `"trashed":true`) {
		t.Fatalf("trash move failed: %d %s", wDel.Code, wDel.Body.String())
	}
	wTrash := apiTest(a, "GET", "/api/files/local?path=.trash", nil, true)
	if wTrash.Code != 200 || !strings.Contains(wTrash.Body.String(), `"trashCount":1`) || !strings.Contains(wTrash.Body.String(), "败犬女主/AI 记录发送时间功能") {
		t.Fatalf("trash list mismatch: %d %s", wTrash.Code, wTrash.Body.String())
	}
	wRestore := apiTest(a, "POST", "/api/files/local/trash/restore", Obj{"path": ".trash/AI 记录发送时间功能"}, true)
	if wRestore.Code != 200 {
		t.Fatalf("restore failed: %d %s", wRestore.Code, wRestore.Body.String())
	}
	if _, err := os.Stat(filepath.Join(subDir, "AI 记录发送时间功能")); err != nil {
		t.Fatalf("expected restored file at original path: %v", err)
	}

	// 再次移入回收站并执行清空回收站
	_ = apiTest(a, "DELETE", "/api/files/local?path="+url.QueryEscape("败犬女主/cover.pdf"), nil, true)
	wEmpty := apiTest(a, "POST", "/api/files/local/trash/empty", Obj{}, true)
	if wEmpty.Code != 200 || !strings.Contains(wEmpty.Body.String(), `"deletedCount":1`) {
		t.Fatalf("empty trash failed: %d %s", wEmpty.Code, wEmpty.Body.String())
	}
}

func TestFilesCloudPreviewAndGoogleDocExport(t *testing.T) {
	a := testApp(t)
	_ = a.Store.Put("secrets", "google", Obj{
		"access_token":  "test-token",
		"refresh_token": "refresh-token",
		"expires_at":    time.Now().Add(time.Hour).Unix(),
	})

	docBody := "AI 记录发送时间功能\n1. 发送时记录本地时间戳\n2. 服务端落盘对齐"
	rawBody := "无扩展名纯文本云盘备忘录"

	a.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		headers := make(http.Header)
		switch {
		case r.Method == "GET" && r.URL.Path == "/drive/v3/files/doc_ai_note" && r.URL.Query().Get("alt") == "":
			return &http.Response{
				StatusCode: 200,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader(`{"id":"doc_ai_note","name":"AI 记录发送时间功能","mimeType":"application/vnd.google-apps.document"}`)),
			}, nil
		case r.Method == "GET" && r.URL.Path == "/drive/v3/files/doc_ai_note/export":
			if got := r.URL.Query().Get("mimeType"); got != "text/plain" {
				t.Fatalf("unexpected export mimeType: %q", got)
			}
			return &http.Response{
				StatusCode: 200,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader(docBody)),
			}, nil
		case r.Method == "GET" && r.URL.Path == "/drive/v3/files/raw_note" && r.URL.Query().Get("alt") == "":
			return &http.Response{
				StatusCode: 200,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader(`{"id":"raw_note","name":"AI 记录发送时间功能","mimeType":"application/octet-stream","size":"36"}`)),
			}, nil
		case r.Method == "GET" && r.URL.Path == "/drive/v3/files/raw_note" && r.URL.Query().Get("alt") == "media":
			headers.Set("Content-Type", "application/octet-stream")
			return &http.Response{
				StatusCode: 200,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader(rawBody)),
			}, nil
		default:
			t.Fatalf("unexpected Drive call: %s %s", r.Method, r.URL.String())
			return nil, nil
		}
	})

	// 1. Google Docs 原生文档预览与下载（自动追加 .txt）
	w := apiTest(a, "GET", "/api/files/cloud/content?id=doc_ai_note", nil, true)
	if w.Code != 200 || w.Body.String() != docBody || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("google doc preview failed: %d ct=%q body=%q", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	wDl := apiTest(a, "GET", "/api/files/cloud/content?id=doc_ai_note&download=1", nil, true)
	if wDl.Code != 200 || !strings.Contains(wDl.Header().Get("Content-Disposition"), ".txt") {
		t.Fatalf("google doc download disposition mismatch: %q", wDl.Header().Get("Content-Disposition"))
	}

	// 2. 无扩展名 octet-stream 云盘文件自动嗅探为 UTF-8 纯文本预览
	wRaw := apiTest(a, "GET", "/api/files/cloud/content?id=raw_note", nil, true)
	if wRaw.Code != 200 || wRaw.Body.String() != rawBody || !strings.HasPrefix(wRaw.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("cloud raw note sniff failed: %d ct=%q body=%q", wRaw.Code, wRaw.Header().Get("Content-Type"), wRaw.Body.String())
	}
}
