package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
)

func downloadPieceWithPeer(peer Address, infoDict map[string]interface{}, queue *WorkQueue, buffer *PieceBuffer, infoHashBytes []byte) error {
	// 建立连接并完成握手
	conn, err := performHandshakeWithPeer(peer, infoHashBytes)
	if err != nil {
		return fmt.Errorf("error performing handshake with peer %s:%d: %v", peer.IP, peer.Port, err)
	}
	defer conn.Close() // 确保连接关闭

	// 等待 bitfield 消息
	err = waitForBitfield(conn)
	if err != nil {
		return fmt.Errorf("error waiting for bitfield: %v", err)
	}

	// 发送 interested 消息
	err = sendInterested(conn)
	if err != nil {
		return fmt.Errorf("error sending interested: %v", err)
	}

	// 等待 unchoke 消息
	err = waitForUnchoke(conn)
	if err != nil {
		return fmt.Errorf("error waiting for unchoke: %v", err)
	}
	for {
		// 检查队列是否为空
		if queue.IsEmpty() {
			break
		}
		// 从队列获取 piece index
		pieceIndex, ok := queue.Get()
		if !ok {
			// 队列为空，退出循环
			break
		}

		// 检查这个 piece 是否已经下载
		if _, exists := buffer.Get(pieceIndex); exists {
			continue // 跳过已下载的 piece
		}

		// 使用已建立的连接下载 piece（不保存到文件，只返回数据）
		data, err := downloadPieceReuseConn(conn, infoDict, pieceIndex)
		if err != nil {
			// 下载失败，放回队列重试
			queue.Add(pieceIndex)
			// 继续处理下一个 piece，不返回错误
			continue
		}

		// 成功下载并验证，写入缓冲区
		buffer.Set(pieceIndex, data)
	}

	return nil
}

// performMagnetHandshakeWithPeer 与指定的 peer 执行磁力链接握手（包括扩展握手），返回连接
func performMagnetHandshakeWithPeer(peer Address, infoHashBytes []byte) (net.Conn, error) {
	// 建立 TCP 连接
	peerAddress := net.JoinHostPort(peer.IP, strconv.Itoa(peer.Port))
	conn, err := net.Dial("tcp", peerAddress)
	if err != nil {
		return nil, fmt.Errorf("error connecting to peer: %v", err)
	}

	// 生成随机 peer id（20 字节）
	peerID := make([]byte, 20)
	_, err = rand.Read(peerID)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("error generating peer id: %v", err)
	}

	// 构建握手消息（设置扩展支持位）
	handshakeMsg := make([]byte, 0, 68)
	handshakeMsg = append(handshakeMsg, 19)
	handshakeMsg = append(handshakeMsg, []byte("BitTorrent protocol")...)
	// 设置 reserved[5] 的第4位为1，表示支持扩展
	reserved := make([]byte, 8)
	reserved[5] |= (1 << 4)
	handshakeMsg = append(handshakeMsg, reserved...)
	handshakeMsg = append(handshakeMsg, infoHashBytes...)
	handshakeMsg = append(handshakeMsg, peerID...)

	// 发送握手消息
	_, err = conn.Write(handshakeMsg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("error sending handshake: %v", err)
	}

	// 接收握手响应（68 字节）
	response := make([]byte, 68)
	totalRead := 0
	for totalRead < 68 {
		n, err := conn.Read(response[totalRead:])
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("error receiving handshake: %v", err)
		}
		totalRead += n
	}

	// 验证响应格式
	if totalRead < 68 {
		conn.Close()
		return nil, fmt.Errorf("handshake response too short, got %d bytes", totalRead)
	}

	// 验证协议字符串长度
	if response[0] != 19 {
		conn.Close()
		return nil, fmt.Errorf("invalid protocol string length, got %d", response[0])
	}

	// 验证协议字符串
	protocolStr := string(response[1:20])
	if protocolStr != "BitTorrent protocol" {
		conn.Close()
		return nil, fmt.Errorf("invalid protocol string, got %s", protocolStr)
	}

	// 提取保留字节（索引20-27）
	reservedBytes := response[20:28]

	// 等待并接收 bitfield 消息
	err = waitForBitfield(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("error waiting for bitfield: %v", err)
	}

	// 检查对方是否支持扩展，如果支持则发送扩展握手消息
	ourExtensionID := byte(1)
	if supportsExtensions(reservedBytes) {
		extensionHandshakeMsg, err := buildExtensionHandshakeMessage(ourExtensionID)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("error building extension handshake: %v", err)
		}

		// 发送扩展握手消息
		_, err = conn.Write(extensionHandshakeMsg)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("error sending extension handshake: %v", err)
		}

		// 接收扩展握手响应
		messageID, payload, err := readPeerMessage(conn)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("error reading extension handshake response: %v", err)
		}
		if messageID != 20 {
			conn.Close()
			return nil, fmt.Errorf("invalid extension handshake message ID, got %d", messageID)
		}
		if len(payload) == 0 {
			conn.Close()
			return nil, fmt.Errorf("extension handshake payload is empty")
		}
		extensionID := payload[0]
		if extensionID != 0 {
			conn.Close()
			return nil, fmt.Errorf("invalid extension ID in handshake, expected 0, got %d", extensionID)
		}
		// 解析扩展握手字典（这里我们不需要使用，只需要验证格式正确）
		_, _, err = decodeBencode(string(payload[1:]))
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("error decoding extension handshake dict: %v", err)
		}
	}

	return conn, nil
}

