package main

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strconv"
	"time"
)

type selftestTemplateVars struct {
	ts    string
	tsMs  string
	uuid  string
	cache map[string]string
}

func buildSelftestTemplateVars() *selftestTemplateVars {
	now := time.Now()
	return &selftestTemplateVars{
		ts:    strconv.FormatInt(now.Unix(), 10),
		tsMs:  strconv.FormatInt(now.UnixMilli(), 10),
		uuid:  randSelftestHex(32),
		cache: map[string]string{},
	}
}

var selftestTemplateVarRE = regexp.MustCompile(`\$\{([a-zA-Z_]+)(?::([a-zA-Z0-9_-]+))?\}`)

func applySelftestTemplateVars(s string, v *selftestTemplateVars) string {
	if s == "" || v == nil {
		return s
	}
	return selftestTemplateVarRE.ReplaceAllStringFunc(s, func(match string) string {
		groups := selftestTemplateVarRE.FindStringSubmatch(match)
		name, arg := groups[1], groups[2]
		key := name
		if arg != "" {
			key += ":" + arg
		}
		if cached, ok := v.cache[key]; ok {
			return cached
		}
		val := resolveSelftestTemplateName(name, arg, v)
		v.cache[key] = val
		return val
	})
}

func resolveSelftestTemplateName(name, arg string, v *selftestTemplateVars) string {
	switch name {
	case "range_tag":
		return "test_req_selftest_"
	case "ts":
		return v.ts
	case "ts_ms":
		return v.tsMs
	case "uuid":
		return v.uuid
	case "rand":
		n := 6
		if arg != "" {
			if parsed, err := strconv.Atoi(arg); err == nil && parsed >= 2 && parsed <= 32 {
				n = parsed
			}
		}
		return randSelftestHex(n)
	}
	if arg == "" {
		return "${" + name + "}"
	}
	return "${" + name + ":" + arg + "}"
}

func randSelftestHex(n int) string {
	bytesNeeded := (n + 1) / 2
	buf := make([]byte, bytesNeeded)
	if _, err := rand.Read(buf); err != nil {
		fallback := strconv.FormatInt(time.Now().UnixNano(), 16)
		if len(fallback) >= n {
			return fallback[:n]
		}
		return fallback
	}
	return hex.EncodeToString(buf)[:n]
}

func resolveSelftestTemplateAny(v any, vars *selftestTemplateVars) any {
	if s, ok := v.(string); ok {
		return applySelftestTemplateVars(s, vars)
	}
	return v
}
