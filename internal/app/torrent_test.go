package app

import (
	"bytes"
	"context"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func localTorrentConfig(dir string) *torrent.ClientConfig {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.DisablePEX = true
	cfg.DisableUTP = true
	cfg.DisableWebtorrent = true
	cfg.DisableIPv6 = true
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.ListenPort = 0
	cfg.NoDefaultPortForwarding = true
	return cfg
}
func TestNativeTorrentLocalPeer(t *testing.T) {
	a := testApp(t)
	seedDir := t.TempDir()
	payload := bytes.Repeat([]byte("local fixture torrent"), 16000)
	path := filepath.Join(seedDir, "fixture.mp4")
	if e := os.WriteFile(path, payload, 0600); e != nil {
		t.Fatal(e)
	}
	info := metainfo.Info{PieceLength: 32 * 1024}
	if e := info.BuildFromFilePath(path); e != nil {
		t.Fatal(e)
	}
	raw, e := bencode.Marshal(info)
	if e != nil {
		t.Fatal(e)
	}
	mi := metainfo.MetaInfo{InfoBytes: raw}
	seedCfg := localTorrentConfig(seedDir)
	seedCfg.Seed = true
	seed, e := torrent.NewClient(seedCfg)
	if e != nil {
		t.Fatal(e)
	}
	defer seed.Close()
	st, e := seed.AddTorrent(&mi)
	if e != nil {
		t.Fatal(e)
	}
	st.VerifyData()
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peers := []map[string]any{}
		for _, addr := range seed.ListenAddrs() {
			if tcp, ok := addr.(*net.TCPAddr); ok {
				peers = append(peers, map[string]any{"ip": "127.0.0.1", "port": tcp.Port})
			}
		}
		data, _ := bencode.Marshal(map[string]any{"interval": 1, "peers": peers})
		w.Write(data)
	}))
	defer tracker.Close()
	mi.Announce = tracker.URL
	var buf bytes.Buffer
	if e = mi.Write(&buf); e != nil {
		t.Fatal(e)
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(buf.Bytes()) }))
	defer source.Close()
	for _, kind := range []string{"torrent-url", "magnet"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			link := source.URL + "/fixture.torrent"
			if kind == "magnet" {
				link = mi.Magnet(nil, &info).String()
			}
			j := Obj{"gid": "bt-" + kind, "directory": dir, "sourceType": kind, "sourceUrl": link}
			a.Store.Put("downloads", str(j["gid"]), j)
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			if err := a.engine.Download(ctx, j); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(dir, "fixture.mp4"))
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatal("BT bytes differ", err)
			}
		})
	}
}