func downloadPieceWithPeerByMagnet(peer Address, metadataMap map[string]interface{}, queue *WorkQueue, buffer *PieceBuffer, infoHashBytes []byte) error {
	// 连接到指定的 peer 并执行握手
	conn, err := performMagnetHandshakeWithPeer(peer, infoHashBytes)
	if err != nil {
		return fmt.Errorf("error performing handshake with peer %s:%d: %v", peer.IP, peer.Port, err)
	}
	defer conn.Close()

	// 发送 interested 消息
	err = sendInterested(conn)
	if err != nil {
		return fmt.Errorf("error sending interested: %v", err)
	}

	// 等待 unchoke 消息
	err = waitForUnchoke(conn)
	if err != nil {
		return fmt.Errorf("error waiting for unchoke: %v", err)
	}

	// 循环从队列获取 piece index 并下载
	for {
		if queue.IsEmpty() {
			break
		}
		pieceIndex, ok := queue.Get()
		if !ok {
			break
		}
		// 检查这个 piece 是否已经下载
		if _, exists := buffer.Get(pieceIndex); exists {
			continue // 跳过已下载的 piece
		}

		// 使用已建立的连接下载 piece（不保存到文件，只返回数据）
		data, err := downloadPieceWithMagnetReuseConn(conn, metadataMap, pieceIndex)
		if err != nil {
			// 下载失败，放回队列重试
			queue.Add(pieceIndex)
			// 继续处理下一个 piece，不返回错误
			continue
		}
		buffer.Set(pieceIndex, data)
	}
	return nil
}

func combinePieces(pieces map[int][]byte, length int) ([]byte, error) {
	result := make([]byte, 0, length)
	// 对piecesMap进行排序
	type pieceEntry struct {
		index int
		data  []byte
	}
	entries := make([]pieceEntry, 0, len(pieces))
	for k, v := range pieces {
		entries = append(entries, pieceEntry{index: k, data: v})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].index < entries[j].index
	})
	for _, entry := range entries {
		result = append(result, entry.data...)
	}
	if len(result) != length {
		return nil, fmt.Errorf("combined file length mismatch: expected %d, got %d", length, len(result))
	}
	return result, nil
}

