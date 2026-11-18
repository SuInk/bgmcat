package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var driveFolderID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,200}$`)

func (a *App) cloudSetupRoutes() {
	a.handle("GET /api/cloud/folders", func(w http.ResponseWriter, r *http.Request) error {
		provider := firstNonEmpty(strings.TrimSpace(r.URL.Query().Get("provider")), a.activeCloudProvider())
		parent := r.URL.Query().Get("parent")
		if parent == "" {
			parent = "root"
		}
		switch provider {
		case "onedrive":
			result, err := a.onedriveListFolders(r.Context(), parent, r.URL.Query().Get("pageToken"))
			if err == nil {
				ok(w, result)
			}
			return err
		case "webdav":
			items, err := a.webdavPropfind(r.Context(), a.Settings(), cleanWebDAVRelPath(parent), "1")
			if err != nil {
				return err
			}
			files := []Obj{}
			for _, it := range items {
				if it.IsDir {
					files = append(files, Obj{"id": it.RelPath, "name": it.Name})
				}
			}
			ok(w, Obj{"files": files, "nextPageToken": ""})
			return nil
		}
		if !driveFolderID.MatchString(parent) {
			return errors.New("无效的文件夹 ID")
		}
		q := url.Values{"q": {"trashed = false and mimeType = 'application/vnd.google-apps.folder' and '" + parent + "' in parents"}, "fields": {"nextPageToken,files(id,name)"}, "pageSize": {"100"}, "orderBy": {"name"}, "supportsAllDrives": {"true"}, "includeItemsFromAllDrives": {"true"}}
		if token := r.URL.Query().Get("pageToken"); token != "" {
			q.Set("pageToken", token)
		}
		result, _, err := a.driveJSON(r.Context(), "GET", driveBase+"/files?"+q.Encode(), nil)
		if err == nil {
			ok(w, result)
		}
		return err
	})
	a.handle("POST /api/cloud/folders", func(w http.ResponseWriter, r *http.Request) error {
		input, err := body(w, r)
		if err != nil {
			return err
		}
		provider := firstNonEmpty(strings.TrimSpace(str(input["provider"])), a.activeCloudProvider())
		parent := str(input["parent"])
		if parent == "" {
			parent = "root"
		}
		folderName := strings.TrimSpace(str(input["name"]))
		if folderName == "" {
			folderName = "Bangumi Cat"
		} else {
			folderName, err = validateSingleEntryName(folderName)
			if err != nil {
				return err
			}
		}
		switch provider {
		case "onedrive":
			result, err := a.onedriveCreateFolder(r.Context(), parent, folderName)
			if err == nil {
				ok(w, result)
			}
			return err
		case "webdav":
			parentRel := cleanWebDAVRelPath(parent)
			targetRel := folderName
			if parentRel != "" {
				targetRel = parentRel + "/" + folderName
			}
			if err := a.webdavEnsureDir(r.Context(), a.Settings(), targetRel); err != nil {
				return err
			}
			ok(w, Obj{"id": targetRel, "name": folderName})
			return nil
		}
		if !driveFolderID.MatchString(parent) {
			return errors.New("无效的文件夹 ID")
		}
		result, _, err := a.driveJSON(r.Context(), "POST", driveBase+"/files?fields=id,name&supportsAllDrives=true", Obj{"name": folderName, "mimeType": "application/vnd.google-apps.folder", "parents": []string{parent}})
		if err == nil {
			ok(w, result)
		}
		return err
	})
	a.handle("POST /api/cloud/check", func(w http.ResponseWriter, r *http.Request) error {
		input, err := body(w, r)
		if err != nil {
			return err
		}
		provider := firstNonEmpty(strings.TrimSpace(str(input["provider"])), a.activeCloudProvider())
		folder := str(input["folderId"])
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		switch provider {
		case "onedrive":
			if folder == "" {
				folder = "root"
			}
			_, err = a.onedriveListFolders(ctx, folder, "")
			if err == nil {
				ok(w, Obj{"verified": true})
			}
			return err
		case "webdav":
			if err := a.webdavEnsureDir(ctx, a.Settings(), cleanWebDAVRelPath(folder)); err != nil {
				return err
			}
			ok(w, Obj{"verified": true})
			return nil
		}
		if !driveFolderID.MatchString(folder) {
			return errors.New("请选择保存文件夹")
		}
		err = a.checkDriveFolder(ctx, folder)
		if err == nil {
			ok(w, Obj{"verified": true})
		}
		return err
	})
}

// Only the newly created diagnostic file is removed, even on read/upload failure.
func (a *App) checkDriveFolder(ctx context.Context, folder string) (resultErr error) {
	file, _, err := a.driveJSON(ctx, "POST", driveBase+"/files?fields=id&supportsAllDrives=true", Obj{"name": ".bgmcat-connection-test-" + randomID(8) + ".txt", "parents": []string{folder}, "mimeType": "text/plain"})
	if err != nil {
		return fmt.Errorf("无法在所选目录创建测试文件：%w", err)
	}
	id := str(file["id"])
	if !driveFolderID.MatchString(id) {
		return errors.New("Google 未返回有效测试文件 ID")
	}
	endpoint := driveBase + "/files/" + url.PathEscape(id)
	defer func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, e := a.driveRequest(cleanCtx, "DELETE", endpoint+"?supportsAllDrives=true", nil, nil)
		if e == nil {
			res.Body.Close()
			if res.StatusCode >= 300 {
				e = fmt.Errorf("HTTP %d", res.StatusCode)
			}
		}
		if e != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("测试文件清理失败（ID %s），请到云盘手动删除：%w", id, e))
		}
	}()
	content := []byte("Bangumi Cat connection check " + randomID(24))
	res, err := a.driveRequest(ctx, "PATCH", "https://www.googleapis.com/upload/drive/v3/files/"+url.PathEscape(id)+"?uploadType=media&supportsAllDrives=true", content, map[string]string{"Content-Type": "text/plain"})
	if err != nil {
		return fmt.Errorf("测试上传失败：%w", err)
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("测试上传失败：HTTP %d", res.StatusCode)
	}
	res, err = a.driveRequest(ctx, "GET", endpoint+"?alt=media&supportsAllDrives=true", nil, nil)
	if err != nil {
		return fmt.Errorf("测试读取失败：%w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	if err != nil {
		return err
	}
	if res.StatusCode != 200 || string(data) != string(content) {
		return errors.New("云端读取校验失败，请检查网络后重试")
	}
	return nil
}
