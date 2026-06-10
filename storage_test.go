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
		memory: map[string]storageResource{},
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

func TestLoadStorageConfigNormalizesPublicBaseURL(t *testing.T) {
	cfg := loadStorageConfig(map[string]string{
		"STORAGE_R2_ACCOUNT_ID":        "account",
		"STORAGE_R2_ACCESS_KEY_ID":     "access",
		"STORAGE_R2_SECRET_ACCESS_KEY": "secret",
		"STORAGE_R2_BUCKET":            "bucket",
		"STORAGE_R2_PUBLIC_BASE_URL":   "cdn.example.com/",
	})
	if cfg.PublicBaseURL != "https://cdn.example.com" {
		t.Fatalf("PublicBaseURL=%q", cfg.PublicBaseURL)
	}
	if !cfg.validForUpload() {
		t.Fatal("裸域名补 https 后应通过上传配置校验")
	}
}

func TestHandleUploadRejectsInvalidBusinessObjectType(t *testing.T) {
	plugin := testPlugin()
	body, contentType, err := multipartBody(map[string]string{
		"feature":              "recharge_voucher",
		"business_object_type": "bad type",
		"business_object_id":   "profile:1001",
	}, "file", "voucher.jpg", sampleJPEG())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "User00009006")
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

func TestHandleUploadAllowsAdminProxyAccount(t *testing.T) {
	plugin := testPlugin()
	body, contentType, err := multipartBody(map[string]string{"feature": "recharge_voucher"}, "file", "voucher.jpg", sampleJPEG())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "cb08ee6a")
	req.Header.Set("X-Admin-Session-Token", "admin-session")
	rec := httptest.NewRecorder()
	plugin.handleUpload(rec, req)
	assertBusinessStatus(t, rec, 0)
	if len(plugin.memory) != 1 {
		t.Fatalf("memory count=%d, want 1", len(plugin.memory))
	}
	for _, res := range plugin.memory {
		if res.UserID != systemOwner {
			t.Fatalf("user_id=%q, want %q", res.UserID, systemOwner)
		}
	}
}

func TestHandleUploadRejectsInvalidUserWithoutAdminProxy(t *testing.T) {
	plugin := testPlugin()
	body, contentType, err := multipartBody(map[string]string{"feature": "recharge_voucher"}, "file", "voucher.jpg", sampleJPEG())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "cb08ee6a")
	rec := httptest.NewRecorder()
	plugin.handleUpload(rec, req)
	assertBusinessStatus(t, rec, 8001)
}

func TestPutR2ObjectReturnsStatusError(t *testing.T) {
	plugin := testPlugin()
	plugin.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 403,
			Body:       io.NopCloser(strings.NewReader("<Error><Code>AccessDenied</Code></Error>")),
			Header:     make(http.Header),
		}, nil
	})}
	err := plugin.putR2Object(context.Background(), "dev/recharge_voucher/system/res.jpg", "image/jpeg", sampleJPEG())
	var r2Err *r2ObjectError
	if !errors.As(err, &r2Err) {
		t.Fatalf("err=%T %v, want *r2ObjectError", err, err)
	}
	if r2Err.Operation != "put" || r2Err.StatusCode != 403 || !strings.Contains(r2Err.Message, "AccessDenied") {
		t.Fatalf("r2Err=%+v, want put status 403 AccessDenied", r2Err)
	}
}