// getTotalPiecesFromDict 从已解析的 torrentDict 获取 pieces 数量和文件长度
func getTotalPiecesFromDict(torrentDict map[string]interface{}) (int, int, error) {
	infoDict, ok := torrentDict["info"]
	if !ok {
		return 0, 0, errors.New("'info' key not found")
	}
	infoDictMap, ok := infoDict.(map[string]interface{})
	if !ok {
		return 0, 0, errors.New("'info' value is not a dictionary")
	}
	pieces, ok := infoDictMap["pieces"]
	if !ok {
		return 0, 0, errors.New("'pieces' key not found")
	}
	piecesStr, ok := pieces.(string)
	if !ok {
		return 0, 0, errors.New("'pieces' value is not a string")
	}
	// 提取 length
	length, ok := infoDictMap["length"]
	if !ok {
		return 0, 0, errors.New("'length' key not found")
	}
	lengthInt, ok := length.(int)
	if !ok {
		return 0, 0, errors.New("'length' value is not an integer")
	}
	piecesBytes := []byte(piecesStr)
	piecesLength := len(piecesBytes)
	return piecesLength / 20, lengthInt, nil
}

func verifyPieceHash(piece []byte, expectedHash []byte) bool {
	hash := sha1.Sum(piece)
	return bytes.Equal(hash[:], expectedHash)
}

func savePieceToFile(piece []byte, piecePath string) error {
	err := os.WriteFile(piecePath, piece, 0644)
	if err != nil {
		return fmt.Errorf("error saving piece to file: %v", err)
	}
	return nil
}

func sendRequest(conn net.Conn, blockInfo BlockInfo) error {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload, uint32(blockInfo.Index))
	binary.BigEndian.PutUint32(payload[4:8], uint32(blockInfo.Begin))
	binary.BigEndian.PutUint32(payload[8:12], uint32(blockInfo.Length))
	requestMsg := buildPeerMessage(6, payload)
	_, err := conn.Write(requestMsg)
	if err != nil {
		return fmt.Errorf("error sending request message: %v", err)
	}
	return nil
}

func receivePiece(conn net.Conn) (int, int, []byte, error) {
	messageID, payload, err := readPeerMessage(conn)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("error reading message: %v", err)
	}
	if messageID != 7 {
		return 0, 0, nil, fmt.Errorf("expected message ID 7, got %d", messageID)
	}
	if len(payload) < 8 {
		return 0, 0, nil, fmt.Errorf("payload too short, expected at least 8 bytes, got %d bytes", len(payload))
	}
	index := int(binary.BigEndian.Uint32(payload[:4]))
	begin := int(binary.BigEndian.Uint32(payload[4:8]))
	block := payload[8:] // block 数据从第 8 字节开始
	return index, begin, block, nil
}

func combineBlocks(blocks map[int][]byte, pieceLength int) ([]byte, error) {
	if len(blocks) == 0 {
		return nil, fmt.Errorf("no blocks to combine")
	}

	// 将 map 转换为可以排序的切片
	type blockEntry struct {
		begin int
		data  []byte
	}

	entries := make([]blockEntry, 0, len(blocks))
	for begin, data := range blocks {
		entries = append(entries, blockEntry{begin: begin, data: data})
	}

	// 按 begin 偏移排序
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].begin < entries[j].begin
	})

	// 按顺序组合所有 blocks
	result := make([]byte, 0, pieceLength)
	totalLength := 0

	for _, entry := range entries {
		// 验证 begin 偏移是否连续
		if totalLength != entry.begin {
			return nil, fmt.Errorf("blocks are not contiguous: expected begin at %d, got %d", totalLength, entry.begin)
		}

		result = append(result, entry.data...)
		totalLength += len(entry.data)
	}

	// 验证总长度是否等于 piece length
	if totalLength != pieceLength {
		return nil, fmt.Errorf("total length mismatch: expected %d, got %d", pieceLength, totalLength)
	}

	return result, nil
}

