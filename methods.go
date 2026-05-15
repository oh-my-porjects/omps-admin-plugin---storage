package main

import (
	"context"
	"errors"
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
	userID := stringArg(args, "user_id")
	objectType := stringArg(args, "business_object_type")
	objectID := stringArg(args, "business_object_id")
	if !validateUUID(resourceID) {
		return nil, fmt.Errorf("resource_id invalid")
	}
	if userID != "" && !validateUUID(userID) {
		return nil, fmt.Errorf("user_id invalid")
	}
	res, err := p.bindResource(context.Background(), resourceID, userID, objectType, objectID)
	if err != nil {
		if errors.Is(err, errResourceConflict) {
			return nil, fmt.Errorf("同一充值订单只能绑定 1 张充值凭证图片")
		}
		return nil, err
	}
	return resourceDetail(res), nil
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
