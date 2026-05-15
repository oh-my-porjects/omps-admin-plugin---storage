package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	errResourceNotFound = errors.New("resource not found")
	errResourceConflict = errors.New("resource conflict")
)

type storageResource struct {
	ID                 string
	UserID             string
	Feature            string
	BusinessObjectType string
	BusinessObjectID   string
	OriginalFilename   string
	FileExt            string
	MimeType           string
	FileSizeBytes      int64
	StorageKey         string
	PublicURL          string
	Status             string
	UploadedAt         time.Time
	DeletedAt          *time.Time
	CleanedAt          *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type resourceListFilter struct {
	UserID             string
	Feature            string
	BusinessObjectType string
	BusinessObjectID   string
	Status             string
	UploadedFrom       *time.Time
	UploadedTo         *time.Time
	Page               int
	PageSize           int
}

func (p *StoragePlugin) ensureSchema(ctx context.Context) error {
	if p.db == nil {
		return nil
	}
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS storage_resources (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID,
			feature TEXT NOT NULL,
			business_object_type TEXT NOT NULL DEFAULT '',
			business_object_id TEXT NOT NULL DEFAULT '',
			original_filename TEXT NOT NULL,
			file_ext TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			file_size_bytes BIGINT NOT NULL CHECK (file_size_bytes > 0),
			storage_key TEXT NOT NULL UNIQUE,
			public_url TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'normal' CHECK (status IN ('normal', 'deleted', 'cleaned')),
			uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			deleted_at TIMESTAMPTZ,
			cleaned_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS idx_storage_resources_user ON storage_resources(user_id);
		CREATE INDEX IF NOT EXISTS idx_storage_resources_feature ON storage_resources(feature);
		CREATE INDEX IF NOT EXISTS idx_storage_resources_object ON storage_resources(business_object_type, business_object_id);
		CREATE INDEX IF NOT EXISTS idx_storage_resources_status_deleted ON storage_resources(status, deleted_at);
		CREATE UNIQUE INDEX IF NOT EXISTS uniq_storage_recharge_order_voucher
			ON storage_resources(feature, business_object_type, business_object_id)
			WHERE feature='recharge_voucher' AND business_object_type='recharge_order' AND business_object_id <> '' AND status <> 'cleaned';
	`)
	return err
}

func (p *StoragePlugin) insertResource(ctx context.Context, res storageResource) (storageResource, error) {
	if p.db != nil {
		row := p.db.QueryRowContext(ctx, `
			INSERT INTO storage_resources
				(id, user_id, feature, business_object_type, business_object_id, original_filename, file_ext, mime_type,
				 file_size_bytes, storage_key, public_url, status, uploaded_at, created_at, updated_at)
			VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13, $13)
			RETURNING id::text, COALESCE(user_id::text, ''), feature, business_object_type, business_object_id,
				original_filename, file_ext, mime_type, file_size_bytes, storage_key, public_url, status,
				uploaded_at, deleted_at, cleaned_at, created_at, updated_at`,
			res.ID, res.UserID, res.Feature, res.BusinessObjectType, res.BusinessObjectID, res.OriginalFilename, res.FileExt,
			res.MimeType, res.FileSizeBytes, res.StorageKey, res.PublicURL, res.Status, res.UploadedAt)
		return scanResource(row)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.memory[res.ID] = res
	return res, nil
}

func (p *StoragePlugin) listResources(ctx context.Context, filter resourceListFilter) ([]storageResource, int, error) {
	if p.db != nil {
		where, args := buildResourceWhere(filter)
		var total int
		if err := p.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM storage_resources WHERE "+where, args...).Scan(&total); err != nil {
			return nil, 0, err
		}
		args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
		rows, err := p.db.QueryContext(ctx, `
			SELECT id::text, COALESCE(user_id::text, ''), feature, business_object_type, business_object_id,
				original_filename, file_ext, mime_type, file_size_bytes, storage_key, public_url, status,
				uploaded_at, deleted_at, cleaned_at, created_at, updated_at
			FROM storage_resources WHERE `+where+`
			ORDER BY uploaded_at DESC, id DESC
			LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		resources := []storageResource{}
		for rows.Next() {
			res, err := scanResource(rows)
			if err != nil {
				return nil, 0, err
			}
			resources = append(resources, res)
		}
		return resources, total, rows.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	all := make([]storageResource, 0, len(p.memory))
	for _, res := range p.memory {
		if resourceMatches(res, filter) {
			all = append(all, res)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].UploadedAt.Equal(all[j].UploadedAt) {
			return all[i].ID > all[j].ID
		}
		return all[i].UploadedAt.After(all[j].UploadedAt)
	})
	total := len(all)
	start := (filter.Page - 1) * filter.PageSize
	if start >= total {
		return []storageResource{}, total, nil
	}
	end := min(total, start+filter.PageSize)
	return all[start:end], total, nil
}