// waitForBitfield 等待并验证 bitfield 消息（消息ID=5）
func waitForBitfield(conn net.Conn) error {
	for {
		messageID, payload, err := readPeerMessage(conn)
		if err != nil {
			return fmt.Errorf("error reading message: %v", err)
		}

		// 如果是 keep-alive 消息，继续读取
		if messageID == 0 && payload == nil {
			continue
		}

		// 验证消息ID是否为5（bitfield）
		if messageID == 5 {
			// 忽略 payload，tracker 确保所有 peers 都有所有 pieces
			return nil
		}

		// 如果收到其他消息，继续等待 bitfield
		// 注意：某些客户端可能先发送其他消息，我们需要继续等待
	}
}

// sendInterested 发送 interested 消息（消息ID=2，无payload）
func sendInterested(conn net.Conn) error {
	interestedMsg := buildPeerMessage(2, nil)
	_, err := conn.Write(interestedMsg)
	if err != nil {
		return fmt.Errorf("error sending interested message: %v", err)
	}
	return nil
}

// waitForUnchoke 等待并验证 unchoke 消息（消息ID=1，无payload）
func waitForUnchoke(conn net.Conn) error {
	for {
		messageID, payload, err := readPeerMessage(conn)
		if err != nil {
			return fmt.Errorf("error reading message: %v", err)
		}

		// 如果是 keep-alive 消息，继续读取
		if messageID == 0 && payload == nil {
			continue
		}

		// 验证消息ID是否为1（unchoke）
		if messageID == 1 {
			// 验证 payload 为空
			if len(payload) != 0 {
				return fmt.Errorf("unchoke message should have empty payload, got %d bytes", len(payload))
			}
			return nil
		}
	}
}

// performHandshakeWithPeer 与单个 peer 执行握手，返回连接对象
func performHandshakeWithPeer(address Address, infoHashBytes []byte) (net.Conn, error) {
	// 建立 TCP 连接
	conn, err := net.Dial("tcp", address.IP+":"+strconv.Itoa(address.Port))
	if err != nil {
		return nil, fmt.Errorf("error connecting to peer: %v", err)
	}

	// 生成随机 peer id（20 字节）
	peerID := make([]byte, 20)
	_, err = rand.Read(peerID)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("error generating peer id: %v", err)
	}

	// 构建握手消息
	handshakeMsg := make([]byte, 0, 68)                                   // 1 + 19 + 8 + 20 + 20 = 68 字节
	handshakeMsg = append(handshakeMsg, 19)                               // 协议字符串长度
	handshakeMsg = append(handshakeMsg, []byte("BitTorrent protocol")...) // 协议字符串
	handshakeMsg = append(handshakeMsg, make([]byte, 8)...)               // 8 个保留字节（全为0）
	handshakeMsg = append(handshakeMsg, infoHashBytes...)                 // info hash（20 字节）
	handshakeMsg = append(handshakeMsg, peerID...)                        // peer id（20 字节）

	// 发送握手消息
	_, err = conn.Write(handshakeMsg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("error sending handshake: %v", err)
	}

	// 接收握手响应（68 字节）
	response := make([]byte, 68)
	totalRead := 0
	for totalRead < 68 {
		n, err := conn.Read(response[totalRead:])
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("error receiving handshake: %v", err)
		}
		totalRead += n
	}

	// 验证响应格式
	if totalRead < 68 {
		conn.Close()
		return nil, fmt.Errorf("handshake response too short, got %d bytes", totalRead)
	}

	// 验证协议字符串长度
	if response[0] != 19 {
		conn.Close()
		return nil, fmt.Errorf("invalid protocol string length, got %d", response[0])
	}

	// 验证协议字符串
	protocolStr := string(response[1:20])
	if protocolStr != "BitTorrent protocol" {
		conn.Close()
		return nil, fmt.Errorf("invalid protocol string, got %s", protocolStr)
	}

	return conn, nil
}

