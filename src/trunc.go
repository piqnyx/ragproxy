package main

import (
	"encoding/json"
	"log"
)

const maxStrLen = 32

func truncateJSONStrings(data string) string {
	var obj any
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		log.Printf("Error unmarshaling JSON for truncation: %v", err)
		return data
	}

	obj = truncateStrings(obj)

	truncatedData, err := json.Marshal(obj)
	if err != nil {
		log.Printf("Error marshaling truncated JSON: %v", err)
		return data
	}

	return string(truncatedData)
}

func truncateStrings(v any) any {
	switch val := v.(type) {
	case string:
		if len(val) > maxStrLen {
			return val[:maxStrLen] + "..."
		}
		return val
	case map[string]any:
		for k, vv := range val {
			val[k] = truncateStrings(vv)
		}
		return val
	case []any:
		for i, item := range val {
			val[i] = truncateStrings(item)
		}
		return val
	default:
		return v
	}
}
