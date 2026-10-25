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
	var buf bytes.Buffer
	if e = mi.Write(&buf); e != nil {
		t.Fatal(e)
	}
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
	client, e := torrent.NewClient(localTorrentConfig(t.TempDir()))
	if e != nil {
		t.Fatal(e)
	}
	a.torrent.client = client
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(buf.Bytes()) }))
	defer source.Close()
	dir := t.TempDir()
	j := Obj{"gid": "bt-fixture", "directory": dir, "sourceType": "torrent-url", "sourceUrl": source.URL + "/fixture.torrent"}
	a.Store.Put("downloads", "bt-fixture", j)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.torrent.Download(ctx, j) }()
	tick := time.NewTicker(30 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
			got, e := os.ReadFile(filepath.Join(dir, "fixture.mp4"))
			if e != nil || !bytes.Equal(got, payload) {
				t.Fatal("BT bytes differ", e)
			}
			return
		case <-ctx.Done():
			t.Fatal("local torrent timed out")
		case <-tick.C:
			for _, down := range client.Torrents() {
				for _, addr := range seed.ListenAddrs() {
					if tcp, ok := addr.(*net.TCPAddr); ok {
						down.AddPeers([]torrent.PeerInfo{{Addr: tcp, Trusted: true}})
					}
				}
			}
		}
	}
}
