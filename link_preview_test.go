package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
)

func previewPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// previewSite serves a product page with Open Graph tags, the page image at
// /page.png and a caller image at /custom.png, and counts requests per route.
type previewSite struct {
	server *httptest.Server
	page   atomic.Int32
	pageIm atomic.Int32
	custom atomic.Int32
}

func newPreviewSite(t *testing.T) *previewSite {
	t.Helper()
	site := &previewSite{}
	pageImage := previewPNG(t, 800, 400, color.RGBA{R: 200, A: 255})
	customImage := previewPNG(t, 400, 400, color.RGBA{B: 200, A: 255})
	mux := http.NewServeMux()
	mux.HandleFunc("/product", func(w http.ResponseWriter, r *http.Request) {
		site.page.Add(1)
		fmt.Fprint(w, `<html><head><meta property="og:title" content="Page title">
<meta property="og:description" content="Page description">
<meta property="og:image" content="/page.png"></head></html>`)
	})
	mux.HandleFunc("/page.png", func(w http.ResponseWriter, r *http.Request) {
		site.pageIm.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Write(pageImage)
	})
	mux.HandleFunc("/custom.png", func(w http.ResponseWriter, r *http.Request) {
		site.custom.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Write(customImage)
	})
	site.server = httptest.NewServer(mux)
	t.Cleanup(site.server.Close)

	previous := globalHTTPClient
	globalHTTPClient = site.server.Client()
	t.Cleanup(func() { globalHTTPClient = previous })
	openGraphCache.Flush()
	t.Cleanup(openGraphCache.Flush)
	return site
}

func (s *previewSite) productURL() string { return s.server.URL + "/product" }

func previewSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("thumbnail is not a decodable image: %v", err)
	}
	return cfg.Width, cfg.Height
}

