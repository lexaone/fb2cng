package fb2

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"mime"
	"path"
	"strconv"
	"strings"

	"github.com/beevik/etree"

	"github.com/disintegration/imaging"
	"go.uber.org/zap"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"fbc/common"
	"fbc/config"
	"fbc/jpegquality"
	imgutil "fbc/utils/images"
)

// Image processing functions for FictionBook.

// PrepareImages processes all binary objects in the FictionBook creating
// actual image and building image index, applying output-format compatibility rules.
// Never returns an error - uses placeholder for broken images.
// Non-image binaries (e.g., fonts) are skipped.
func (fb *FictionBook) PrepareImages(outputFormat common.OutputFmt, cfg *config.ImagesConfig, log *zap.Logger) BookImages {
	index := make(BookImages)

	imgNum := 1
	for i := range fb.Binaries {
		// Skip non-image binaries (e.g., fonts loaded by stylesheet normalization)
		if !isImageMIME(fb.Binaries[i].ContentType) {
			log.Debug("Skipping non-image binary",
				zap.String("id", fb.Binaries[i].ID),
				zap.String("content-type", fb.Binaries[i].ContentType))
			continue
		}

		if _, exists := index[fb.Binaries[i].ID]; exists {
			log.Debug("Duplicate binary ID found, skipping", zap.String("id", fb.Binaries[i].ID))
			continue
		}
		cover := len(fb.Description.TitleInfo.Coverpage) > 0 && strings.HasSuffix(fb.Description.TitleInfo.Coverpage[0].Href, fb.Binaries[i].ID)
		bi := fb.Binaries[i].PrepareImage(outputFormat, cover, cfg, log)
		ext := mimeToExt(bi.MimeType)
		bi.Filename = path.Join(ImagesDir, fmt.Sprintf("img%05d.%s", imgNum, ext))
		imgNum++
		index[fb.Binaries[i].ID] = bi
	}
	return index
}

// handleImageError is a unified error handler for all image processing failures.
// It logs the error and optionally substitutes the image with a placeholder.
func (bo *BinaryObject) handleImageError(
	bi *BookImage,
	operation string,
	err error,
	normalizeToJPEG bool,
	cfg *config.ImagesConfig,
	log *zap.Logger,
) *BookImage {
	// Log warning with appropriate context
	if err != nil {
		log.Warn("Unable to "+operation+" image", zap.String("id", bo.ID), zap.String("content-type", bo.ContentType), zap.Error(err))
	} else {
		log.Warn("Unable to "+operation+" image", zap.String("id", bo.ID), zap.String("content-type", bo.ContentType))
	}

	if cfg.UseBroken {
		return bi
	}

	log.Debug("Substituting image with broken placeholder", zap.String("id", bo.ID))
	bi.Data = brokenImage
	bi.MimeType = "image/svg+xml"
	bi.BuiltinVignette = false
	bi.LayoutMimeType = ""

	if !normalizeToJPEG && !cfg.RasterizeSVG {
		// Discard dimensions from the failed source/resize, not the placeholder.
		bi.Dim.Width, bi.Dim.Height = 0, 0
		w, h, sizeErr := imgutil.SVGImageSize(brokenImage, 0, 0)
		if sizeErr != nil {
			log.Warn("Unable to determine broken placeholder SVG size", zap.Error(sizeErr))
		} else {
			bi.Dim.Width, bi.Dim.Height = w, h
		}
		return bi
	}

	// brokenImage placeholder SVG already has thick strokes (stroke-width="4"),
	// no scaling needed
	rasterize := imgutil.RasterizeSVGToImage
	imgType := "jpeg"
	if !normalizeToJPEG && cfg.RasterFormat != common.RasterFormatJpeg {
		imgType = "png"
		if !cfg.RemoveTransparency {
			rasterize = imgutil.RasterizeSVGToTransparentImage
		}
	}
	img, rasterErr := rasterize(brokenImage, 0, 0, 0)
	if rasterErr != nil {
		log.Warn("Unable to rasterize broken placeholder SVG", zap.String("id", bo.ID), zap.Error(rasterErr))
		return bi
	}

	data, encErr := bo.encodeImage(img, imgType, cfg, log)
	if encErr != nil {
		log.Warn("Unable to encode rasterized broken placeholder", zap.String("id", bo.ID), zap.Error(encErr))
		return bi
	}

	bi.Data = data
	bi.MimeType = "image/" + imgType
	bi.Dim.Width = img.Bounds().Dx()
	bi.Dim.Height = img.Bounds().Dy()
	return bi
}

