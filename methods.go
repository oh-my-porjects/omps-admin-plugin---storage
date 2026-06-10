package main

import (
	"context"
	"fmt"
	"strings"
)

func registerStorageMethods() {
	Methods["BindBusinessObject"] = func(args map[string]any) (any, error) {
		return Plugin.methodBindBusinessObject(args)
	}
	Methods["SoftDeleteResource"] = func(args map[string]any) (any, error) {
		return Plugin.methodSoftDeleteResource(args)
	}
}

func (p *StoragePlugin) methodBindBusinessObject(args map[string]any) (any, error) {
	resourceID := stringArg(args, "resource_id")
	resourceIDs := stringSliceArg(args, "resource_ids")
	if resourceID != "" {
		resourceIDs = append([]string{resourceID}, resourceIDs...)
	}
	userID := stringArg(args, "user_id")
	objectType := stringArg(args, "business_object_type")
	objectID := stringArg(args, "business_object_id")
	if len(resourceIDs) == 0 {
		return nil, fmt.Errorf("resource_id invalid")
	}
	deduped := make([]string, 0, len(resourceIDs))
	seen := map[string]struct{}{}
	for _, id := range resourceIDs {
		id = strings.TrimSpace(id)
		if !validateUUID(id) {
			return nil, fmt.Errorf("resource_id invalid")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		deduped = append(deduped, id)
	}
	if userID != "" && !validateUUID(userID) {
		return nil, fmt.Errorf("user_id invalid")
	}
	resources, err := p.bindResourceBatch(context.Background(), deduped, userID, objectType, objectID)
	if err != nil {
		return nil, err
	}
	if len(resources) == 1 && len(resourceIDs) == 1 && resourceID != "" {
		return resourceDetail(resources[0]), nil
	}
	items := make([]map[string]any, 0, len(resources))
	for _, res := range resources {
		items = append(items, resourceDetail(res))
	}
	return map[string]any{"resources": items}, nil
}

func (p *StoragePlugin) methodSoftDeleteResource(args map[string]any) (any, error) {
	resourceID := stringArg(args, "resource_id")
	if !validateUUID(resourceID) {
		return nil, fmt.Errorf("resource_id invalid")
	}
	res, err := p.softDeleteResource(context.Background(), resourceID)
	if err != nil {
		return nil, err
	}
	return resourceDetail(res), nil
}

func stringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func stringSliceArg(args map[string]any, key string) []string {
	if args == nil {
		return nil
	}
	switch v := args[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}
