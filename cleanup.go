package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

const retentionConfigKey = "storage.resource_delete_retention_days"

func registerStorageScheduledTasks() {
	ScheduledTasks["cleanup_deleted_resources"] = func(emit func(line string)) error {
		return Plugin.cleanupDeletedResources(context.Background(), emit)
	}
}

func (p *StoragePlugin) cleanupDeletedResources(ctx context.Context, emit func(line string)) error {
	retentionDays := p.readRetentionDays(ctx)
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	resources, err := p.deletedResourcesBefore(ctx, cutoff)
	if err != nil {
		return err
	}
	emit(fmt.Sprintf("发现 %d 个待清理资源，保留期 %d 天", len(resources), retentionDays))
	cleaned := 0
	for _, res := range resources {
		if err := p.deleteR2Object(ctx, res.StorageKey); err != nil {
			emit(fmt.Sprintf("资源 %s 清理失败: %v", res.ID, err))
			continue
		}
		if err := p.markResourceCleaned(ctx, res.ID); err != nil {
			emit(fmt.Sprintf("资源 %s 状态更新失败: %v", res.ID, err))
			continue
		}
		cleaned++
	}
	emit(fmt.Sprintf("成功清理 %d 个资源", cleaned))
	return nil
}

func (p *StoragePlugin) readRetentionDays(ctx context.Context) int {
	if p.db == nil {
		return 7
	}
	var raw string
	err := p.db.QueryRowContext(ctx, `SELECT current_value::text FROM global_configs_items WHERE config_key=$1`, retentionConfigKey).Scan(&raw)
	if err != nil {
		return 7
	}
	var n int
	if err := json.Unmarshal([]byte(raw), &n); err == nil && n >= 1 && n <= 365 {
		return n
	}
	var f float64
	if err := json.Unmarshal([]byte(raw), &f); err == nil {
		n = int(f)
		if float64(n) == f && n >= 1 && n <= 365 {
			return n
		}
	}
	if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 1 && parsed <= 365 {
		return parsed
	}
	return 7
}

func (p *StoragePlugin) deletedResourcesBefore(ctx context.Context, cutoff time.Time) ([]storageResource, error) {
	if p.db != nil {
		rows, err := p.db.QueryContext(ctx, `
			SELECT id::text, upload_batch_id, is_current, COALESCE(user_id::text, ''), feature, business_object_type, business_object_id,
				original_filename, file_ext, mime_type, file_size_bytes, storage_key, public_url, status,
				uploaded_at, deleted_at, cleaned_at, created_at, updated_at
			FROM storage_resources
			WHERE status='deleted' AND deleted_at IS NOT NULL AND deleted_at <= $1
			ORDER BY deleted_at ASC LIMIT 200`, cutoff)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		resources := []storageResource{}
		for rows.Next() {
			res, err := scanResource(rows)
			if err != nil {
				return nil, err
			}
			resources = append(resources, res)
		}
		return resources, rows.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	resources := []storageResource{}
	for _, res := range p.memory {
		if res.Status == statusDeleted && res.DeletedAt != nil && !res.DeletedAt.After(cutoff) {
			resources = append(resources, res)
		}
	}
	return resources, nil
}

func (p *StoragePlugin) markResourceCleaned(ctx context.Context, resourceID string) error {
	now := time.Now().UTC()
	if p.db != nil {
		result, err := p.db.ExecContext(ctx, `
			UPDATE storage_resources SET status='cleaned', cleaned_at=$2, updated_at=$2
			WHERE id=$1 AND status='deleted'`, resourceID, now)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil && !errorsIsUnsupportedRowsAffected(err) {
			return err
		}
		if affected == 0 {
			return sql.ErrNoRows
		}
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	res, ok := p.memory[resourceID]
	if !ok || res.Status != statusDeleted {
		return sql.ErrNoRows
	}
	res.Status = statusCleaned
	res.CleanedAt = &now
	res.UpdatedAt = now
	p.memory[resourceID] = res
	return nil
}

func errorsIsUnsupportedRowsAffected(err error) bool {
	return err != nil && err.Error() == "RowsAffected is not supported"
}
