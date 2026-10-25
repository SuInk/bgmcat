package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/SuInk/bgmcat/internal/app"
	"github.com/SuInk/bgmcat/web"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
func main() {
	home, _ := os.UserHomeDir()
	data := flag.String("data", env("ANIME_RSS_DATA_DIR", filepath.Join(home, ".anime-rss-go")), "private data directory")
	addr := flag.String("listen", env("ANIME_RSS_LISTEN", "127.0.0.1:3001"), "HTTP listen address")
	public := flag.String("public-url", env("ANIME_RSS_PUBLIC_URL", ""), "external URL for OAuth callbacks")
	importPath := flag.String("import-legacy", "", "import a COPY of the TypeScript app SQLite database, then exit")
	stdio := flag.Bool("mcp-stdio", false, "MCP stdio bridge to the running Bangumi Cat server")
	desktop := flag.Bool("desktop", false, "private desktop child process with stdio lifecycle")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(app.Version)
		return
	}
	if *stdio {
		if e := app.RunMCPStdio(context.Background(), os.Stdin, os.Stdout, os.Getenv("ANIME_RSS_MCP_URL"), os.Getenv("ANIME_RSS_MCP_TOKEN")); e != nil {
			log.Fatal(e)
		}
		return
	}
	var listener net.Listener
	if *importPath == "" {
		if *desktop {
			host, _, err := net.SplitHostPort(*addr)
			if err != nil || host != "127.0.0.1" {
				log.Fatal("Desktop server must listen on 127.0.0.1")
			}
		}
		var err error
		listener, err = net.Listen("tcp", *addr)
		if err != nil {
			log.Fatal(err)
		}
		defer listener.Close()
		*addr = listener.Addr().String()
	}
	a, e := app.New(app.Config{DataDir: *data, Addr: *addr, PublicURL: *public, Web: web.FS()})
	if e != nil {
		log.Fatal(e)
	}
	if *importPath != "" {
		if e = a.ImportLegacy(*importPath); e != nil {
			log.Fatal(e)
		}
		a.Close()
		fmt.Println("Legacy data imported. Original database was not changed.")
		return
	}
	a.Start()
	server := &http.Server{Addr: *addr, Handler: a.Handler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 90 * time.Second}
	go func() {
		if e := server.Serve(listener); e != nil && e != http.ErrServerClosed {
			log.Fatal(e)
		}
	}()
	if *desktop {
		ready, _ := json.Marshal(map[string]any{"url": "http://" + *addr, "token": a.Settings()["token"]})
		fmt.Printf("BGMCAT_DESKTOP_READY %s\n", ready)
	} else {
		fmt.Printf("Bangumi Cat %s\nhttp://%s/\nAccess token: %s\n", app.Version, *addr, a.Settings()["token"])
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	parentClosed := make(chan struct{})
	if *desktop {
		go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(parentClosed) }()
	}
	select {
	case <-ctx.Done():
	case <-parentClosed:
	}
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shut)
	_ = a.Close()
}
