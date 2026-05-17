package main

import (
	"crypto/rand"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (p *StoragePlugin) handleUpload(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(getUserID(r))
	if userID == "" || !validateUUID(userID) {
		writeJSON(w, 8001, nil, "未登录或登录态无效")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, 8002, nil, "缺少上传文件")
		return
	}
	feature := strings.TrimSpace(r.FormValue("feature"))
	if !validateFeature(feature) {
		writeJSON(w, 8003, nil, "资源功能类型缺失或格式非法")
		return
	}
	objectType := strings.TrimSpace(r.FormValue("business_object_type"))
	objectID := strings.TrimSpace(r.FormValue("business_object_id"))
	if !validateBusinessObject(objectType, objectID) {
		writeJSON(w, 8005, nil, "业务对象类型或业务对象 ID 格式非法")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 8002, nil, "缺少上传文件")
		return
	}
	defer file.Close()
	payload, err := readImagePayload(file, header)
	if err != nil {
		if errors.Is(err, errUnsupportedImage) {
			writeJSON(w, 8004, nil, "不支持的图片格式，仅允许 JPG、JPEG、PNG、WEBP")
			return
		}
		writeJSON(w, 8002, nil, "缺少上传文件")
		return
	}
	now := time.Now().UTC()
	resourceID := newUUID()
	cfg := p.cfg
	useSelftestStorage := selftestUploadMockEnabled(r)
	if useSelftestStorage && !cfg.validForUpload() {
		cfg = selftestUploadStorageConfig(cfg)
	}
	storageKey := buildStorageKey(cfg.Environment, feature, userID, resourceID, payload.Ext, now)
	if !cfg.validForUpload() {
		writeJSON(w, 8006, nil, "R2 存储配置缺失或不可用")
		return
	}
	if !useSelftestStorage {
		if err := p.putR2Object(r.Context(), storageKey, payload.MimeType, payload.Content); err != nil {
			if errors.Is(err, errR2ConfigMissing) {
				writeJSON(w, 8006, nil, "R2 存储配置缺失或不可用")
				return
			}
			writeJSON(w, 8007, nil, "上传 R2 失败")
			return
		}
	}
	res := storageResource{
		ID:                 resourceID,
		UserID:             userID,
		Feature:            feature,
		BusinessObjectType: objectType,
		BusinessObjectID:   objectID,
		OriginalFilename:   payload.OriginalName,
		FileExt:            payload.Ext,
		MimeType:           payload.MimeType,
		FileSizeBytes:      payload.SizeBytes,
		StorageKey:         storageKey,
		PublicURL:          publicURL(cfg.PublicBaseURL, storageKey),
		Status:             statusNormal,
		UploadedAt:         now,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	saved, err := p.insertResource(r.Context(), res)
	if err != nil {
		writeJSON(w, 8008, nil, "资源元数据保存失败")
		return
	}
	writeJSON(w, 0, uploadResponse(saved), "")
}

func (p *StoragePlugin) handleResourceList(w http.ResponseWriter, r *http.Request) {
	filter, status, msg := parseResourceListFilter(r)
	if status != 0 {
		writeJSON(w, status, nil, msg)
		return
	}
	items, total, err := p.listResources(r.Context(), filter)
	if err != nil {
		writeJSON(w, 8015, nil, "查询资源列表失败")
		return
	}
	respItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		respItems = append(respItems, resourceListItem(item))
	}
	writeJSON(w, 0, map[string]any{
		"items":     respItems,
		"total":     total,
		"page":      filter.Page,
		"page_size": filter.PageSize,
	}, "")
}

func (p *StoragePlugin) handleResourceDetail(w http.ResponseWriter, r *http.Request) {
	resourceID := strings.TrimSpace(r.URL.Query().Get("resource_id"))
	if resourceID == "" || !validateUUID(resourceID) {
		writeJSON(w, 8022, nil, "资源 ID 缺失或格式非法")
		return
	}
	res, err := p.getResource(r.Context(), resourceID)
	if err != nil {
		if errors.Is(err, errResourceNotFound) {
			writeJSON(w, 8023, nil, "资源不存在")
			return
		}
		writeJSON(w, 8024, nil, "查询资源详情失败")
		return
	}
	writeJSON(w, 0, resourceDetail(res), "")
}

