package app

import (
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func renderQRMatrixToPNG(t *testing.T, matrix [][]bool, outPath string) {
	t.Helper()
	n := len(matrix)
	const quiet = 4
	const scale = 8
	dim := (n + quiet*2) * scale
	img := image.NewGray(image.Rect(0, 0, dim, dim))
	for y := 0; y < dim; y++ {
		for x := 0; x < dim; x++ {
			img.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if !matrix[r][c] {
				continue
			}
			y0 := (r + quiet) * scale
			x0 := (c + quiet) * scale
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetGray(x0+dx, y0+dy, color.Gray{Y: 0})
				}
			}
		}
	}
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestQRCodeSVGAndDecodability(t *testing.T) {
	a := testApp(t)
	samples := []string{
		"http://192.168.1.25:3001",
		"https://cat-anime-test-1234.trycloudflare.com",
		// Long URL requiring Version >= 7 (tests alignment + version info bits)
		"https://cat-anime-very-long-tunnel-subdomain.example.trycloudflare.com/s/abcdef1234567890?redirect=mobile-webui-check",
	}

	for _, text := range samples {
		matrix, err := encodeQRMatrix(text)
		if err != nil {
			t.Fatalf("encodeQRMatrix(%q): %v", text, err)
		}
		n := len(matrix)
		if n < 21 || (n-17)%4 != 0 {
			t.Fatalf("invalid QR matrix size %d for %q", n, text)
		}
		// Verify finder top-left center 3x3 is dark and timing row 6 alternates
		for dr := 2; dr <= 4; dr++ {
			for dc := 2; dc <= 4; dc++ {
				if !matrix[dr][dc] {
					t.Fatalf("top-left finder center (%d,%d) light", dr, dc)
				}
			}
		}
		for i := 8; i < n-8; i++ {
			if matrix[6][i] != (i%2 == 0) {
				t.Fatalf("timing row mismatch at col %d", i)
			}
		}

		// Endpoint returns valid crisp SVG
		token := str(a.Settings()["token"])
		req := httptest.NewRequest(http.MethodGet, "/api/remote/qrcode?text="+strings.ReplaceAll(text, "&", "%26"), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "image/svg+xml") || !strings.Contains(w.Body.String(), "<svg ") {
			t.Fatalf("GET /api/remote/qrcode failed: %d ct=%q", w.Code, w.Header().Get("Content-Type"))
		}

		if runtime.GOOS == "darwin" {
			if _, lookErr := exec.LookPath("/usr/bin/swift"); lookErr == nil {
				pngPath := filepath.Join(t.TempDir(), "qr.png")
				renderQRMatrixToPNG(t, matrix, pngPath)
				swiftCode := `import Foundation; import Vision; import AppKit
let url = URL(fileURLWithPath: CommandLine.arguments[1])
guard let img = NSImage(contentsOf: url), let cg = img.cgImage(forProposedRect: nil, context: nil, hints: nil) else { exit(2) }
let req = VNDetectBarcodesRequest()
req.symbologies = [.qr]
try VNImageRequestHandler(cgImage: cg, options: [:]).perform([req])
let first = req.results?.first?.payloadStringValue ?? ""
print(first)`
				cmd := exec.Command("/usr/bin/swift", "-e", swiftCode, pngPath)
				out, sErr := cmd.Output()
				if sErr != nil {
					t.Fatalf("Vision QR decode error for %q: %v (%s)", text, sErr, string(out))
				}
				decoded := strings.TrimSpace(string(out))
				if decoded != text {
					t.Fatalf("Vision decoded %q, want %q", decoded, text)
				}
			}
		}
	}
}