func (bo *BinaryObject) encodeImage(img image.Image, imgType string, cfg *config.ImagesConfig, log *zap.Logger) ([]byte, error) {
	var buf = new(bytes.Buffer)
	var err error

	switch imgType {
	case "png":
		err = imaging.Encode(buf, img, imaging.PNG, imaging.PNGCompressionLevel(png.BestCompression))
		if err != nil {
			return nil, fmt.Errorf("unable to encode processed PNG, ID - %s: %w", bo.ID, err)
		}
		return buf.Bytes(), nil
	case "jpeg":
		if cfg.Grayscale || (cfg.Optimize && imgutil.IsGrayscale(img)) {
			if _, ok := img.(*image.Gray); !ok {
				gray := image.NewGray(img.Bounds())
				draw.Draw(gray, gray.Bounds(), img, img.Bounds().Min, draw.Src)
				img = gray
			}
		}

		err = imaging.Encode(buf, img, imaging.JPEG, imaging.JPEGQuality(cfg.JPEGQuality))
		if err != nil {
			return nil, fmt.Errorf("unable to encode processed JPEG, ID - %s: %w", bo.ID, err)
		}
		density := imgutil.JPEGDensityFromDPI(cfg.Screen.DPI)
		data, added, err := imgutil.EnsureJFIFAPP0(buf.Bytes(), imgutil.DpiPxPerInch, density, density)
		if err != nil {
			return nil, fmt.Errorf("unable to insert jpeg JFIF APP0 marker segment, ID - %s: %w", bo.ID, err)
		}
		if added {
			log.Debug("Inserting jpeg JFIF APP0 marker segment", zap.String("id", bo.ID))
		}
		return data, nil
	default:
		return nil, fmt.Errorf("unsupported image format %q, ID - %s", imgType, bo.ID)
	}
}

