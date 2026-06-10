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

var errResourceNotFound = errors.New("resource not found")

type storageResource struct {
	ID                 string
	UploadBatchID      string
	IsCurrent          bool
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
	IncludeHistory     bool
	Page               int
	PageSize           int
}

func (p *StoragePlugin) ensureSchema(ctx context.Context) error {
	if p.db == nil {
		return nil
	}
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS storage_resources (
			id TEXT PRIMARY KEY DEFAULT generate_short_id(),
			upload_batch_id TEXT NOT NULL DEFAULT generate_short_id(),
			is_current BOOLEAN NOT NULL DEFAULT TRUE,
			user_id TEXT,
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
		ALTER TABLE storage_resources ADD COLUMN IF NOT EXISTS upload_batch_id TEXT NOT NULL DEFAULT generate_short_id();
		ALTER TABLE storage_resources ADD COLUMN IF NOT EXISTS is_current BOOLEAN NOT NULL DEFAULT TRUE;
		DROP INDEX IF EXISTS uniq_storage_recharge_order_voucher;
		CREATE INDEX IF NOT EXISTS idx_storage_resources_user ON storage_resources(user_id);
		CREATE INDEX IF NOT EXISTS idx_storage_resources_feature ON storage_resources(feature);
		CREATE INDEX IF NOT EXISTS idx_storage_resources_object ON storage_resources(business_object_type, business_object_id);
		CREATE INDEX IF NOT EXISTS idx_storage_resources_batch ON storage_resources(upload_batch_id);
		CREATE INDEX IF NOT EXISTS idx_storage_resources_current_object ON storage_resources(feature, business_object_type, business_object_id, is_current);
		CREATE INDEX IF NOT EXISTS idx_storage_resources_status_deleted ON storage_resources(status, deleted_at);
	`)
	return err
}

func (p *StoragePlugin) insertResource(ctx context.Context, res storageResource) (storageResource, error) {
	if res.UploadBatchID == "" {
		res.UploadBatchID = newShortID()
	}
	res.IsCurrent = true
	saved, err := p.insertResourceBatch(ctx, res.UploadBatchID, []storageResource{res})
	if err != nil {
		return storageResource{}, err
	}
	if len(saved) == 0 {
		return storageResource{}, errResourceNotFound
	}
	return saved[0], nil
}

func (p *StoragePlugin) insertResourceBatch(ctx context.Context, batchID string, resources []storageResource) ([]storageResource, error) {
	if len(resources) == 0 {
		return nil, errors.New("empty resource batch")
	}
	if p.db != nil {
		tx, err := p.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		first := resources[0]
		if first.BusinessObjectType != "" && first.BusinessObjectID != "" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE storage_resources
				SET is_current=FALSE, updated_at=$4
				WHERE feature=$1 AND business_object_type=$2 AND business_object_id=$3 AND is_current=TRUE`,
				first.Feature, first.BusinessObjectType, first.BusinessObjectID, first.UpdatedAt); err != nil {
				return nil, err
			}
		}
		saved := make([]storageResource, 0, len(resources))
		for _, res := range resources {
			res.UploadBatchID = batchID
			res.IsCurrent = true
			row := tx.QueryRowContext(ctx, `
			INSERT INTO storage_resources
				(id, upload_batch_id, is_current, user_id, feature, business_object_type, business_object_id, original_filename, file_ext, mime_type,
				 file_size_bytes, storage_key, public_url, status, uploaded_at, created_at, updated_at)
			VALUES ($1, $2, TRUE, NULLIF($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14, $14)
			RETURNING id, upload_batch_id, is_current, COALESCE(user_id, ''), feature, business_object_type, business_object_id,
				original_filename, file_ext, mime_type, file_size_bytes, storage_key, public_url, status,
				uploaded_at, deleted_at, cleaned_at, created_at, updated_at`,
				res.ID, res.UploadBatchID, res.UserID, res.Feature, res.BusinessObjectType, res.BusinessObjectID, res.OriginalFilename, res.FileExt,
				res.MimeType, res.FileSizeBytes, res.StorageKey, res.PublicURL, res.Status, res.UploadedAt)
			stored, err := scanResource(row)
			if err != nil {
				return nil, err
			}
			saved = append(saved, stored)
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return saved, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	first := resources[0]
	if first.BusinessObjectType != "" && first.BusinessObjectID != "" {
		for id, existing := range p.memory {
			if existing.Feature == first.Feature && existing.BusinessObjectType == first.BusinessObjectType && existing.BusinessObjectID == first.BusinessObjectID && existing.IsCurrent {
				existing.IsCurrent = false
				existing.UpdatedAt = first.UpdatedAt
				p.memory[id] = existing
			}
		}
	}
	saved := make([]storageResource, 0, len(resources))
	for _, res := range resources {
		res.UploadBatchID = batchID
		res.IsCurrent = true
		p.memory[res.ID] = res
		saved = append(saved, res)
	}
	return saved, nil
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
			SELECT id, upload_batch_id, is_current, COALESCE(user_id, ''), feature, business_object_type, business_object_id,
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
			SELECT id, upload_batch_id, is_current, COALESCE(user_id, ''), feature, business_object_type, business_object_id,
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
	resources, err := p.bindResourceBatch(ctx, []string{resourceID}, userID, objectType, objectID)
	if err != nil {
		return storageResource{}, err
	}
	if len(resources) == 0 {
		return storageResource{}, errResourceNotFound
	}
	return resources[0], nil
}

