package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

func decodeBencode(bencodedString string) (interface{}, int, error) {
	if unicode.IsDigit(rune(bencodedString[0])) {
		var firstColonIndex int
		for i := 0; i < len(bencodedString); i++ {
			if bencodedString[i] == ':' {
				firstColonIndex = i
				break
			}
		}

		lengthStr := bencodedString[:firstColonIndex]

		length, err := strconv.Atoi(lengthStr)
		if err != nil {
			return "", 0, err
		}
		return bencodedString[firstColonIndex+1 : firstColonIndex+1+length], firstColonIndex + 1 + length, nil
	} else if bencodedString[0] == 'i' {
		index := strings.Index(bencodedString[1:], "e")
		if index == -1 {
			return "", 0, errors.New("invalid bencoded string: " + bencodedString)
		}
		valueStr := bencodedString[1 : index+1]
		value, err := strconv.Atoi(valueStr)
		if err != nil {
			return "", 0, err
		}
		return value, index + 2, nil // +1 for 'i', +1 for 'e'
	} else if bencodedString[0] == 'l' {
		result := make([]interface{}, 0)
		index := 1 // 跳过 'l'
		for index < len(bencodedString) && bencodedString[index] != 'e' {
			value, consumed, err := decodeBencode(bencodedString[index:])
			if err != nil {
				return "", 0, err
			}
			result = append(result, value)
			index += consumed
		}
		if index >= len(bencodedString) || bencodedString[index] != 'e' {
			return "", 0, errors.New("invalid bencoded string: missing 'e' for list")
		}
		return result, index + 1, nil // +1 包括 'e'
	} else if bencodedString[0] == 'd' {
		// 解码字典
		result := make(map[string]interface{})
		index := 1 // 跳过 'd'
		for index < len(bencodedString) && bencodedString[index] != 'e' {
			key, consumed, err := decodeBencode(bencodedString[index:])
			if err != nil {
				return "", 0, err
			}
			index += consumed
			value, consumed, err := decodeBencode(bencodedString[index:])
			if err != nil {
				return "", 0, err
			}
			keyStr, ok := key.(string)
			if !ok {
				return "", 0, errors.New("invalid bencoded string: dictionary key must be a string")
			}
			result[keyStr] = value
			index += consumed
		}
		if index >= len(bencodedString) || bencodedString[index] != 'e' {
			return "", 0, errors.New("invalid bencoded string: missing 'e' for dictionary")
		}
		return result, index + 1, nil // +1 包括 'e'
	} else {
		return "", 0, errors.New("invalid bencoded string: " + bencodedString)
	}
}

// magnet:?xt=urn:btih:ad42ce8109f54c99613ce38f9b4d87e70f24a165&dn=magnet1.gif&tr=http%3A%2F%2Fbittorrent-test-tracker.codecrafters.io%2Fannounce
func decodeMagnetLink(link string) (map[string]string, error) {
	result := make(map[string]string)
	if link[:6] != "magnet" {
		return nil, errors.New("invalid magnet link: " + link)
	}
	query := link[8:]
	queryParts := strings.Split(query, "&")
	for _, part := range queryParts {
		if strings.HasPrefix(part, "xt=") {
			// 提取 info hash: xt=urn:btih:<hash>
			lastIndex := strings.LastIndex(part, ":")
			if lastIndex == -1 {
				return nil, errors.New("invalid magnet link: " + link)
			}
			value := part[lastIndex+1:]
			result["Info Hash"] = value
		} else if strings.HasPrefix(part, "tr=") {
			// 提取 tracker URL: tr=<url>
			value := part[3:] // 跳过 "tr="
			// URL 解码 tracker URL
			//
			// 为什么需要解码？
			// 在磁力链接中，URL 参数值通常是 URL 编码的（percent-encoded），因为：
			// 1. 磁力链接本身是一个 URL，其查询参数中的值如果包含特殊字符（如 :、/、?、& 等），必须进行编码
			// 2. 例如：tr=http%3A%2F%2Ftracker.example.com%2Fannounce
			//    - %3A 是 ':' 的编码
			//    - %2F 是 '/' 的编码
			//    - 解码后得到：http://tracker.example.com/announce
			// 3. 我们需要解码它，才能得到正确的 URL 用于后续的 HTTP 请求（url.Parse 和 http.Get）
			//
			// 关于编码（在其他地方使用 url.QueryEscape）：
			// 当我们构建 tracker 请求 URL 时，需要对 info_hash 和 peer_id 进行编码，因为：
			// 1. 它们是二进制数据（20 字节），可能包含特殊字符
			// 2. 作为 URL 查询参数传递时，必须进行 URL 编码
			// 3. 例如：info_hash=%XX%XX...（XX 是十六进制）
			decodedURL, err := url.QueryUnescape(value)
			if err != nil {
				return nil, fmt.Errorf("error decoding tracker URL: %v", err)
			}
			result["Tracker URL"] = decodedURL
		}
		// 忽略其他参数（如 dn= 文件名）
	}
	return result, nil
}