// PrepareImage performs required image modifications leaving original data
// intact if no changes where requested. If image is decodable it will always
// attempt to normalize mime type. Never returns an error - uses placeholder
// for broken images.
// NOTE: today KP3 always seems to scale images to 2048 width and height, we do
// not do it
func (bo *BinaryObject) PrepareImage(outputFormat common.OutputFmt, cover bool, cfg *config.ImagesConfig, log *zap.Logger) *BookImage {
	// Embedded Markdown retains its existing JPEG and vignette policy without
	// impersonating a Kindle output format.
	requireJPEG := outputFormat.IsKindle() ||
		(outputFormat == common.OutputFmtMd && cfg.Markdown == common.MarkdownImageModeEmbedded)
	normalizeToJPEG := requireJPEG || cfg.RasterFormat == common.RasterFormatJpeg
	// A substituted body placeholder can use native KVG. Covers, explicit raster
	// policies and embedded Markdown still need the existing JPEG representation.
	rasterizeBrokenSVG := requireJPEG &&
		(!outputFormat.IsKindle() || cover || cfg.RasterizeSVG || cfg.RemoveTransparency)
	bi := &BookImage{
		MimeType:        bo.ContentType,
		Data:            bo.Data,
		BuiltinVignette: bo.BuiltinVignette,
	}

	// SVG handling
	if strings.HasSuffix(strings.ToLower(bo.ContentType), "svg+xml") {
		bi.MimeType = "image/svg+xml"
		if outputFormat.IsKindle() && !cover && !cfg.RasterizeSVG && !cfg.RemoveTransparency {
			// Retain the legacy layout box without allocating a raster. The backend
			// can translate the scene or request the normal JPEG fallback later.
			targetW := cfg.Screen.Width
			if bytes.Equal(bo.Data, notFoundImage) || bytes.Equal(bo.Data, brokenImage) {
				targetW = 0
			}
			w, h, err := imgutil.SVGImageSize(bo.Data, targetW, 0)
			if err != nil {
				return bo.handleImageError(bi, "parse", err, rasterizeBrokenSVG, cfg, log)
			}
			if !bytes.Equal(bo.Data, brokenImage) && cfg.ScaleFactor > 0 && cfg.ScaleFactor != 1 {
				newH := int(float64(h) * cfg.ScaleFactor)
				if newH <= 0 {
					return bo.RasterizeSVG(outputFormat, cover, cfg, log)
				}
				w, h = max(int(math.Round(float64(w)*float64(newH)/float64(h))), 1), newH
			}
			bi.Dim.Width, bi.Dim.Height = w, h
			return bi
		}
		if !requireJPEG && !cfg.RasterizeSVG {
			if (!cover && cfg.Resize == "fit") || (cover && cfg.Cover.Resize == common.ImageResizeModeCrop) {
				if err := resizeSVGBox(bi, cover, cfg); err != nil {
					return bo.handleImageError(bi, "resize SVG", err, rasterizeBrokenSVG, cfg, log)
				}
			}
			return bi
		}
		if cfg.RasterizeSVG || (outputFormat.IsKindle() && cfg.RemoveTransparency) {
			log.Debug("Rasterizing SVG as requested by configuration", zap.String("id", bo.ID),
				zap.Bool("rasterize_svg", cfg.RasterizeSVG), zap.Bool("remove_transparency", cfg.RemoveTransparency))
		}
		return bo.RasterizeSVG(outputFormat, cover, cfg, log)
	}

	imageChanged := false
	img, imgType, imgDecodingErr := image.Decode(bytes.NewReader(bo.Data))
	if imgDecodingErr != nil {
		return bo.handleImageError(bi, "decode", imgDecodingErr, rasterizeBrokenSVG, cfg, log)
	}
	bi.MimeType = mime.TypeByExtension("." + imgType)
	if outputFormat == common.OutputFmtPdf && cfg.RasterFormat == common.RasterFormatJpeg {
		bi.LayoutMimeType = bi.MimeType
	}
	bi.Dim.Width = img.Bounds().Dx()
	bi.Dim.Height = img.Bounds().Dy()

	// Kobo does not support WebP. Use lossless PNG, preserving transparency
	// unless its removal is explicitly requested below.
	if outputFormat.IsKobo() && imgType == "webp" && !normalizeToJPEG {
		log.Debug("Converting WebP image to PNG for Kobo output", zap.String("id", bo.ID))
		imgType = "png"
		bi.MimeType = "image/png"
		imageChanged = true
	}

	// Scaling cover image
	if cover {
		w, h := cfg.Screen.Width, cfg.Screen.Height
		switch cfg.Cover.Resize {
		case common.ImageResizeModeNone:
		case common.ImageResizeModeKeepAR:
			if img.Bounds().Dy() >= h {
				break
			}
			resizedImg := imaging.Resize(img, 0, h, imaging.Lanczos)
			if resizedImg == nil {
				return bo.handleImageError(bi, "resize", nil, rasterizeBrokenSVG, cfg, log)
			}
			img = resizedImg
			bi.Dim.Width = img.Bounds().Dx()
			bi.Dim.Height = img.Bounds().Dy()
			imageChanged = true
		case common.ImageResizeModeStretch:
			resizedImg := imaging.Resize(img, w, h, imaging.Lanczos)
			if resizedImg == nil {
				return bo.handleImageError(bi, "resize", nil, rasterizeBrokenSVG, cfg, log)
			}
			img = resizedImg
			bi.Dim.Width = img.Bounds().Dx()
			bi.Dim.Height = img.Bounds().Dy()
			imageChanged = true
		case common.ImageResizeModeCrop:
			img = resizeImageToCoverBox(img, w, h)
			if img == nil {
				return bo.handleImageError(bi, "resize", nil, rasterizeBrokenSVG, cfg, log)
			}
			bi.Dim.Width, bi.Dim.Height = img.Bounds().Dx(), img.Bounds().Dy()
			imageChanged = true
		case common.ImageResizeModeFit:
			resizedImg := resizeImageToFitBox(img, w, h)
			if resizedImg == nil {
				return bo.handleImageError(bi, "resize", nil, rasterizeBrokenSVG, cfg, log)
			}
			img = resizedImg
			bi.Dim.Width = img.Bounds().Dx()
			bi.Dim.Height = img.Bounds().Dy()
			imageChanged = true
		}
	}

	// Scaling non-cover images
	if !cover && cfg.Resize != "fit" && cfg.ScaleFactor > 0.0 && cfg.ScaleFactor != 1.0 {
		resizedImg := imaging.Resize(img, 0, int(float64(img.Bounds().Dy())*cfg.ScaleFactor), imaging.Linear)
		if resizedImg == nil {
			return bo.handleImageError(bi, "resize", nil, rasterizeBrokenSVG, cfg, log)
		}
		img = resizedImg
		bi.Dim.Width = img.Bounds().Dx()
		bi.Dim.Height = img.Bounds().Dy()
		imageChanged = true
	}

	if !cover && cfg.Resize == "fit" && (img.Bounds().Dx() > cfg.Screen.Width || img.Bounds().Dy() > cfg.Screen.Height) {
		img = resizeImageToFitBox(img, cfg.Screen.Width, cfg.Screen.Height)
		if img == nil {
			return bo.handleImageError(bi, "resize", nil, rasterizeBrokenSVG, cfg, log)
		}
		bi.Dim.Width, bi.Dim.Height = img.Bounds().Dx(), img.Bounds().Dy()
		imageChanged = true
	}

	// Flatten transparency onto white when explicitly requested or when preparing
	// Kindle or embedded Markdown output, which this pipeline normalizes to JPEG.
	if normalizeToJPEG || cfg.RemoveTransparency {
		if imgType == "png" || imgType == "gif" || imgType == "tiff" || imgType == "webp" {
			opaque := func(im image.Image) bool {
				if oimg, ok := im.(interface{ Opaque() bool }); ok {
					return oimg.Opaque()
				}
				return true
			}(img)

			if !opaque {
				log.Debug("Removing image transparency", zap.String("id", bo.ID), zap.String("type", imgType))
				opaqueImg := image.NewRGBA(img.Bounds())
				draw.Draw(opaqueImg, img.Bounds(), &image.Uniform{color.RGBA{255, 255, 255, 255}}, image.Point{}, draw.Src)
				draw.Draw(opaqueImg, img.Bounds(), img, image.Point{}, draw.Over)
				img = opaqueImg
				imageChanged = true
				// GIF, TIFF, and WebP must be converted to PNG for re-encoding
				// since encodeImage only supports PNG and JPEG.
				// JPEG normalization overrides this below when required.
				if imgType != "png" {
					imgType = "png"
					bi.MimeType = "image/png"
					if cfg.RemoveTransparency && bi.LayoutMimeType != "" {
						// Preserve the layout of the same transparency-removal request
						// under auto, rather than undoing its existing format change.
						bi.LayoutMimeType = bi.MimeType
					}
				}
			}
		}
	}

	// Compression & image quality
	if cfg.Optimize {
		switch imgType {
		case "jpeg":
			jr, err := jpegquality.NewFromBytes(bo.Data)
			if err != nil {
				log.Warn("Unable to detect JPEG quality level, skipping...", zap.String("id", bo.ID), zap.Error(err))
				break
			}

			q := jr.Quality()
			if q <= cfg.JPEGQuality {
				log.Debug("JPEG quality level already lower than requested, skipping...",
					zap.String("id", bo.ID), zap.Int("detected", q), zap.Int("requested", cfg.JPEGQuality))
				break
			}

			log.Debug("JPEG quality level higher than requested, reencoding...",
				zap.String("id", bo.ID), zap.Int("detected", q), zap.Int("requested", cfg.JPEGQuality))

			imageChanged = true
		case "png":
			if outputFormat == common.OutputFmtPdf {
				log.Debug("Skipping non-JPEG source image optimization for output format",
					zap.String("id", bo.ID),
					zap.String("type", imgType))
				break
			}
			imageChanged = true
		}
	}

	// Required compatibility or explicit encoding policy: normalize decodable raster inputs.
	if normalizeToJPEG && imgType != "jpeg" {
		log.Debug("Converting image to jpeg for raster output",
			zap.String("id", bo.ID),
			zap.String("type", imgType), zap.String("raster_format", cfg.RasterFormat.String()))
		imgType = "jpeg"
		bi.MimeType = "image/jpeg"
		imageChanged = true
	}

	// Go's JPEG encoder writes baseline sequential JPEG. Re-encode only
	// non-baseline inputs; unchanged baseline images remain byte-identical.
	if cfg.BaselineJPEG && imgType == "jpeg" && !isBaselineJPEG(bo.Data) {
		imageChanged = true
	}

	// Force grayscale encoding even when the source image is colorful.
	if cfg.Grayscale && imgType == "jpeg" {
		imageChanged = true
	}

	if !imageChanged {
		return bi
	}

	data, err := bo.encodeImage(img, imgType, cfg, log)
	if err != nil {
		return bo.handleImageError(bi, "encode", err, rasterizeBrokenSVG, cfg, log)
	}
	bi.Data = data

	return bi
}