func readPeerMessage(conn net.Conn) (messageID byte, payload []byte, err error) {
	// 读取4字节的长度前缀
	messageLenBytes := make([]byte, 4)
	_, err = io.ReadFull(conn, messageLenBytes)
	if err != nil {
		return 0, nil, err
	}
	messageLen := binary.BigEndian.Uint32(messageLenBytes)

	// 如果长度为0，这是keep-alive消息，没有消息ID和payload
	if messageLen == 0 {
		return 0, nil, nil
	}

	// 读取1字节的消息ID
	messageIDBytes := make([]byte, 1)
	_, err = io.ReadFull(conn, messageIDBytes)
	if err != nil {
		return 0, nil, err
	}
	messageID = messageIDBytes[0]

	// 如果长度大于1，读取payload（长度-1是因为已经读取了消息ID）
	if messageLen > 1 {
		payloadLen := int(messageLen) - 1
		payload = make([]byte, payloadLen)
		_, err = io.ReadFull(conn, payload)
		if err != nil {
			return 0, nil, err
		}
	}

	return messageID, payload, nil
}

func buildPeerMessage(messageID byte, payload []byte) []byte {
	// 计算消息总长度：1字节消息ID + payload长度
	messageLen := uint32(1 + len(payload))

	// 构建消息
	result := make([]byte, 0, 4+1+len(payload))

	// 添加4字节的长度前缀（大端序）
	lenBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBytes, messageLen)
	result = append(result, lenBytes...)

	// 添加1字节的消息ID
	result = append(result, messageID)

	// 添加payload（如果有）
	if len(payload) > 0 {
		result = append(result, payload...)
	}

	return result
}

// getPieceInfoFromDict 从已解析的 infoDict 获取指定 piece 的信息
func getPieceInfoFromDict(infoDict map[string]interface{}, pieceIndex int) (int, [20]byte, error) {

	// 获取标准 piece length
	// getPieceInfo 函数返回的是标准的 piece length
	// 但对于最后一个 piece，实际长度可能小于这个值
	pieceLength, ok := infoDict["piece length"]
	if !ok {
		return 0, [20]byte{}, errors.New("'piece length' key not found")
	}
	pieceLengthInt, ok := pieceLength.(int)
	if !ok {
		return 0, [20]byte{}, errors.New("'piece length' value is not an integer")
	}

	// 获取文件总长度
	length, ok := infoDict["length"]
	if !ok {
		return 0, [20]byte{}, errors.New("'length' key not found")
	}
	totalLength, ok := length.(int)
	if !ok {
		return 0, [20]byte{}, errors.New("'length' value is not an integer")
	}

	pieces, ok := infoDict["pieces"]
	if !ok {
		return 0, [20]byte{}, errors.New("'pieces' key not found")
	}
	pieceHashesStr, ok := pieces.(string)
	if !ok {
		return 0, [20]byte{}, errors.New("'pieces' value is not a string")
	}
	pieceHashesBytes := []byte(pieceHashesStr)

	// pieces 是连接在一起的哈希值，每个 piece 的哈希是 20 字节
	// 计算指定 piece 的哈希值位置
	hashStart := pieceIndex * 20
	hashEnd := hashStart + 20

	if hashEnd > len(pieceHashesBytes) {
		return 0, [20]byte{}, fmt.Errorf("piece index %d out of range", pieceIndex)
	}

	pieceHash := [20]byte{}
	copy(pieceHash[:], pieceHashesBytes[hashStart:hashEnd])

	// 计算实际的 piece 长度
	// 最后一个 piece 的长度 = 总长度 - (pieceIndex * pieceLength)
	actualPieceLength := pieceLengthInt
	expectedStart := pieceIndex * pieceLengthInt
	if expectedStart+pieceLengthInt > totalLength {
		// 这是最后一个 piece
		actualPieceLength = totalLength - expectedStart
	}

	return actualPieceLength, pieceHash, nil
}