func TestValidateLinkPreviewImage(t *testing.T) {
	valid := []string{"", "https://cdn.example.com/a.jpg", "http://example.com/a.png", "data:image/png;base64,AAAA", "DATA:image/jpeg;base64,AAAA"}
	for _, v := range valid {
		if err := validateLinkPreviewImage(v); err != nil {
			t.Errorf("%q rejected: %v", v, err)
		}
	}
	invalid := []string{"ftp://example.com/a.png", "file:///etc/passwd", "javascript:alert(1)", "not a url", "data:text/html;base64,AAAA", "/relative.png"}
	for _, v := range invalid {
		if err := validateLinkPreviewImage(v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
}

func TestResolveLinkPreviewWithoutOverrideUsesThePage(t *testing.T) {
	site := newPreviewSite(t)
	got := resolveLinkPreview(context.Background(), site.productURL(), "user", linkPreviewOverride{})
	if got.Title != "Page title" || got.Description != "Page description" {
		t.Fatalf("page text not used: %+v", got)
	}
	if w, _ := previewSize(t, got.HQImageData); w != 600 {
		t.Fatalf("page image not used for the large card, width %d", w)
	}
	if site.custom.Load() != 0 {
		t.Fatal("custom image was requested without an override")
	}
}

func TestResolveLinkPreviewFullOverrideSkipsThePage(t *testing.T) {
	site := newPreviewSite(t)
	got := resolveLinkPreview(context.Background(), site.productURL(), "user", linkPreviewOverride{
		Title: "Our title", Image: site.server.URL + "/custom.png",
	})
	if got.Title != "Our title" || got.Description != "" {
		t.Fatalf("unexpected text: %+v", got)
	}
	if site.page.Load() != 0 || site.pageIm.Load() != 0 {
		t.Fatalf("page fetched although title and image were supplied (page=%d image=%d)", site.page.Load(), site.pageIm.Load())
	}
	if site.custom.Load() != 1 {
		t.Fatalf("custom image fetched %d times", site.custom.Load())
	}
	w, h := previewSize(t, got.HQImageData)
	if w != 400 || h != 400 || got.HQWidth != 400 || got.HQHeight != 400 {
		t.Fatalf("large card is not the custom image: %dx%d (%dx%d)", w, h, got.HQWidth, got.HQHeight)
	}
	if w, _ := previewSize(t, got.ImageData); w > openGraphThumbnailWidth {
		t.Fatalf("inline thumbnail too large: %d", w)
	}
}

func TestResolveLinkPreviewTitleOnlyKeepsThePageImageAndDescription(t *testing.T) {
	site := newPreviewSite(t)
	got := resolveLinkPreview(context.Background(), site.productURL(), "user", linkPreviewOverride{Title: "Our title"})
	if got.Title != "Our title" || got.Description != "Page description" {
		t.Fatalf("unexpected text: %+v", got)
	}
	if w, _ := previewSize(t, got.HQImageData); w != 600 {
		t.Fatalf("page image lost, width %d", w)
	}
}

func TestResolveLinkPreviewImageOnlyKeepsThePageTitle(t *testing.T) {
	site := newPreviewSite(t)
	got := resolveLinkPreview(context.Background(), site.productURL(), "user", linkPreviewOverride{Image: site.server.URL + "/custom.png"})
	if got.Title != "Page title" {
		t.Fatalf("page title lost: %+v", got)
	}
	if w, _ := previewSize(t, got.HQImageData); w != 400 {
		t.Fatalf("custom image not used, width %d", w)
	}
}

func TestResolveLinkPreviewDataURLImage(t *testing.T) {
	site := newPreviewSite(t)
	data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(previewPNG(t, 300, 200, color.RGBA{G: 200, A: 255}))
	got := resolveLinkPreview(context.Background(), site.productURL(), "user", linkPreviewOverride{Title: "T", Image: data})
	if w, h := previewSize(t, got.HQImageData); w != 300 || h != 200 {
		t.Fatalf("data URL image not used: %dx%d", w, h)
	}
	if site.custom.Load() != 0 || site.page.Load() != 0 {
		t.Fatal("unexpected network fetch for a data URL image")
	}
}

func TestResolveLinkPreviewBrokenImageFallsBackToThePage(t *testing.T) {
	site := newPreviewSite(t)
	got := resolveLinkPreview(context.Background(), site.productURL(), "user", linkPreviewOverride{
		Title: "Our title", Image: site.server.URL + "/missing.png",
	})
	if got.Title != "Our title" {
		t.Fatalf("title lost: %+v", got)
	}
	if w, _ := previewSize(t, got.HQImageData); w != 600 {
		t.Fatalf("page image not used as fallback, width %d", w)
	}
}

func TestResolveLinkPreviewOverrideImageIsNotFetchedFromPrivateHosts(t *testing.T) {
	site := newPreviewSite(t)
	globalHTTPClient = newSafeHTTPClient()
	got := resolveLinkPreview(context.Background(), site.productURL(), "user", linkPreviewOverride{
		Title: "T", Image: site.server.URL + "/custom.png",
	})
	if site.custom.Load() != 0 || len(got.HQImageData) != 0 {
		t.Fatal("override image bypassed the SSRF-safe client")
	}
}

// A malformed LinkPreviewImage must fail the request on both transports before
// any WhatsApp lookup or network operation, instead of silently losing the image.
func TestSendMessageRejectsInvalidLinkPreviewImageHTTPAndStdio(t *testing.T) {
	s := makeTestServer(t)
	const user, token = "link-preview-validation-user", "link-preview-validation-token"
	if _, err := s.db.Exec(`INSERT INTO users (id, name, token) VALUES ($1, $2, $3)`, user, "tester", token); err != nil {
		t.Fatal(err)
	}
	clientManager.SetWhatsmeowClient(user, whatsmeow.NewClient(&store.Device{}, nil))
	t.Cleanup(func() { clientManager.DeleteWhatsmeowClient(user) })

	params := map[string]interface{}{
		"Phone": "15555550100", "Body": "Look https://example.com", "LinkPreview": true,
		"LinkPreviewImage": "ftp://example.com/cover.png", "token": token,
	}
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/chat/send/text", strings.NewReader(string(body)))
	req.Header.Set("token", token)
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "LinkPreviewImage") {
		t.Fatalf("HTTP: got %d: %s", response.Code, response.Body.String())
	}

	rpc := executeRequest(t, s, newRequest("invalid-preview-image", "chat.send.text", params).toJSON(t))
	rpcError := assertJSONRPC20Error(t, rpc, "invalid-preview-image", float64(http.StatusBadRequest))
	if message, _ := rpcError["message"].(string); !strings.Contains(message, "LinkPreviewImage") {
		t.Fatalf("stdio: unexpected error %v", rpcError)
	}
}
