package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

func buildStorageKey(environment, feature, ownerID, resourceID, ext string, now time.Time) string {
	if ownerID == "" {
		ownerID = systemOwner
	}
	day := now.UTC()
	return path.Join(
		environment,
		feature,
		ownerID,
		day.Format("2006"),
		day.Format("01"),
		day.Format("02"),
		resourceID+"."+ext,
	)
}

func publicURL(baseURL, storageKey string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(storageKey, "/")
}

func (p *StoragePlugin) putR2Object(ctx context.Context, storageKey, mimeType string, content []byte) error {
	if !p.cfg.validForUpload() {
		return errR2ConfigMissing
	}
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com/%s/%s", p.cfg.AccountID, p.cfg.Bucket, escapePath(storageKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(content))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mimeType)
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(content)))
	signR2Request(req, p.cfg, content)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("r2 put status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (p *StoragePlugin) deleteR2Object(ctx context.Context, storageKey string) error {
	if !p.cfg.validForUpload() {
		return errR2ConfigMissing
	}
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com/%s/%s", p.cfg.AccountID, p.cfg.Bucket, escapePath(storageKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	signR2Request(req, p.cfg, nil)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("r2 delete status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func signR2Request(req *http.Request, cfg storageConfig, body []byte) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256Hex(body)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	canonicalHeaders := "host:" + req.URL.Host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := dateStamp + "/auto/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signature := hex.EncodeToString(hmacSHA256(signingKey(cfg.SecretKey, dateStamp), []byte(stringToSign)))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+cfg.AccessKeyID+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func signingKey(secret, dateStamp string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte("auto"))
	kService := hmacSHA256(kRegion, []byte("s3"))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func escapePath(v string) string {
	parts := strings.Split(v, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