// supportsExtensions 检查保留字节是否支持扩展（检查第20位）
// reserved 是8字节的保留字节数组
func supportsExtensions(reserved []byte) bool {
	if len(reserved) < 8 {
		return false
	}
	// 检查 reserved[5] 的第4位（从右数，0-based）是否为1
	// 16 = 0x10 = 00010000，第4位是1
	return (reserved[5] & 0x10) != 0
}

// buildExtensionHandshakeMessage 构建扩展握手消息
// extensionID 是 ut_metadata 的扩展ID（1-255之间，不能是0）
func buildExtensionHandshakeMessage(extensionID byte) ([]byte, error) {
	if extensionID == 0 {
		return nil, errors.New("extension ID cannot be 0")
	}

	// 构建 bencoded 字典：{"m": {"ut_metadata": extensionID}}
	extensionsDict := map[string]interface{}{
		"ut_metadata": int(extensionID),
	}
	handshakeDict := map[string]interface{}{
		"m": extensionsDict,
	}

	// 编码字典
	encodedDict, err := encodeBencode(handshakeDict)
	if err != nil {
		return nil, fmt.Errorf("error encoding extension handshake dict: %v", err)
	}

	// 构建扩展消息的 payload：
	// - 扩展消息ID（1字节）= 0（扩展握手）
	// - bencoded 字典
	payload := make([]byte, 0, 1+len(encodedDict))
	payload = append(payload, 0) // 扩展消息ID = 0
	payload = append(payload, []byte(encodedDict)...)

	// 构建完整的扩展消息：
	// - 消息长度前缀（4字节）
	// - 消息ID（1字节）= 20（扩展消息）
	// - payload
	return buildPeerMessage(20, payload), nil
}

func buildMetadataRequestMessage(peerExtenstionID byte) ([]byte, error) {
	requestMap := map[string]interface{}{}
	requestMap["msg_type"] = 0
	requestMap["piece"] = 0
	encodedDict, err := encodeBencode(requestMap)
	if err != nil {
		return nil, fmt.Errorf("error encoding metadata request dict: %v", err)
	}
	payload := make([]byte, 0, 1+len(encodedDict))
	payload = append(payload, peerExtenstionID)       // 扩展ID
	payload = append(payload, []byte(encodedDict)...) // 请求字典
	return buildPeerMessage(20, payload), nil
}