func TestHandleUploadSuccess(t *testing.T) {
	plugin := testPlugin()
	body, contentType, err := newMultipartRequestBody("file", "voucher.jpg", sampleJPEG())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "User00000001")
	// multipart 字段必须写在 body 中，重建请求便于同时带文件和字段。
	body, contentType, err = multipartBody(map[string]string{"feature": "recharge_voucher"}, "file", "voucher.jpg", sampleJPEG())
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "User00000001")
	rec := httptest.NewRecorder()
	plugin.handleUpload(rec, req)
	assertBusinessStatus(t, rec, 0)
	if len(plugin.memory) != 1 {
		t.Fatalf("memory count=%d, want 1", len(plugin.memory))
	}
	var resp struct {
		Status int `json:"status"`
		Data   struct {
			BatchID   string `json:"batch_id"`
			Resources []struct {
				ResourceID string `json:"resource_id"`
				UploadedTS int64  `json:"uploaded_ts"`
			} `json:"resources"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.BatchID == "" || len(resp.Data.Resources) != 1 || resp.Data.Resources[0].UploadedTS == 0 {
		t.Fatalf("upload response missing batch fields: %s", rec.Body.String())
	}
}

func TestHandleUploadAcceptsMultipleFilesAndRejectsDuplicates(t *testing.T) {
	plugin := testPlugin()
	body, contentType, err := multipartBodyFiles(map[string]string{"feature": "recharge_voucher"}, "files", []testUploadFile{
		{Filename: "voucher-a.jpg", Content: sampleJPEG()},
		{Filename: "voucher-b.jpg", Content: append(sampleJPEG(), 0x00)},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "User00000001")
	rec := httptest.NewRecorder()
	plugin.handleUpload(rec, req)
	assertBusinessStatus(t, rec, 0)
	if len(plugin.memory) != 2 {
		t.Fatalf("memory count=%d, want 2", len(plugin.memory))
	}

	body, contentType, err = multipartBodyFiles(map[string]string{"feature": "recharge_voucher"}, "files", []testUploadFile{
		{Filename: "same-a.jpg", Content: sampleJPEG()},
		{Filename: "same-b.jpg", Content: sampleJPEG()},
	})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "User00000001")
	rec = httptest.NewRecorder()
	plugin.handleUpload(rec, req)
	assertBusinessStatus(t, rec, 8002)
}

func TestRepeatedUploadReplacesCurrentBatch(t *testing.T) {
	plugin := testPlugin()
	fields := map[string]string{
		"feature":              "recharge_voucher",
		"business_object_type": "recharge_order",
		"business_object_id":   "RC202606100001",
	}
	body, contentType, err := multipartBody(map[string]string{
		"feature":              fields["feature"],
		"business_object_type": fields["business_object_type"],
		"business_object_id":   fields["business_object_id"],
	}, "file", "old.jpg", sampleJPEG())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "User00000001")
	rec := httptest.NewRecorder()
	plugin.handleUpload(rec, req)
	assertBusinessStatus(t, rec, 0)

	body, contentType, err = multipartBodyFiles(fields, "files", []testUploadFile{
		{Filename: "new-a.jpg", Content: append(sampleJPEG(), 0x01)},
		{Filename: "new-b.jpg", Content: append(sampleJPEG(), 0x02)},
	})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/storage/upload", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-User-ID", "User00000001")
	rec = httptest.NewRecorder()
	plugin.handleUpload(rec, req)
	assertBusinessStatus(t, rec, 0)

	current := 0
	history := 0
	for _, res := range plugin.memory {
		if res.BusinessObjectID == fields["business_object_id"] {
			history++
			if res.IsCurrent {
				current++
			}
		}
	}
	if history != 3 || current != 2 {
		t.Fatalf("history=%d current=%d, want history 3 current 2", history, current)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/storage/admin/resource-list?feature=recharge_voucher&business_object_type=recharge_order&business_object_id=RC202606100001", nil)
	rec = httptest.NewRecorder()
	plugin.handleResourceList(rec, req)
	assertListTotal(t, rec, 2)

	req = httptest.NewRequest(http.MethodGet, "/api/storage/admin/resource-list?feature=recharge_voucher&business_object_type=recharge_order&business_object_id=RC202606100001&include_history=true", nil)
	rec = httptest.NewRecorder()
	plugin.handleResourceList(rec, req)
	assertListTotal(t, rec, 3)
}

func TestResourceListAndDetail(t *testing.T) {
	plugin := testPlugin()
	now := time.Now().UTC()
	res := storageResource{
		ID:               "Res000000101",
		UploadBatchID:    "Bat000000101",
		IsCurrent:        true,
		UserID:           "User00000001",
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
	req := httptest.NewRequest(http.MethodGet, "/api/storage/admin/resource-list?feature=recharge_voucher&page=1&page_size=20", nil)
	req.Header.Set("X-API-Key", "test-admin-key")
	rec := httptest.NewRecorder()
	plugin.handleResourceList(rec, req)
	assertBusinessStatus(t, rec, 0)

	req = httptest.NewRequest(http.MethodGet, "/api/storage/admin/resource-detail?resource_id="+res.ID, nil)
	req.Header.Set("X-API-Key", "test-admin-key")
	rec = httptest.NewRecorder()
	plugin.handleResourceDetail(rec, req)
	assertBusinessStatus(t, rec, 0)
}

func TestResourceListAndDetailDoNotRequireModuleAdminAPIKey(t *testing.T) {
	plugin := testPlugin()
	req := httptest.NewRequest(http.MethodGet, "/api/storage/admin/resource-list", nil)
	rec := httptest.NewRecorder()
	plugin.handleResourceList(rec, req)
	assertBusinessStatus(t, rec, 0)

	req = httptest.NewRequest(http.MethodGet, "/api/storage/admin/resource-detail?resource_id=bad-id", nil)
	rec = httptest.NewRecorder()
	plugin.handleResourceDetail(rec, req)
	assertBusinessStatus(t, rec, 8022)
}

func TestResourceListRejectsInvalidBusinessObjectTypeFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/storage/admin/resource-list?feature=profile_avatar&business_object_type=bad%20type&business_object_id=profile:1001", nil)
	_, status, msg := parseResourceListFilter(req)
	if status != 8012 {
		t.Fatalf("status=%d msg=%q, want 8012", status, msg)
	}
}

func TestResourceListAcceptsNonSystemBusinessObjectTypeFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/storage/admin/resource-list?feature=profile_avatar&business_object_type=invoice&business_object_id=profile:1001", nil)
	_, status, msg := parseResourceListFilter(req)
	if status != 0 {
		t.Fatalf("status=%d msg=%q, want 0", status, msg)
	}
}

func TestHandleSelftestReportsDeprecatedEndpoint(t *testing.T) {
	t.Setenv("RUNTIME_INTERNAL_TOKEN", "test-token")
	Plugin = testPlugin()
	req := httptest.NewRequest(http.MethodPost, "/_internal/selftest/storage", nil)
	req.Header.Set("X-Internal-Token", "test-token")
	rec := httptest.NewRecorder()
	handleSelftestInternal(rec, req)
	assertBusinessStatus(t, rec, 1)

	var resp struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	if !strings.Contains(resp.Msg, "selftest 端点已废弃") {
		t.Fatalf("msg=%q, want deprecated selftest message", resp.Msg)
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

func assertListTotal(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status=%d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Status int `json:"status"`
		Data   struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应 JSON 解析失败: %v", err)
	}
	if resp.Status != 0 || resp.Data.Total != want {
		t.Fatalf("status=%d total=%d, want status 0 total %d, body=%s", resp.Status, resp.Data.Total, want, rec.Body.String())
	}
}

type testUploadFile struct {
	Filename string
	Content  []byte
}

func multipartBody(fields map[string]string, field, filename string, content []byte) (*bytes.Buffer, string, error) {
	return multipartBodyFiles(fields, field, []testUploadFile{{Filename: filename, Content: content}})
}

func multipartBodyFiles(fields map[string]string, field string, files []testUploadFile) (*bytes.Buffer, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}
	for _, file := range files {
		part, err := writer.CreateFormFile(field, file.Filename)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(file.Content); err != nil {
			return nil, "", err
		}
	}
	contentType := writer.FormDataContentType()
	return body, contentType, writer.Close()
}
