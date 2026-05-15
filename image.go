package main

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
)

var errUnsupportedImage = errors.New("unsupported image")

type imagePayload struct {
	Content          []byte
	OriginalName     string
	Ext              string
	MimeType         string
	SizeBytes        int64
	DetectedFromBody bool
}

func readImagePayload(file multipart.File, header *multipart.FileHeader) (imagePayload, error) {
	if file == nil || header == nil {
		return imagePayload{}, errors.New("missing file")
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return imagePayload{}, err
	}
	if len(content) == 0 {
		return imagePayload{}, errUnsupportedImage
	}
	mimeType := http.DetectContentType(content)
	ext, ok := extByMime(mimeType)
	if !ok {
		return imagePayload{}, errUnsupportedImage
	}
	nameExt := normalizeExt(filepath.Ext(header.Filename))
	if nameExt != "" {
		if mimeByExt(nameExt) == "" {
			return imagePayload{}, errUnsupportedImage
		}
		// JPEG 的 jpg/jpeg 扩展名都接受，以实际 MIME 作为最终类型。
		if !(mimeType == "image/jpeg" && (nameExt == "jpg" || nameExt == "jpeg")) && mimeByExt(nameExt) != mimeType {
			return imagePayload{}, errUnsupportedImage
		}
	}
	return imagePayload{
		Content:          content,
		OriginalName:     sanitizeFilename(header.Filename),
		Ext:              ext,
		MimeType:         mimeType,
		SizeBytes:        int64(len(content)),
		DetectedFromBody: true,
	}, nil
}

func extByMime(mimeType string) (string, bool) {
	switch mimeType {
	case "image/jpeg":
		return "jpg", true
	case "image/png":
		return "png", true
	case "image/webp":
		return "webp", true
	default:
		return "", false
	}
}

func mimeByExt(ext string) string {
	switch normalizeExt(ext) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	default:
		return ""
	}
}

func normalizeExt(ext string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
}

func sanitizeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "\x00", "")
	if name == "." || name == "/" || name == "" {
		return "upload"
	}
	if len(name) > 255 {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		limit := 255 - len(ext)
		if limit < 1 {
			return name[:255]
		}
		return base[:min(len(base), limit)] + ext
	}
	return name
}

func sampleJPEG() []byte {
	return []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00, 0x01, 0x02, 0xff, 0xd9}
}

func newMultipartRequestBody(field, filename string, content []byte) (*bytes.Buffer, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(content); err != nil {
		return nil, "", err
	}
	return body, writer.FormDataContentType(), writer.Close()
}