// getMetadataFromMagnet 从magnet link获取元数据字典，返回元数据字典、连接和扩展ID
// 注意：返回的连接不会被关闭，调用者需要负责关闭连接
func getMetadataFromMagnet(decoded map[string]string) (metadataMap map[string]interface{}, conn net.Conn, peerExtenstionId int, ourExtensionID int, err error) {
	// 步骤1: 执行握手和扩展握手，获取连接、对方的扩展ID和我们自己的扩展ID
	conn, _, _, peerExtenstionId, ourExtensionID, err = magnetHandshake(decoded)
	if err != nil {
		return nil, nil, 0, 0, fmt.Errorf("error in magnet handshake: %v", err)
	}
	// 注意：不在这里关闭连接，调用者需要负责关闭

	// 步骤2: 构建并发送元数据请求消息
	metadataRequestMessage, err := buildMetadataRequestMessage(byte(peerExtenstionId))
	if err != nil {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error building metadata request: %v", err)
	}
	_, err = conn.Write(metadataRequestMessage)
	if err != nil {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error sending metadata request: %v", err)
	}

	// 步骤3: 接收元数据响应消息
	messageID, payload, err := readPeerMessage(conn)
	if err != nil {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error reading metadata response: %v", err)
	}

	// 步骤4: 验证消息ID（应该是20，表示扩展消息）
	if messageID != 20 {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error: invalid metadata response message ID, got %d", messageID)
	}
	if len(payload) == 0 {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error: metadata response payload is empty")
	}

	// 步骤5: 验证扩展消息ID
	extensionID := payload[0]
	if extensionID != byte(ourExtensionID) {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error: invalid extension ID, expected %d (our extension ID), got %d", ourExtensionID, extensionID)
	}

	// 步骤6: 解析bencoded字典部分（包含msg_type, piece, total_size）
	// 注意：元数据内容不在字典中，而是在字典之后
	dictStr := string(payload[1:])
	decodedDict, consumed, err := decodeBencode(dictStr)
	if err != nil {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error decoding metadata response dict: %v", err)
	}

	decodedDictMap, ok := decodedDict.(map[string]interface{})
	if !ok {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error: metadata response dict is not a dictionary")
	}

	// 步骤7: 验证并提取字典中的字段
	msgType, ok := decodedDictMap["msg_type"].(int)
	if !ok || msgType != 1 {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error: invalid msg_type, expected 1, got %d", msgType)
	}

	piece, ok := decodedDictMap["piece"].(int)
	if !ok {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error: invalid piece, got %v", decodedDictMap["piece"])
	}
	if piece != 0 {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error: unexpected piece index, expected 0, got %d", piece)
	}

	totalSize, ok := decodedDictMap["total_size"].(int)
	if !ok {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error: invalid total_size, got %v", decodedDictMap["total_size"])
	}

	// 步骤8: 提取元数据内容（在字典之后）
	// consumed是字典消耗的字节数（从payload[1]开始计算），需要加上1（扩展ID）和字典的字节数
	// 字典在payload中的位置：payload[1:1+consumed]
	// 元数据内容在：payload[1+consumed:1+consumed+totalSize]
	metadataStart := 1 + consumed // 跳过扩展ID和字典
	if metadataStart+totalSize > len(payload) {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error: metadata size mismatch, expected %d bytes, got %d bytes available", totalSize, len(payload)-metadataStart)
	}

	// 提取元数据内容（bencoded字典）
	metadataBytes := payload[metadataStart : metadataStart+totalSize]

	// 步骤9: 对元数据内容进行SHA1哈希验证
	infoHashHex := decoded["Info Hash"]
	infoHashBytes, err := hex.DecodeString(infoHashHex)
	if err != nil {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error decoding info hash: %v", err)
	}
	metadataHash := sha1.Sum(metadataBytes)
	if !bytes.Equal(metadataHash[:], infoHashBytes) {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error: metadata hash verification failed")
	}

	// 步骤10: 解析元数据内容（bencoded字典）
	metadataDict, _, err := decodeBencode(string(metadataBytes))
	if err != nil {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error decoding metadata content: %v", err)
	}

	metadataMap, ok = metadataDict.(map[string]interface{})
	if !ok {
		conn.Close()
		return nil, nil, 0, 0, fmt.Errorf("error: metadata content is not a dictionary")
	}

	// 成功获取元数据，返回（不关闭连接，调用者负责关闭）
	return metadataMap, conn, peerExtenstionId, ourExtensionID, nil
}

