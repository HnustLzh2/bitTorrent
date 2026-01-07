package main

import (
	"errors"
	"fmt"
	"sort"
)

func encodeBencode(value interface{}) (string, error) {
	switch v := value.(type) {
	case int:
		return fmt.Sprintf("i%de", v), nil
	case string:
		return fmt.Sprintf("%d:%s", len(v), v), nil
	case []byte:
		return fmt.Sprintf("%d:", len(v)) + string(v), nil
	case []interface{}:
		var result string = "l"
		for _, item := range v {
			encoded, err := encodeBencode(item)
			if err != nil {
				return "", err
			}
			result += encoded
		}
		result += "e"
		return result, nil
	case map[string]interface{}:
		var result string = "d"

		// 获取所有键并排序（字典序）
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		// 按排序后的键顺序编码
		for _, k := range keys {
			// 编码键
			encodedKey, err := encodeBencode(k)
			if err != nil {
				return "", err
			}
			result += encodedKey
			// 编码值
			encodedValue, err := encodeBencode(v[k])
			if err != nil {
				return "", err
			}
			result += encodedValue
		}
		result += "e"
		return result, nil
	default:
		return "", errors.New("invalid value type: " + fmt.Sprintf("%T", value))
	}
}