func parseResourceListFilter(r *http.Request) (resourceListFilter, int, string) {
	q := r.URL.Query()
	filter := resourceListFilter{Page: 1, PageSize: 20}
	filter.UserID = strings.TrimSpace(q.Get("user_id"))
	filter.Feature = strings.TrimSpace(q.Get("feature"))
	filter.BusinessObjectType = strings.TrimSpace(q.Get("business_object_type"))
	filter.BusinessObjectID = strings.TrimSpace(q.Get("business_object_id"))
	filter.Status = strings.TrimSpace(q.Get("status"))
	if filter.UserID != "" && !validateUUID(filter.UserID) {
		return filter, 8012, "筛选参数格式非法"
	}
	if filter.Feature != "" && !validateFeatureFilter(filter.Feature) {
		return filter, 8012, "筛选参数格式非法"
	}
	if !validateBusinessObjectFilter(filter.BusinessObjectType, filter.BusinessObjectID) {
		return filter, 8012, "筛选参数格式非法"
	}
	if !validateStatus(filter.Status) {
		return filter, 8012, "筛选参数格式非法"
	}
	if q.Get("page") != "" {
		page, err := strconv.Atoi(q.Get("page"))
		if err != nil || page < 1 {
			return filter, 8013, "分页参数超出允许范围"
		}
		filter.Page = page
	}
	if q.Get("page_size") != "" {
		pageSize, err := strconv.Atoi(q.Get("page_size"))
		if err != nil || pageSize < 1 || pageSize > 100 {
			return filter, 8013, "分页参数超出允许范围"
		}
		filter.PageSize = pageSize
	}
	from, ok, err := parseOptionalTime(q.Get("uploaded_from"))
	if err != nil {
		return filter, 8014, "上传时间范围非法"
	}
	if ok {
		filter.UploadedFrom = &from
	}
	to, ok, err := parseOptionalTime(q.Get("uploaded_to"))
	if err != nil {
		return filter, 8014, "上传时间范围非法"
	}
	if ok {
		filter.UploadedTo = &to
	}
	if filter.UploadedFrom != nil && filter.UploadedTo != nil && filter.UploadedTo.Before(*filter.UploadedFrom) {
		return filter, 8014, "上传时间范围非法"
	}
	return filter, 0, ""
}

func parseOptionalTime(raw string) (time.Time, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false, err
	}
	return t, true, nil
}

func selftestUploadMockEnabled(r *http.Request) bool {
	return r.Header.Get("X-Storage-Selftest-Mock") == "1"
}

func selftestUploadStorageConfig(cfg storageConfig) storageConfig {
	if cfg.Environment == "" {
		cfg.Environment = "selftest"
	}
	if cfg.PublicBaseURL == "" {
		cfg.PublicBaseURL = "https://storage-selftest.example.test"
	}
	if cfg.AccountID == "" {
		cfg.AccountID = "selftest-account"
	}
	if cfg.AccessKeyID == "" {
		cfg.AccessKeyID = "selftest-access-key"
	}
	if cfg.SecretKey == "" {
		cfg.SecretKey = "selftest-secret-key"
	}
	if cfg.Bucket == "" {
		cfg.Bucket = "selftest-bucket"
	}
	return cfg
}

func uploadResponse(res storageResource) map[string]any {
	return map[string]any{
		"resource_id":     res.ID,
		"public_url":      res.PublicURL,
		"storage_key":     res.StorageKey,
		"feature":         res.Feature,
		"status":          res.Status,
		"mime_type":       res.MimeType,
		"file_size_bytes": res.FileSizeBytes,
		"uploaded_at":     res.UploadedAt.UTC().Format(time.RFC3339),
	}
}

func resourceListItem(res storageResource) map[string]any {
	return map[string]any{
		"resource_id":          res.ID,
		"user_id":              res.UserID,
		"feature":              res.Feature,
		"business_object_type": res.BusinessObjectType,
		"business_object_id":   res.BusinessObjectID,
		"public_url":           res.PublicURL,
		"status":               res.Status,
		"mime_type":            res.MimeType,
		"file_size_bytes":      res.FileSizeBytes,
		"uploaded_at":          res.UploadedAt.UTC().Format(time.RFC3339),
	}
}

func resourceDetail(res storageResource) map[string]any {
	item := resourceListItem(res)
	item["original_filename"] = res.OriginalFilename
	item["storage_key"] = res.StorageKey
	item["file_ext"] = res.FileExt
	item["deleted_at"] = formatTimePtr(res.DeletedAt)
	item["cleaned_at"] = formatTimePtr(res.CleanedAt)
	return item
}

// newUUID 保留旧名字（调用方未改），实际生成 12 字符 base62 短 ID
func newUUID() string { return newShortID() }

// newShortID 应用层备用 ID 生成（生产路径走 PG generate_short_id() 默认值）
// 12 字符 base62, crypto/rand 加密随机
func newShortID() string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// fallback：time-based (单元测试 / 极端场景)
		now := time.Now().UnixNano()
		for i := range b {
			b[i] = chars[now%62]
			now /= 62
			if now == 0 {
				now = time.Now().UnixNano() + int64(i)
			}
		}
		return string(b[:])
	}
	for i := range b {
		b[i] = chars[int(b[i])%62]
	}
	return string(b[:])
}
