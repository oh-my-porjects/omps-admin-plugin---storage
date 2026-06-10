package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSoftDeleteAndBind(t *testing.T) {
	plugin := testPlugin()
	now := time.Now().UTC()
	res := storageResource{
		ID:               "Res000000201",
		UploadBatchID:    "Bat000000201",
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
		ID:               "Res000000301",
		UploadBatchID:    "Bat000000301",
		IsCurrent:        true,
		UserID:           "User00000001",
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

func TestMethodBindBusinessObjectAcceptsResourceIDs(t *testing.T) {
	plugin := testPlugin()
	now := time.Now().UTC()
	for i, id := range []string{"Res000000401", "Res000000402"} {
		res := storageResource{
			ID:               id,
			UploadBatchID:    "Bat000000401",
			IsCurrent:        true,
			UserID:           "User00000001",
			Feature:          "recharge_voucher",
			OriginalFilename: "voucher.jpg",
			FileExt:          "jpg",
			MimeType:         "image/jpeg",
			FileSizeBytes:    int64(11 + i),
			StorageKey:       "dev/recharge_voucher/u/2026/05/11/" + id + ".jpg",
			PublicURL:        "https://cdn.example.com/dev/recharge_voucher/u/2026/05/11/" + id + ".jpg",
			Status:           statusNormal,
			UploadedAt:       now,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		_, _ = plugin.insertResource(context.Background(), res)
	}
	result, err := plugin.methodBindBusinessObject(map[string]any{
		"resource_ids":         []any{"Res000000401", "Res000000402"},
		"user_id":              "User00000001",
		"business_object_type": "recharge_order",
		"business_object_id":   "RC202606100002",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result=%T, want map", result)
	}
	items, ok := resp["resources"].([]map[string]any)
	if !ok || len(items) != 2 {
		t.Fatalf("resources=%T len, want 2", resp["resources"])
	}
	current := 0
	for _, res := range plugin.memory {
		if res.BusinessObjectID == "RC202606100002" && res.IsCurrent {
			current++
		}
	}
	if current != 2 {
		t.Fatalf("current=%d, want 2", current)
	}
}
