package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
	if !uuidRE.MatchString(resp.Data.Resources[0].ResourceID) {
		t.Fatalf("resource_id=%q, want standard UUID", resp.Data.Resources[0].ResourceID)
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
	var resp struct {
		Data struct {
			Resources []struct {
				ResourceID string `json:"resource_id"`
			} `json:"resources"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Resources) != 2 {
		t.Fatalf("resources count=%d, want 2", len(resp.Data.Resources))
	}
	firstID := resp.Data.Resources[0].ResourceID
	secondID := resp.Data.Resources[1].ResourceID
	if !uuidRE.MatchString(firstID) || !uuidRE.MatchString(secondID) {
		t.Fatalf("resource ids=%q,%q, want standard UUIDs", firstID, secondID)
	}
	if firstID == secondID {
		t.Fatalf("multi upload resource IDs must be unique: %q", firstID)
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
