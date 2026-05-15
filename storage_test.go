package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func testPlugin() *StoragePlugin {
	return &StoragePlugin{
		logger: slog.Default(),
		cfg: storageConfig{
			AccountID:     "account",
			AccessKeyID:   "access",
			SecretKey:     "secret",
			Bucket:        "bucket",
			PublicBaseURL: "https://cdn.example.com",
			Environment:   "dev",
		},
		httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		})},
		memory:        map[string]storageResource{},
		adminAPIKey:   "test-admin-key",
		internalToken: "test-token",
	}
}

func TestStorageKeyIncludesRequiredParts(t *testing.T) {
	now := time.Date(2026, 5, 11, 10, 20, 30, 0, time.UTC)
	key := buildStorageKey("prod", "recharge_voucher", "user-1", "res-1", "jpg", now)
	want := "prod/recharge_voucher/user-1/2026/05/11/res-1.jpg"
	if key != want {
		t.Fatalf("storage key=%q, want %q", key, want)
	}
}

func TestValidateBusinessObject(t *testing.T) {
	if !validateBusinessObject("", "") {
		t.Fatal("空业务对象应允许")
	}
	if !validateBusinessObject("recharge_order", "RC202605110001") {
		t.Fatal("充值订单业务对象应允许")
	}
	if !validateBusinessObject("user_profile", "profile:1001") {
		t.Fatal("用户资料业务对象应允许")
	}
	if validateBusinessObject("bad type", "RC202605110001") {
		t.Fatal("格式非法的业务对象类型不应允许")
	}
}

func TestHandleUploadRejectsInvalidBusinessObjectType(t *testing.T) {
	plugin := testPlugin()
	body, contentType, err := multipartBody(map[string]string{
		"feature":              "recharge_voucher",
		"business_object_type": "bad type",
		"business_object_id":   "profile:1001",
	}, "voucher.jpg", sampleJPEG())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "00000000-0000-4000-8000-000000009006")
	rec := httptest.NewRecorder()
	plugin.handleUpload(rec, req)
	assertBusinessStatus(t, rec, 8005)
}

func TestReadImagePayloadRejectsText(t *testing.T) {
	body, contentType, err := newMultipartRequestBody("file", "bad.txt", []byte("not image"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	if err := req.ParseMultipartForm(1024); err != nil {
		t.Fatal(err)
	}
	file, header, err := req.FormFile("file")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, err = readImagePayload(file, header)
	if !errors.Is(err, errUnsupportedImage) {
		t.Fatalf("err=%v, want errUnsupportedImage", err)
	}
}

func TestHandleUploadRequiresLogin(t *testing.T) {
	plugin := testPlugin()
	body, contentType, err := newMultipartRequestBody("file", "voucher.jpg", sampleJPEG())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	plugin.handleUpload(rec, req)
	assertBusinessStatus(t, rec, 8001)
}

func TestHandleUploadSuccess(t *testing.T) {
	plugin := testPlugin()
	body, contentType, err := newMultipartRequestBody("file", "voucher.jpg", sampleJPEG())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "00000000-0000-4000-8000-000000000001")
	// multipart 字段必须写在 body 中，重建请求便于同时带文件和字段。
	body, contentType, err = multipartBody(map[string]string{"feature": "recharge_voucher"}, "voucher.jpg", sampleJPEG())
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "00000000-0000-4000-8000-000000000001")
	rec := httptest.NewRecorder()
	plugin.handleUpload(rec, req)
	assertBusinessStatus(t, rec, 0)
	if len(plugin.memory) != 1 {
		t.Fatalf("memory count=%d, want 1", len(plugin.memory))
	}
}

func TestResourceListAndDetail(t *testing.T) {
	plugin := testPlugin()
	now := time.Now().UTC()
	res := storageResource{
		ID:               "00000000-0000-4000-8000-000000000101",
		UserID:           "00000000-0000-4000-8000-000000000001",
		Feature:          "recharge_voucher",
		OriginalFilename: "voucher.jpg",
		FileExt:          "jpg",
		MimeType:         "image/jpeg",
		FileSizeBytes:    11,
		StorageKey:       "dev/recharge_voucher/u/2026/05/11/r.jpg",
		PublicURL:        "https://cdn.example.com/dev/recharge_voucher/u/2026/05/11/r.jpg",
		Status:           statusNormal,
		UploadedAt:       now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	_, err := plugin.insertResource(context.Background(), res)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/storage/resource-list?feature=recharge_voucher&page=1&page_size=20", nil)
	req.Header.Set("X-API-Key", "test-admin-key")
	rec := httptest.NewRecorder()
	plugin.handleResourceList(rec, req)
	assertBusinessStatus(t, rec, 0)

	req = httptest.NewRequest(http.MethodGet, "/api/storage/resource-detail?resource_id="+res.ID, nil)
	req.Header.Set("X-API-Key", "test-admin-key")
	rec = httptest.NewRecorder()
	plugin.handleResourceDetail(rec, req)
	assertBusinessStatus(t, rec, 0)
}

func TestResourceListAndDetailDoNotRequireModuleAdminAPIKey(t *testing.T) {
	plugin := testPlugin()
	req := httptest.NewRequest(http.MethodGet, "/api/storage/resource-list", nil)
	rec := httptest.NewRecorder()
	plugin.handleResourceList(rec, req)
	assertBusinessStatus(t, rec, 0)

	req = httptest.NewRequest(http.MethodGet, "/api/storage/resource-detail?resource_id=bad-id", nil)
	rec = httptest.NewRecorder()
	plugin.handleResourceDetail(rec, req)
	assertBusinessStatus(t, rec, 8022)
}

func TestResourceListRejectsInvalidBusinessObjectTypeFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/storage/resource-list?feature=profile_avatar&business_object_type=bad%20type&business_object_id=profile:1001", nil)
	_, status, msg := parseResourceListFilter(req)
	if status != 8012 {
		t.Fatalf("status=%d msg=%q, want 8012", status, msg)
	}
}

