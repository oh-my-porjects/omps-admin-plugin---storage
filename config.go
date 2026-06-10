package main

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	statusNormal  = "normal"
	statusDeleted = "deleted"
	statusCleaned = "cleaned"

	featureRechargeVoucher = "recharge_voucher"
	objectRechargeOrder    = "recharge_order"
	objectUserProfile      = "user_profile"
	systemOwner            = "system"
)

var (
	errR2ConfigMissing = errors.New("r2 config missing")
	// 平台历史短 ID 标准 12 字符 base62，新上传资源 ID 改为 UUID 后仍保留读取兼容。
	shortIDRE = regexp.MustCompile(`^[A-Za-z0-9]{12}$`)
	uuidRE    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	tokenRE   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,100}$`)
	envRE     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
)

type storageConfig struct {
	AccountID     string
	AccessKeyID   string
	SecretKey     string
	Bucket        string
	PublicBaseURL string
	Environment   string
}

func loadStorageConfig(cfg map[string]string) storageConfig {
	env := strings.TrimSpace(cfg["STORAGE_ENVIRONMENT"])
	if env == "" {
		env = "dev"
	}
	return storageConfig{
		AccountID:     strings.TrimSpace(cfg["STORAGE_R2_ACCOUNT_ID"]),
		AccessKeyID:   strings.TrimSpace(cfg["STORAGE_R2_ACCESS_KEY_ID"]),
		SecretKey:     strings.TrimSpace(cfg["STORAGE_R2_SECRET_ACCESS_KEY"]),
		Bucket:        strings.TrimSpace(cfg["STORAGE_R2_BUCKET"]),
		PublicBaseURL: normalizePublicBaseURL(cfg["STORAGE_R2_PUBLIC_BASE_URL"]),
		Environment:   env,
	}
}

func normalizePublicBaseURL(raw string) string {
	v := strings.TrimRight(strings.TrimSpace(raw), "/")
	if v == "" || strings.Contains(v, "://") {
		return v
	}
	return "https://" + v
}

func (c storageConfig) validForUpload() bool {
	if c.AccountID == "" || c.AccessKeyID == "" || c.SecretKey == "" || c.Bucket == "" || c.PublicBaseURL == "" {
		return false
	}
	if !envRE.MatchString(c.Environment) {
		return false
	}
	u, err := url.Parse(c.PublicBaseURL)
	return err == nil && u.Scheme != "" && u.Host != ""
}

func validateFeature(feature string) bool {
	return feature == featureRechargeVoucher
}

func validateFeatureFilter(feature string) bool {
	return validateFilterToken(feature, 50)
}

func validateBusinessObjectType(objectType string) bool {
	switch objectType {
	case objectRechargeOrder, objectUserProfile:
		return true
	default:
		return false
	}
}

func validateBusinessObject(objectType, objectID string) bool {
	if objectType == "" && objectID == "" {
		return true
	}
	if objectType == "" || objectID == "" {
		return false
	}
	return validateBusinessObjectType(objectType) && len(objectID) <= 100 && tokenRE.MatchString(objectID)
}

func validateBusinessObjectFilter(objectType, objectID string) bool {
	if objectType != "" && !validateFilterToken(objectType, 50) {
		return false
	}
	if objectID != "" && (len(objectID) > 100 || !tokenRE.MatchString(objectID)) {
		return false
	}
	return true
}

func validateFilterToken(v string, maxLen int) bool {
	return len(v) >= 1 && len(v) <= maxLen && tokenRE.MatchString(v)
}

func validateStatus(status string) bool {
	return status == "" || status == statusNormal || status == statusDeleted || status == statusCleaned
}

// validateShortID 校验 12 字符 base62 ID 格式
func validateShortID(v string) bool {
	return shortIDRE.MatchString(v)
}

// validateUUID 接受新标准 UUID 和历史 12 位短 ID，避免旧资源无法查询、绑定或软删除。
func validateUUID(v string) bool { return uuidRE.MatchString(v) || validateShortID(v) }

func formatTimePtr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func unixTimePtr(t *time.Time) int64 {
	if t == nil || t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}