// RasterizeSVG prepares a raster representation when an output backend cannot use
// the source SVG. Policy logging belongs to the caller, so fallback is never
// mislabeled as an explicit rasterize_svg request.
func (bo *BinaryObject) RasterizeSVG(outputFormat common.OutputFmt, cover bool, cfg *config.ImagesConfig, log *zap.Logger) *BookImage {
	requireJPEG := outputFormat.IsKindle() ||
		(outputFormat == common.OutputFmtMd && cfg.Markdown == common.MarkdownImageModeEmbedded)
	normalizeToJPEG := requireJPEG || cfg.RasterFormat == common.RasterFormatJpeg
	preserveSVGSizing := outputFormat == common.OutputFmtPdf
	bi := &BookImage{MimeType: "image/svg+xml", Data: bo.Data, BuiltinVignette: bo.BuiltinVignette}
	targetW, targetH := cfg.Screen.Width, 0
	if preserveSVGSizing || (!cover && cfg.Resize == "fit") || (cover && cfg.Cover.Resize == common.ImageResizeModeCrop) || bytes.Equal(bo.Data, notFoundImage) || bytes.Equal(bo.Data, brokenImage) {
		targetW, targetH = 0, 0
	}
	var strokeFactor float64
	if requireJPEG && bo.BuiltinVignette {
		strokeFactor = imgutil.KindleSVGStrokeWidthFactor
	}
	rasterize := imgutil.RasterizeSVGToImage
	imgType := "jpeg"
	if !normalizeToJPEG {
		imgType = "png"
		if !cfg.RemoveTransparency {
			rasterize = imgutil.RasterizeSVGToTransparentImage
		}
	}
	img, err := rasterize(bo.Data, targetW, targetH, strokeFactor)
	if err != nil {
		return bo.handleImageError(bi, "rasterize", err, normalizeToJPEG, cfg, log)
	}
	if cover && !preserveSVGSizing {
		w, h := cfg.Screen.Width, cfg.Screen.Height
		switch cfg.Cover.Resize {
		case common.ImageResizeModeNone:
		case common.ImageResizeModeKeepAR:
			if img.Bounds().Dy() < h {
				resized := imaging.Resize(img, 0, h, imaging.Lanczos)
				if resized == nil {
					return bo.handleImageError(bi, "resize", nil, normalizeToJPEG, cfg, log)
				}
				img = resized
			}
		case common.ImageResizeModeStretch:
			resized := imaging.Resize(img, w, h, imaging.Lanczos)
			if resized == nil {
				return bo.handleImageError(bi, "resize", nil, normalizeToJPEG, cfg, log)
			}
			img = resized
		case common.ImageResizeModeCrop:
			img = resizeImageToCoverBox(img, w, h)
		case common.ImageResizeModeFit:
			img = resizeImageToFitBox(img, w, h)
		}
		if img == nil {
			return bo.handleImageError(bi, "resize", nil, normalizeToJPEG, cfg, log)
		}
	}
	if !cover && cfg.Resize != "fit" && !preserveSVGSizing && !bytes.Equal(bo.Data, brokenImage) && cfg.ScaleFactor > 0 && cfg.ScaleFactor != 1 {
		resized := imaging.Resize(img, 0, int(float64(img.Bounds().Dy())*cfg.ScaleFactor), imaging.Linear)
		if resized == nil {
			return bo.handleImageError(bi, "resize", nil, normalizeToJPEG, cfg, log)
		}
		img = resized
	}
	if !cover && !preserveSVGSizing && cfg.Resize == "fit" && (img.Bounds().Dx() > cfg.Screen.Width || img.Bounds().Dy() > cfg.Screen.Height) {
		img = resizeImageToFitBox(img, cfg.Screen.Width, cfg.Screen.Height)
		if img == nil {
			return bo.handleImageError(bi, "resize", nil, normalizeToJPEG, cfg, log)
		}
	}
	bi.Dim.Width, bi.Dim.Height = img.Bounds().Dx(), img.Bounds().Dy()
	data, err := bo.encodeImage(img, imgType, cfg, log)
	if err != nil {
		return bo.handleImageError(bi, "encode", err, normalizeToJPEG, cfg, log)
	}
	bi.Data, bi.MimeType = data, "image/"+imgType
	return bi
}