func TestResourceListAcceptsNonSystemBusinessObjectTypeFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/storage/resource-list?feature=profile_avatar&business_object_type=invoice&business_object_id=profile:1001", nil)
	_, status, msg := parseResourceListFilter(req)
	if status != 0 {
		t.Fatalf("status=%d msg=%q, want 0", status, msg)
	}
}

func TestHandleSelftestExecutesCases(t *testing.T) {
	t.Setenv("RUNTIME_INTERNAL_TOKEN", "test-token")
	Plugin = testPlugin()
	req := httptest.NewRequest(http.MethodPost, "/_internal/selftest/storage", nil)
	req.Header.Set("X-Internal-Token", "test-token")
	rec := httptest.NewRecorder()
	handleSelftestInternal(rec, req)
	assertBusinessStatus(t, rec, 0)

	var resp struct {
		Data struct {
			Total  int `json:"total"`
			Failed int `json:"failed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	if resp.Data.Total == 0 {
		t.Fatal("selftest 应执行 tests/*.test.json 用例")
	}
	if resp.Data.Failed != 0 {
		t.Fatalf("selftest failed=%d, body=%s", resp.Data.Failed, rec.Body.String())
	}
}

func TestSoftDeleteAndBind(t *testing.T) {
	plugin := testPlugin()
	now := time.Now().UTC()
	res := storageResource{
		ID:               "00000000-0000-4000-8000-000000000201",
		UserID:           "00000000-0000-4000-8000-000000000001",
		Feature:          "recharge_voucher",
		OriginalFilename: "voucher.jpg",
		FileExt:          "jpg",
		MimeType:         "image/jpeg",
		FileSizeBytes:    11,
		StorageKey:       "dev/recharge_voucher/u/2026/05/11/r.jpg",
		PublicURL:        "https://cdn.example.com/dev/recharge_voucher/u/2026/05/11/r.jpg",
		Status:           statusNormal,
		UploadedAt:       now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	_, _ = plugin.insertResource(context.Background(), res)
	bound, err := plugin.bindResource(context.Background(), res.ID, res.UserID, "recharge_order", "RC202605110001")
	if err != nil {
		t.Fatal(err)
	}
	if bound.BusinessObjectID != "RC202605110001" {
		t.Fatalf("BusinessObjectID=%q", bound.BusinessObjectID)
	}
	deleted, err := plugin.softDeleteResource(context.Background(), res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Status != statusDeleted || deleted.DeletedAt == nil {
		t.Fatalf("deleted status invalid: %+v", deleted)
	}
}

func TestBindRechargeOrderRejectsNonVoucherResource(t *testing.T) {
	plugin := testPlugin()
	now := time.Now().UTC()
	res := storageResource{
		ID:               "00000000-0000-4000-8000-000000000301",
		UserID:           "00000000-0000-4000-8000-000000000001",
		Feature:          "profile_avatar",
		OriginalFilename: "avatar.jpg",
		FileExt:          "jpg",
		MimeType:         "image/jpeg",
		FileSizeBytes:    11,
		StorageKey:       "dev/profile_avatar/u/2026/05/11/r.jpg",
		PublicURL:        "https://cdn.example.com/dev/profile_avatar/u/2026/05/11/r.jpg",
		Status:           statusNormal,
		UploadedAt:       now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	_, _ = plugin.insertResource(context.Background(), res)
	_, err := plugin.bindResource(context.Background(), res.ID, res.UserID, "recharge_order", "RC202605110001")
	if !errors.Is(err, errResourceNotFound) {
		t.Fatalf("err=%v, want errResourceNotFound", err)
	}
}

func assertBusinessStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status=%d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	if resp.Status != want {
		t.Fatalf("business status=%d, want %d, body=%s", resp.Status, want, rec.Body.String())
	}
}

func multipartBody(fields map[string]string, filename string, content []byte) (*bytes.Buffer, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(content); err != nil {
		return nil, "", err
	}
	contentType := writer.FormDataContentType()
	return body, contentType, writer.Close()
}
