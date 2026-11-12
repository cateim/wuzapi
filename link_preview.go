package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/rs/zerolog/log"
)

// linkPreviewOverride carries the optional preview fields a caller can send with
// /chat/send/text. Set fields replace what the page's Open Graph tags provide.
type linkPreviewOverride struct {
	Title       string
	Description string
	Image       string // http(s) URL or data:image/...;base64 URL
}

func (o linkPreviewOverride) isZero() bool {
	return o.Title == "" && o.Description == "" && o.Image == ""
}

// validateLinkPreviewImage rejects values that are neither an http(s) URL nor an
// image data URL, so a typo fails the request instead of silently losing the image.
func validateLinkPreviewImage(value string) error {
	if value == "" || isHTTPURL(value) || strings.HasPrefix(strings.ToLower(value), "data:image/") {
		return nil
	}
	return errors.New("invalid LinkPreviewImage: expected an http(s) URL or a data:image/...;base64 URL")
}

// resolveLinkPreview returns the preview data for urlStr. Without overrides it is
// the plain Open Graph lookup. With overrides, the page is only fetched when the
// title or the image is missing, and any field the caller set wins over the page.
func resolveLinkPreview(ctx context.Context, urlStr, userID string, override linkPreviewOverride) openGraphResult {
	if override.isZero() {
		return getOpenGraphData(ctx, urlStr, userID)
	}

	var page openGraphResult
	pageFetched := override.Title == "" || override.Image == ""
	if pageFetched {
		page = getOpenGraphData(ctx, urlStr, userID)
	}

	result := openGraphResult{Title: override.Title, Description: override.Description}
	if result.Title == "" {
		result.Title = page.Title
	}
	if result.Description == "" {
		result.Description = page.Description
	}

	if override.Image != "" {
		applyOverrideImage(ctx, userID, override.Image, &result)
	}
	if len(result.HQImageData) == 0 {
		// The caller's image is missing or unusable: a card without a picture is
		// worse than the page's own image, so look the page up if we have not yet.
		if !pageFetched {
			page = getOpenGraphData(ctx, urlStr, userID)
		}
		result.ImageData, result.HQImageData = page.ImageData, page.HQImageData
		result.HQWidth, result.HQHeight = page.HQWidth, page.HQHeight
	}
	return result
}

// applyOverrideImage loads the caller's image and builds the thumbnails. A failure
// is logged and leaves result without an image, so the page's image can take over.
// It runs under the same per-user limit and panic guard as the page lookup, since
// decoding and resizing a large image is the expensive part.
func applyOverrideImage(ctx context.Context, userID, value string, result *openGraphResult) {
	ctx, cancel := context.WithTimeout(ctx, openGraphFetchTimeout)
	defer cancel()

	userPool := userSemaphoreManager.ForUser(userID)
	select {
	case userPool <- struct{}{}:
		defer func() { <-userPool }()
	case <-ctx.Done():
		log.Warn().Msg("Link preview image skipped while waiting for a worker")
		return
	}

	defer func() {
		if r := recover(); r != nil {
			log.Error().
				Interface("panic_info", r).
				Bytes("stack", debug.Stack()).
				Msg("Panic recovered while building the link preview image")
			*result = openGraphResult{Title: result.Title, Description: result.Description}
		}
	}()

	imgBytes, err := readPreviewImage(ctx, value)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load link preview image from the request")
		return
	}
	applyOpenGraphImage(imgBytes, "request:LinkPreviewImage", result)
}

func readPreviewImage(ctx context.Context, value string) ([]byte, error) {
	media, err := readOutgoingMedia(ctx, value, openGraphImageMaxBytes)
	if err != nil {
		return nil, err
	}
	defer media.Close()

	file, err := media.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, openGraphImageMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > openGraphImageMaxBytes {
		return nil, fmt.Errorf("image exceeds allowed size (%d bytes)", openGraphImageMaxBytes)
	}
	return data, nil
}