// isImageMIME returns true if the MIME type indicates an image resource
func isImageMIME(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/")
}

func resizeImageToFitBox(img image.Image, maxW, maxH int) image.Image {
	if img == nil || maxW <= 0 || maxH <= 0 {
		return nil
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}

	if float64(w)/float64(h) > float64(maxW)/float64(maxH) {
		return imaging.Resize(img, maxW, 0, imaging.Lanczos)
	}
	return imaging.Resize(img, 0, maxH, imaging.Lanczos)
}

// mimeToExt returns file extension for common image MIME types
func mimeToExt(mimeType string) string {
	// Handle common types directly to prefer standard extensions
	switch strings.ToLower(mimeType) {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/bmp":
		return "bmp"
	case "image/svg+xml":
		return "svg"
	case "image/webp":
		return "webp"
	case "image/tiff":
		return "tiff"
	}
	// Fallback to mime package for other types
	exts, err := mime.ExtensionsByType(mimeType)
	if err == nil && len(exts) > 0 {
		return strings.TrimPrefix(exts[0], ".")
	}
	return "img"
}

// resizeImageToCoverBox preserves the entire image: Crop in the reader, not here.
// The limiting side is exact; ceil keeps the other side at least screen-sized.
func resizeImageToCoverBox(img image.Image, screenW, screenH int) image.Image {
	if img == nil || screenW <= 0 || screenH <= 0 {
		return nil
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	if float64(w)/float64(h) >= float64(screenW)/float64(screenH) {
		return imaging.Resize(img, int(math.Ceil(float64(w)*float64(screenH)/float64(h))), screenH, imaging.Lanczos)
	}
	return imaging.Resize(img, screenW, int(math.Ceil(float64(h)*float64(screenW)/float64(w))), imaging.Lanczos)
}

// Inspect JPEG marker segments, skipping APP payloads (which may contain thumbnails).
func isBaselineJPEG(data []byte) bool {
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return false
	}
	for i := 2; i < len(data); {
		if data[i] != 0xff {
			return false
		}
		for i < len(data) && data[i] == 0xff {
			i++
		}
		if i >= len(data) {
			return false
		}
		marker := data[i]
		i++
		if marker == 0xc0 {
			return true
		}
		if marker >= 0xc1 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc {
			return false
		}
		if marker == 0xda || marker == 0xd9 {
			return false
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if i+2 > len(data) {
			return false
		}
		length := int(data[i])<<8 | int(data[i+1])
		if length < 2 || i+length > len(data) {
			return false
		}
		i += length
	}
	return false
}

// Native SVG keeps its vector contents; only its viewport is resized.
func resizeSVGBox(bi *BookImage, cover bool, cfg *config.ImagesConfig) error {
	w, h, err := imgutil.SVGImageSize(bi.Data, 0, 0)
	if err != nil {
		return err
	}
	if w <= 0 || h <= 0 || cfg.Screen.Width <= 0 || cfg.Screen.Height <= 0 {
		return fmt.Errorf("invalid SVG or screen dimensions")
	}
	scale := math.Min(1, math.Min(float64(cfg.Screen.Width)/float64(w), float64(cfg.Screen.Height)/float64(h)))
	if cover {
		scale = math.Max(float64(cfg.Screen.Width)/float64(w), float64(cfg.Screen.Height)/float64(h))
	}
	nw, nh := max(1, int(math.Round(float64(w)*scale))), max(1, int(math.Round(float64(h)*scale)))
	if cover {
		nw, nh = max(cfg.Screen.Width, nw), max(cfg.Screen.Height, nh)
	}
	bi.Dim.Width, bi.Dim.Height = nw, nh
	if nw == w && nh == h {
		return nil
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(bi.Data); err != nil {
		return err
	}
	root := doc.Root()
	if root == nil || root.Tag != "svg" {
		return fmt.Errorf("missing SVG root")
	}
	if root.SelectAttr("viewBox") == nil {
		root.CreateAttr("viewBox", fmt.Sprintf("0 0 %d %d", w, h))
	}
	root.CreateAttr("width", strconv.Itoa(nw))
	root.CreateAttr("height", strconv.Itoa(nh))
	bi.Data, err = doc.WriteToBytes()
	return err
}