// getPieceInfoFromMetadata 从元数据字典获取指定 piece 的信息
func getPieceInfoFromMetadata(metadataMap map[string]interface{}, pieceIndex int) (int, [20]byte, error) {
	// 获取标准 piece length
	pieceLength, ok := metadataMap["piece length"]
	if !ok {
		return 0, [20]byte{}, errors.New("'piece length' key not found")
	}
	pieceLengthInt, ok := pieceLength.(int)
	if !ok {
		return 0, [20]byte{}, errors.New("'piece length' value is not an integer")
	}

	// 获取文件总长度
	length, ok := metadataMap["length"]
	if !ok {
		return 0, [20]byte{}, errors.New("'length' key not found")
	}
	totalLength, ok := length.(int)
	if !ok {
		return 0, [20]byte{}, errors.New("'length' value is not an integer")
	}

	pieces, ok := metadataMap["pieces"]
	if !ok {
		return 0, [20]byte{}, errors.New("'pieces' key not found")
	}
	pieceHashesStr, ok := pieces.(string)
	if !ok {
		return 0, [20]byte{}, errors.New("'pieces' value is not a string")
	}
	pieceHashesBytes := []byte(pieceHashesStr)

	// pieces 是连接在一起的哈希值，每个 piece 的哈希是 20 字节
	// 计算指定 piece 的哈希值位置
	hashStart := pieceIndex * 20
	hashEnd := hashStart + 20

	if hashEnd > len(pieceHashesBytes) {
		return 0, [20]byte{}, fmt.Errorf("piece index %d out of range", pieceIndex)
	}

	pieceHash := [20]byte{}
	copy(pieceHash[:], pieceHashesBytes[hashStart:hashEnd])

	// 计算实际的 piece 长度
	// 最后一个 piece 的长度 = 总长度 - (pieceIndex * pieceLength)
	actualPieceLength := pieceLengthInt
	expectedStart := pieceIndex * pieceLengthInt
	if expectedStart+pieceLengthInt > totalLength {
		// 这是最后一个 piece
		actualPieceLength = totalLength - expectedStart
	}

	return actualPieceLength, pieceHash, nil
}
func getPeerAddressFromMagnet(trackerURL string, infoHashBytes []byte) ([]Address, error) {
	peerID := make([]byte, 20)
	_, err := rand.Read(peerID)
	if err != nil {
		return nil, fmt.Errorf("error generating peer id: %v", err)
	}
	parsedURL, err := url.Parse(trackerURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing tracker URL: %v", err)
	}
	queryParts := []string{
		"info_hash=" + url.QueryEscape(string(infoHashBytes)),
		"peer_id=" + url.QueryEscape(string(peerID)),
		"port=6881",
		"uploaded=0",
		"downloaded=0",
		"left=1",
		"compact=1",
	}
	parsedURL.RawQuery = strings.Join(queryParts, "&")
	resp, err := http.Get(parsedURL.String())
	if err != nil {
		return nil, fmt.Errorf("error making request to tracker: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error: tracker returned status code %d", resp.StatusCode)
	}
	// 读取 tracker 响应
	body := make([]byte, 0)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	// 解析 bencoded 响应
	responseStr := string(body)
	decodedResponse, _, err := decodeBencode(responseStr)
	if err != nil {
		return nil, fmt.Errorf("error decoding tracker response: %v", err)
	}
	// 类型断言为字典
	responseDict, ok := decodedResponse.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("error: tracker response is not a dictionary")
	}
	// 提取 peers（紧凑格式：每个 peer 6 字节，前 4 字节是 IP，后 2 字节是端口）
	peers, ok := responseDict["peers"]
	if !ok {
		return nil, fmt.Errorf("error: 'peers' key not found in tracker response")
	}

	peersStr, ok := peers.(string)
	if !ok {
		return nil, fmt.Errorf("error: 'peers' value is not a string")
	}

	// 解析 peers（紧凑格式）
	peersBytes := []byte(peersStr)
	if len(peersBytes)%6 != 0 {
		return nil, fmt.Errorf("error: invalid peers format, length is %d (should be multiple of 6)", len(peersBytes))
	}

	if len(peersBytes) == 0 {
		return nil, fmt.Errorf("error: no peers found in tracker response")
	}
	var addresses []Address
	for i := 0; i < len(peersBytes); i += 6 {
		ip := fmt.Sprintf("%d.%d.%d.%d", peersBytes[i], peersBytes[i+1], peersBytes[i+2], peersBytes[i+3])
		port := int(peersBytes[i+4])<<8 | int(peersBytes[i+5])
		addresses = append(addresses, Address{IP: ip, Port: port})
	}
	return addresses, nil
}