func (p *StoragePlugin) bindResourceBatch(ctx context.Context, resourceIDs []string, userID, objectType, objectID string) ([]storageResource, error) {
	if !validateBusinessObject(objectType, objectID) {
		return nil, fmt.Errorf("invalid business object")
	}
	if len(resourceIDs) == 0 {
		return nil, errResourceNotFound
	}
	batchID := newShortID()
	if p.db != nil {
		tx, err := p.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		targets := make([]storageResource, 0, len(resourceIDs))
		for _, resourceID := range resourceIDs {
			row := tx.QueryRowContext(ctx, `
			SELECT id, upload_batch_id, is_current, COALESCE(user_id, ''), feature, business_object_type, business_object_id,
				original_filename, file_ext, mime_type, file_size_bytes, storage_key, public_url, status,
				uploaded_at, deleted_at, cleaned_at, created_at, updated_at
			FROM storage_resources
			WHERE id=$1 AND status='normal' AND (user_id IS NULL OR user_id=$2)`,
				resourceID, userID)
			target, err := scanResource(row)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errResourceNotFound
			}
			if err != nil {
				return nil, err
			}
			targets = append(targets, target)
		}
		feature := targets[0].Feature
		for _, target := range targets {
			if target.Feature != feature {
				return nil, fmt.Errorf("resource feature mismatch")
			}
			if objectType == objectRechargeOrder && objectID != "" && target.Feature != featureRechargeVoucher {
				return nil, errResourceNotFound
			}
		}
		now := time.Now().UTC()
		if objectType != "" && objectID != "" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE storage_resources SET is_current=FALSE, updated_at=$4
				WHERE feature=$1 AND business_object_type=$2 AND business_object_id=$3 AND is_current=TRUE`,
				feature, objectType, objectID, now); err != nil {
				return nil, err
			}
		}
		saved := make([]storageResource, 0, len(targets))
		for _, target := range targets {
			row := tx.QueryRowContext(ctx, `
			UPDATE storage_resources
			SET business_object_type=$2, business_object_id=$3, upload_batch_id=$5, is_current=TRUE, updated_at=$6
			WHERE id=$1 AND status='normal' AND (user_id IS NULL OR user_id=$4)
			RETURNING id, upload_batch_id, is_current, COALESCE(user_id, ''), feature, business_object_type, business_object_id,
				original_filename, file_ext, mime_type, file_size_bytes, storage_key, public_url, status,
				uploaded_at, deleted_at, cleaned_at, created_at, updated_at`,
				target.ID, objectType, objectID, userID, batchID, now)
			res, err := scanResource(row)
			if err != nil {
				return nil, err
			}
			saved = append(saved, res)
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return saved, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	targets := make([]storageResource, 0, len(resourceIDs))
	for _, resourceID := range resourceIDs {
		res, ok := p.memory[resourceID]
		if !ok || res.Status != statusNormal || (res.UserID != "" && res.UserID != userID) {
			return nil, errResourceNotFound
		}
		targets = append(targets, res)
	}
	feature := targets[0].Feature
	for _, res := range targets {
		if res.Feature != feature {
			return nil, fmt.Errorf("resource feature mismatch")
		}
		if objectType == objectRechargeOrder && objectID != "" && res.Feature != featureRechargeVoucher {
			return nil, errResourceNotFound
		}
	}
	now := time.Now().UTC()
	if objectType != "" && objectID != "" {
		for id, existing := range p.memory {
			if existing.Feature == feature && existing.BusinessObjectType == objectType && existing.BusinessObjectID == objectID && existing.IsCurrent {
				existing.IsCurrent = false
				existing.UpdatedAt = now
				p.memory[id] = existing
			}
		}
	}
	saved := make([]storageResource, 0, len(targets))
	for _, res := range targets {
		res.BusinessObjectType = objectType
		res.BusinessObjectID = objectID
		res.UploadBatchID = batchID
		res.IsCurrent = true
		res.UpdatedAt = now
		p.memory[res.ID] = res
		saved = append(saved, res)
	}
	return saved, nil
}

func (p *StoragePlugin) softDeleteResource(ctx context.Context, resourceID string) (storageResource, error) {
	now := time.Now().UTC()
	if p.db != nil {
		row := p.db.QueryRowContext(ctx, `
			UPDATE storage_resources SET status='deleted', is_current=FALSE, deleted_at=$2, updated_at=$2
			WHERE id=$1 AND status='normal'
			RETURNING id, upload_batch_id, is_current, COALESCE(user_id, ''), feature, business_object_type, business_object_id,
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
	res.IsCurrent = false
	res.DeletedAt = &now
	res.UpdatedAt = now
	p.memory[resourceID] = res
	return res, nil
}

func scanResource(scanner interface{ Scan(dest ...any) error }) (storageResource, error) {
	var res storageResource
	var deletedAt, cleanedAt sql.NullTime
	if err := scanner.Scan(&res.ID, &res.UploadBatchID, &res.IsCurrent, &res.UserID, &res.Feature, &res.BusinessObjectType, &res.BusinessObjectID,
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
		add("user_id=?", filter.UserID)
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
	if !filter.IncludeHistory {
		where = append(where, "is_current=TRUE")
		if filter.Status == "" {
			where = append(where, "status='normal'")
		}
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
		(filter.Status == "" || res.Status == filter.Status) &&
		(filter.IncludeHistory || res.IsCurrent)
}