func (p *StoragePlugin) getResource(ctx context.Context, id string) (storageResource, error) {
	if p.db != nil {
		row := p.db.QueryRowContext(ctx, `
			SELECT id::text, COALESCE(user_id::text, ''), feature, business_object_type, business_object_id,
				original_filename, file_ext, mime_type, file_size_bytes, storage_key, public_url, status,
				uploaded_at, deleted_at, cleaned_at, created_at, updated_at
			FROM storage_resources WHERE id=$1`, id)
		res, err := scanResource(row)
		if errors.Is(err, sql.ErrNoRows) {
			return storageResource{}, errResourceNotFound
		}
		return res, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	res, ok := p.memory[id]
	if !ok {
		return storageResource{}, errResourceNotFound
	}
	return res, nil
}

func (p *StoragePlugin) bindResource(ctx context.Context, resourceID, userID, objectType, objectID string) (storageResource, error) {
	if !validateBusinessObject(objectType, objectID) {
		return storageResource{}, fmt.Errorf("invalid business object")
	}
	if p.db != nil {
		if objectType == objectRechargeOrder && objectID != "" {
			ok, conflict, err := p.checkRechargeVoucherBinding(ctx, resourceID, userID, objectID)
			if err != nil {
				return storageResource{}, err
			}
			if !ok {
				return storageResource{}, errResourceNotFound
			}
			if conflict {
				return storageResource{}, errResourceConflict
			}
		}
		row := p.db.QueryRowContext(ctx, `
			UPDATE storage_resources SET business_object_type=$2, business_object_id=$3, updated_at=now()
			WHERE id=$1 AND status='normal' AND (user_id IS NULL OR user_id::text=$4)
			RETURNING id::text, COALESCE(user_id::text, ''), feature, business_object_type, business_object_id,
				original_filename, file_ext, mime_type, file_size_bytes, storage_key, public_url, status,
				uploaded_at, deleted_at, cleaned_at, created_at, updated_at`,
			resourceID, objectType, objectID, userID)
		res, err := scanResource(row)
		if errors.Is(err, sql.ErrNoRows) {
			return storageResource{}, errResourceNotFound
		}
		return res, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	res, ok := p.memory[resourceID]
	if !ok || res.Status != statusNormal || (res.UserID != "" && res.UserID != userID) {
		return storageResource{}, errResourceNotFound
	}
	if objectType == objectRechargeOrder && objectID != "" {
		if res.Feature != featureRechargeVoucher {
			return storageResource{}, errResourceNotFound
		}
		for _, existing := range p.memory {
			if existing.ID != resourceID && existing.Feature == featureRechargeVoucher && existing.BusinessObjectType == objectRechargeOrder && existing.BusinessObjectID == objectID && existing.Status != statusCleaned {
				return storageResource{}, errResourceConflict
			}
		}
	}
	now := time.Now().UTC()
	res.BusinessObjectType = objectType
	res.BusinessObjectID = objectID
	res.UpdatedAt = now
	p.memory[resourceID] = res
	return res, nil
}

func (p *StoragePlugin) checkRechargeVoucherBinding(ctx context.Context, resourceID, userID, objectID string) (bool, bool, error) {
	var ok, conflict bool
	err := p.db.QueryRowContext(ctx, `
		SELECT EXISTS (
				SELECT 1 FROM storage_resources target
				WHERE target.id=$1 AND target.status=$2 AND target.feature=$3 AND (target.user_id IS NULL OR target.user_id::text=$4)
			),
			EXISTS (
				SELECT 1 FROM storage_resources target
				WHERE target.id=$1 AND target.status=$2 AND target.feature=$3 AND (target.user_id IS NULL OR target.user_id::text=$4)
					AND EXISTS (
						SELECT 1 FROM storage_resources existing
						WHERE existing.id<>target.id AND existing.feature=$3 AND existing.business_object_type=$5
							AND existing.business_object_id=$6 AND existing.status<>$7
					)
			)`,
		resourceID, statusNormal, featureRechargeVoucher, userID, objectRechargeOrder, objectID, statusCleaned).Scan(&ok, &conflict)
	return ok, conflict, err
}

func (p *StoragePlugin) softDeleteResource(ctx context.Context, resourceID string) (storageResource, error) {
	now := time.Now().UTC()
	if p.db != nil {
		row := p.db.QueryRowContext(ctx, `
			UPDATE storage_resources SET status='deleted', deleted_at=$2, updated_at=$2
			WHERE id=$1 AND status='normal'
			RETURNING id::text, COALESCE(user_id::text, ''), feature, business_object_type, business_object_id,
				original_filename, file_ext, mime_type, file_size_bytes, storage_key, public_url, status,
				uploaded_at, deleted_at, cleaned_at, created_at, updated_at`, resourceID, now)
		res, err := scanResource(row)
		if errors.Is(err, sql.ErrNoRows) {
			return storageResource{}, errResourceNotFound
		}
		return res, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	res, ok := p.memory[resourceID]
	if !ok || res.Status != statusNormal {
		return storageResource{}, errResourceNotFound
	}
	res.Status = statusDeleted
	res.DeletedAt = &now
	res.UpdatedAt = now
	p.memory[resourceID] = res
	return res, nil
}

func scanResource(scanner interface{ Scan(dest ...any) error }) (storageResource, error) {
	var res storageResource
	var deletedAt, cleanedAt sql.NullTime
	if err := scanner.Scan(&res.ID, &res.UserID, &res.Feature, &res.BusinessObjectType, &res.BusinessObjectID,
		&res.OriginalFilename, &res.FileExt, &res.MimeType, &res.FileSizeBytes, &res.StorageKey, &res.PublicURL,
		&res.Status, &res.UploadedAt, &deletedAt, &cleanedAt, &res.CreatedAt, &res.UpdatedAt); err != nil {
		return storageResource{}, err
	}
	if deletedAt.Valid {
		res.DeletedAt = &deletedAt.Time
	}
	if cleanedAt.Valid {
		res.CleanedAt = &cleanedAt.Time
	}
	return res, nil
}

func buildResourceWhere(filter resourceListFilter) (string, []any) {
	where := []string{"TRUE"}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, strings.ReplaceAll(clause, "?", "$"+strconv.Itoa(len(args))))
	}
	if filter.UserID != "" {
		add("user_id::text=?", filter.UserID)
	}
	if filter.Feature != "" {
		add("feature=?", filter.Feature)
	}
	if filter.BusinessObjectType != "" {
		add("business_object_type=?", filter.BusinessObjectType)
	}
	if filter.BusinessObjectID != "" {
		add("business_object_id=?", filter.BusinessObjectID)
	}
	if filter.Status != "" {
		add("status=?", filter.Status)
	}
	if filter.UploadedFrom != nil {
		add("uploaded_at>=?", *filter.UploadedFrom)
	}
	if filter.UploadedTo != nil {
		add("uploaded_at<=?", *filter.UploadedTo)
	}
	return strings.Join(where, " AND "), args
}

func resourceMatches(res storageResource, filter resourceListFilter) bool {
	if filter.UploadedFrom != nil && res.UploadedAt.Before(*filter.UploadedFrom) {
		return false
	}
	if filter.UploadedTo != nil && res.UploadedAt.After(*filter.UploadedTo) {
		return false
	}
	return (filter.UserID == "" || res.UserID == filter.UserID) &&
		(filter.Feature == "" || res.Feature == filter.Feature) &&
		(filter.BusinessObjectType == "" || res.BusinessObjectType == filter.BusinessObjectType) &&
		(filter.BusinessObjectID == "" || res.BusinessObjectID == filter.BusinessObjectID) &&
		(filter.Status == "" || res.Status == filter.Status)
}
