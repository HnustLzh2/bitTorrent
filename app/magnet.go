package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// TODO 使用结构体去存储magnet的元数据

// magnetHandshake 执行magnet握手，返回：连接、响应字符串、peerID、对方的扩展ID、我们自己的扩展ID、错误
func magnetHandshake(decoded map[string]string) (net.Conn, string, string, int, int, error) {
	infoHashHex := decoded["Info Hash"]
	trackerURL := decoded["Tracker URL"]
	// 将十六进制字符串转换为20字节的原始字节
	infoHashBytes, err := hex.DecodeString(infoHashHex)
	if err != nil {
		return nil, "", "", 0, 0, fmt.Errorf("error decoding info hash: %v", err)
	}
	if len(infoHashBytes) != 20 {
		return nil, "", "", 0, 0, fmt.Errorf("error: info hash must be 20 bytes, got %d", len(infoHashBytes))
	}

	// 生成随机的peerID（用于 tracker 请求和握手）
	peerID := make([]byte, 20)
	_, err = rand.Read(peerID)
	if err != nil {
		return nil, "", "", 0, 0, fmt.Errorf("error generating peer id: %v", err)
	}

	// 步骤1: 通过 HTTP GET 请求 tracker 获取 peer 列表
	parsedURL, err := url.Parse(trackerURL)
	if err != nil {
		return nil, "", "", 0, 0, fmt.Errorf("error parsing tracker URL: %v", err)
	}

	// 构建 tracker 请求 URL（使用 compact=1 格式）
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

	// 发送 HTTP GET 请求到 tracker
	resp, err := http.Get(parsedURL.String())
	if err != nil {
		return nil, "", "", 0, 0, fmt.Errorf("error making request to tracker: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", "", 0, 0, fmt.Errorf("error: tracker returned status code %d", resp.StatusCode)
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
		return nil, "", "", 0, 0, fmt.Errorf("error decoding tracker response: %v", err)
	}

	// 类型断言为字典
	responseDict, ok := decodedResponse.(map[string]interface{})
	if !ok {
		return nil, "", "", 0, 0, fmt.Errorf("error: tracker response is not a dictionary")
	}

	// 提取 peers（紧凑格式：每个 peer 6 字节，前 4 字节是 IP，后 2 字节是端口）
	peers, ok := responseDict["peers"]
	if !ok {
		return nil, "", "", 0, 0, fmt.Errorf("error: 'peers' key not found in tracker response")
	}

	peersStr, ok := peers.(string)
	if !ok {
		return nil, "", "", 0, 0, fmt.Errorf("error: 'peers' value is not a string")
	}

	// 解析 peers（紧凑格式）
	peersBytes := []byte(peersStr)
	if len(peersBytes)%6 != 0 {
		return nil, "", "", 0, 0, fmt.Errorf("error: invalid peers format, length is %d (should be multiple of 6)", len(peersBytes))
	}

	if len(peersBytes) == 0 {
		return nil, "", "", 0, 0, fmt.Errorf("error: no peers found in tracker response")
	}

	// 步骤2: 尝试连接到每个 peer，直到成功完成握手
	// 我们使用的ut_metadata扩展ID（告诉对方的）
	ourExtensionID := byte(1)
	var conn net.Conn
	var receivedPeerID []byte
	var peerExtenstionId int

	// 循环尝试所有 peers
	for i := 0; i < len(peersBytes); i += 6 {
		// 解析 peer 的 IP 和端口
		ip := fmt.Sprintf("%d.%d.%d.%d", peersBytes[i], peersBytes[i+1], peersBytes[i+2], peersBytes[i+3])
		port := int(peersBytes[i+4])<<8 | int(peersBytes[i+5])
		peerAddress := net.JoinHostPort(ip, fmt.Sprintf("%d", port))

		// 建立 TCP 连接
		conn, err = net.Dial("tcp", peerAddress)
		if err != nil {
			continue // 尝试下一个 peer
		}

		// 步骤3: 执行 BitTorrent 握手（设置扩展支持位）
		handshakeMsg := make([]byte, 0, 68)
		handshakeMsg = append(handshakeMsg, 19)
		handshakeMsg = append(handshakeMsg, []byte("BitTorrent protocol")...)
		// 8字节 = 64位，从右起第20位（0-based）意味着是第21个位
		// 如果按大端序（从左到右），位索引：63(最左) ... 20 ... 0(最右)
		// 第20位：20 / 8 = 2余4，但根据期望结果 [0 0 0 0 0 16 0 0]，应该是索引5的字节
		// 重新计算：如果从右起计数，64位中第20位（0-based）在索引5的字节的第4位
		// 20 = 5*8 + 4，所以是 reserved[5] 的第4位（从右数，0-based）
		reserved := make([]byte, 8)
		// 设置索引5的字节的第4位为1：reserved[5] |= (1 << 4)
		reserved[5] |= (1 << 4)

		handshakeMsg = append(handshakeMsg, reserved...)
		handshakeMsg = append(handshakeMsg, infoHashBytes...)
		handshakeMsg = append(handshakeMsg, peerID...)
		_, err = conn.Write(handshakeMsg)
		if err != nil {
			conn.Close()
			continue // 尝试下一个 peer
		}

		// 步骤4: 接收握手响应
		response := make([]byte, 68)
		totalRead := 0
		for totalRead < 68 {
			n, err := conn.Read(response[totalRead:])
			if err != nil {
				conn.Close()
				break // 跳出内层循环，继续尝试下一个 peer
			}
			totalRead += n
		}

		// 如果读取失败，尝试下一个 peer
		if totalRead < 68 {
			continue
		}

		// 验证响应格式
		if response[0] != 19 {
			conn.Close()
			continue // 尝试下一个 peer
		}

		// 验证协议字符串
		protocolStr := string(response[1:20])
		if protocolStr != "BitTorrent protocol" {
			conn.Close()
			continue // 尝试下一个 peer
		}

		// 提取保留字节（索引20-27）
		reservedBytes := response[20:28]

		// 步骤5: 等待并接收 bitfield 消息
		err = waitForBitfield(conn)
		if err != nil {
			conn.Close()
			continue // 尝试下一个 peer
		}

		// 步骤6: 检查对方是否支持扩展，如果支持则发送扩展握手消息
		peerExtenstionId = 0
		if supportsExtensions(reservedBytes) {
			// 选择 ut_metadata 的扩展ID（1-255之间，不能是0）
			// 这里选择1作为扩展ID（这是我们告诉对方的ID）
			extensionHandshakeMsg, err := buildExtensionHandshakeMessage(ourExtensionID)
			if err != nil {
				conn.Close()
				continue // 尝试下一个 peer
			}

			// 发送扩展握手消息
			_, err = conn.Write(extensionHandshakeMsg)
			if err != nil {
				conn.Close()
				continue // 尝试下一个 peer
			}
			// 接受扩展握手消息
			messageID, payload, err := readPeerMessage(conn)
			if err != nil {
				conn.Close()
				continue // 尝试下一个 peer
			}
			if messageID != 20 {
				conn.Close()
				continue // 尝试下一个 peer
			}
			if len(payload) == 0 {
				conn.Close()
				continue // 尝试下一个 peer
			}
			extenstionID := payload[0]
			if extenstionID != 0 {
				conn.Close()
				continue // 尝试下一个 peer
			}
			// 解析负荷的bencoded字典
			extensionDict, _, err := decodeBencode(string(payload[1:]))
			if err != nil {
				conn.Close()
				continue // 尝试下一个 peer
			}
			extensionDictMap, ok := extensionDict.(map[string]interface{})
			if !ok {
				conn.Close()
				continue // 尝试下一个 peer
			}
			utMetadataMap, ok := extensionDictMap["m"].(map[string]interface{})
			if !ok {
				conn.Close()
				continue // 尝试下一个 peer
			}
			peerExtenstionId, ok = utMetadataMap["ut_metadata"].(int)
			if !ok {
				conn.Close()
				continue // 尝试下一个 peer
			}
		}

		// 提取对方发送的 peer id（最后 20 字节）
		receivedPeerID = response[48:68]

		// 成功完成握手，跳出循环
		break
	}

	// 检查是否成功连接到 peer
	if conn == nil {
		return nil, "", "", 0, 0, fmt.Errorf("error: failed to connect to any peer")
	}

	// 转换为十六进制字符串
	peerIDHex := hex.EncodeToString(receivedPeerID)
	// 返回：连接、响应字符串、peerID、对方的扩展ID、我们自己的扩展ID
	return conn, fmt.Sprintf("Peer ID: %s\nPeer Metadata Extension ID: %d", peerIDHex, peerExtenstionId), peerIDHex, peerExtenstionId, int(ourExtensionID), nil
}

// magnetInfo 实现magnet_info命令，获取并解析元数据，返回格式化字符串
func magnetInfo(decoded map[string]string) string {
	// 直接调用 magnetHandshake 和后续逻辑获取元数据
	// TODO: 重构为使用 getMetadataFromMagnet 函数
	conn, _, _, peerExtenstionId, ourExtensionID, err := magnetHandshake(decoded)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	defer conn.Close() // magnet_info 命令使用完后关闭连接

	// 构建并发送元数据请求消息
	metadataRequestMessage, err := buildMetadataRequestMessage(byte(peerExtenstionId))
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	_, err = conn.Write(metadataRequestMessage)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}

	// 接收元数据响应消息
	messageID, payload, err := readPeerMessage(conn)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}

	// 验证消息ID（应该是20，表示扩展消息）
	if messageID != 20 {
		return fmt.Sprintf("error: invalid metadata response message ID, got %d", messageID)
	}
	if len(payload) == 0 {
		return "error: metadata response payload is empty"
	}

	// 验证扩展消息ID
	extensionID := payload[0]
	if extensionID != byte(ourExtensionID) {
		return fmt.Sprintf("error: invalid extension ID, expected %d (our extension ID), got %d", ourExtensionID, extensionID)
	}

	// 解析bencoded字典部分（包含msg_type, piece, total_size）
	dictStr := string(payload[1:])
	decodedDict, consumed, err := decodeBencode(dictStr)
	if err != nil {
		return fmt.Sprintf("error decoding metadata response dict: %v", err)
	}

	decodedDictMap, ok := decodedDict.(map[string]interface{})
	if !ok {
		return "error: metadata response dict is not a dictionary"
	}

	// 验证并提取字典中的字段
	msgType, ok := decodedDictMap["msg_type"].(int)
	if !ok || msgType != 1 {
		return fmt.Sprintf("error: invalid msg_type, expected 1, got %d", msgType)
	}

	totalSize, ok := decodedDictMap["total_size"].(int)
	if !ok {
		return fmt.Sprintf("error: invalid total_size, got %v", decodedDictMap["total_size"])
	}

	// 提取元数据内容（在字典之后）
	metadataStart := 1 + consumed
	if metadataStart+totalSize > len(payload) {
		return fmt.Sprintf("error: metadata size mismatch, expected %d bytes, got %d bytes available", totalSize, len(payload)-metadataStart)
	}

	// 提取元数据内容（bencoded字典）
	metadataBytes := payload[metadataStart : metadataStart+totalSize]

	// 对元数据内容进行SHA1哈希验证
	infoHashHex := decoded["Info Hash"]
	infoHashBytes, err := hex.DecodeString(infoHashHex)
	if err != nil {
		return fmt.Sprintf("error decoding info hash: %v", err)
	}
	metadataHash := sha1.Sum(metadataBytes)
	if !bytes.Equal(metadataHash[:], infoHashBytes) {
		return "error: metadata hash verification failed"
	}

	// 解析元数据内容（bencoded字典）
	metadataDict, _, err := decodeBencode(string(metadataBytes))
	if err != nil {
		return fmt.Sprintf("error decoding metadata content: %v", err)
	}

	metadataMap, ok := metadataDict.(map[string]interface{})
	if !ok {
		return "error: metadata content is not a dictionary"
	}

	// 提取元数据字段并格式化输出
	length, ok := metadataMap["length"].(int)
	if !ok {
		return fmt.Sprintf("error: invalid length in metadata, got %v", metadataMap["length"])
	}

	pieceLength, ok := metadataMap["piece length"].(int)
	if !ok {
		return fmt.Sprintf("error: invalid piece length in metadata, got %v", metadataMap["piece length"])
	}

	pieces, ok := metadataMap["pieces"].(string)
	if !ok {
		return fmt.Sprintf("error: invalid pieces in metadata, got %v", metadataMap["pieces"])
	}

	// 格式化输出
	// pieces是连接在一起的哈希值，每个piece的哈希是20字节
	// 需要格式化为多行输出
	trackerURL := decoded["Tracker URL"]
	// infoHashHex 已在上面声明，这里直接使用
	var pieceHashesBuilder strings.Builder
	piecesBytes := []byte(pieces)
	for i := 0; i < len(piecesBytes); i += 20 {
		if i+20 <= len(piecesBytes) {
			pieceHash := hex.EncodeToString(piecesBytes[i : i+20])
			if pieceHashesBuilder.Len() > 0 {
				pieceHashesBuilder.WriteString("\n")
			}
			pieceHashesBuilder.WriteString(pieceHash)
		}
	}
	response := fmt.Sprintf("Tracker URL: %s\nLength: %d\nInfo Hash: %s\nPiece Length: %d\nPiece Hashes:\n%s",
		trackerURL, length, infoHashHex, pieceLength, pieceHashesBuilder.String())
	return response
}

// downloadPieceWithMagnetReuseConn 使用已建立的连接下载 piece（不保存到文件）
func downloadPieceWithMagnetReuseConn(conn net.Conn, metadataMap map[string]interface{}, pieceIndex int) ([]byte, error) {
	// 从元数据中获取 piece 信息
	pieceLength, pieceHash, err := getPieceInfoFromMetadata(metadataMap, pieceIndex)
	if err != nil {
		return nil, fmt.Errorf("error getting piece info: %v", err)
	}

	// 计算需要多少个 blocks（每个 block 16KB = 16384 字节）
	blockSize := 16384
	numBlocks := (pieceLength + blockSize - 1) / blockSize // 向上取整

	// 管道化请求：同时保持最多 5 个待处理的请求
	const maxPendingRequests = 5
	blocks := make(map[int][]byte)
	nextBlockIndex := 0  //下一个要发送的 block 索引
	pendingRequests := 0 //当前待处理的请求数量

	// 先发送初始的请求（最多 5 个或所有 blocks，取较小值）
	initialRequests := maxPendingRequests
	if numBlocks < initialRequests {
		initialRequests = numBlocks
	}

	for i := 0; i < initialRequests; i++ {
		begin := i * blockSize
		length := blockSize
		// 最后一个 block 可能小于 blockSize
		if begin+length > pieceLength {
			length = pieceLength - begin
		}

		err = sendRequest(conn, BlockInfo{
			Index:  pieceIndex,
			Begin:  begin,
			Length: length,
		})
		if err != nil {
			return nil, fmt.Errorf("error sending request: %v", err)
		}
		pendingRequests++
		nextBlockIndex++
	}

	// 接收所有 blocks，每收到一个就发送下一个请求
	for len(blocks) < numBlocks {
		index, begin, block, err := receivePiece(conn)
		if err != nil {
			return nil, fmt.Errorf("error receiving piece: %v", err)
		}

		// 验证 index 是否正确
		if index != pieceIndex {
			return nil, fmt.Errorf("received wrong piece index: expected %d, got %d", pieceIndex, index)
		}

		blocks[begin] = block
		pendingRequests--

		// 如果还有未发送的请求，发送下一个
		if nextBlockIndex < numBlocks {
			begin := nextBlockIndex * blockSize
			length := blockSize
			// 最后一个 block 可能小于 blockSize
			if begin+length > pieceLength {
				length = pieceLength - begin
			}

			err = sendRequest(conn, BlockInfo{
				Index:  pieceIndex,
				Begin:  begin,
				Length: length,
			})
			if err != nil {
				return nil, fmt.Errorf("error sending request: %v", err)
			}
			pendingRequests++
			nextBlockIndex++
		}
	}

	// 组合 blocks 成 piece
	piece, err := combineBlocks(blocks, pieceLength)
	if err != nil {
		return nil, fmt.Errorf("error combining blocks: %v", err)
	}

	// 验证 piece 哈希
	if !verifyPieceHash(piece, pieceHash[:]) {
		return nil, fmt.Errorf("piece hash verification failed")
	}

	return piece, nil
}

func downloadPieceWithMagnet(piecePath string, pieceIndex int, decodedMap map[string]string) ([]byte, error) {
	// 步骤1: 获取元数据和连接
	metadataMap, conn, _, _, err := getMetadataFromMagnet(decodedMap)
	if err != nil {
		return nil, fmt.Errorf("error getting metadata from magnet: %v", err)
	}
	defer conn.Close()

	// 步骤2: 发送 interested 消息（如果还没有发送）
	// 注意：在 getMetadataFromMagnet 中已经完成了扩展握手，但可能还没有发送 interested
	err = sendInterested(conn)
	if err != nil {
		return nil, fmt.Errorf("error sending interested: %v", err)
	}

	// 步骤3: 等待 unchoke 消息
	err = waitForUnchoke(conn)
	if err != nil {
		return nil, fmt.Errorf("error waiting for unchoke: %v", err)
	}

	// 使用复用连接的函数下载 piece
	piece, err := downloadPieceWithMagnetReuseConn(conn, metadataMap, pieceIndex)
	if err != nil {
		return nil, err
	}

	// 保存 piece 到文件
	err = savePieceToFile(piece, piecePath)
	if err != nil {
		return nil, fmt.Errorf("error saving piece: %v", err)
	}

	return piece, nil
}

func downloadFileConcurrentWithMagnet(decodedMap map[string]string, filePath string) error {
	// 获取元数据（只需要获取一次）
	metadataMap, conn, _, _, err := getMetadataFromMagnet(decodedMap)
	if err != nil {
		return fmt.Errorf("error getting metadata: %v", err)
	}
	conn.Close() // 关闭元数据获取时建立的连接，后续会为每个 peer 建立新连接

	// 从元数据中获取 pieces 数量和文件长度
	pieces := metadataMap["pieces"]
	piecesStr, ok := pieces.(string)
	if !ok {
		return fmt.Errorf("'pieces' value is not a string")
	}
	piecesBytes := []byte(piecesStr)
	piecesLen := len(piecesBytes) / 20

	length, ok := metadataMap["length"]
	if !ok {
		return fmt.Errorf("'length' key not found")
	}
	dataLen, ok := length.(int)
	if !ok {
		return fmt.Errorf("'length' value is not an integer")
	}

	// 获取 peer 列表
	trackerURL := decodedMap["Tracker URL"]
	infoHashHex := decodedMap["Info Hash"]
	infoHashBytes, err := hex.DecodeString(infoHashHex)
	if err != nil {
		return fmt.Errorf("error decoding info hash: %v", err)
	}
	addressList, err := getPeerAddressFromMagnet(trackerURL, infoHashBytes)
	if err != nil {
		return fmt.Errorf("error getting peer address: %v", err)
	}
	if len(addressList) == 0 {
		return fmt.Errorf("no peers found")
	}

	// 初始化工作队列
	queue := &WorkQueue{}
	for i := 0; i < piecesLen; i++ {
		queue.Add(i)
	}

	// 初始化 piece 缓冲区
	buffer := &PieceBuffer{
		pieces: make(map[int][]byte),
	}
	// 启动多个 worker goroutines
	var wg sync.WaitGroup
	for i := 0; i < len(addressList); i++ {
		wg.Add(1)
		peer := addressList[i] // 创建局部变量，避免闭包问题
		go func(peer Address) {
			defer wg.Done()
			err := downloadPieceWithPeerByMagnet(peer, metadataMap, queue, buffer, infoHashBytes)
			if err != nil {
				// 记录错误但不中断其他 workers
				fmt.Fprintf(os.Stderr, "Worker error with peer %s:%d: %v\n", peer.IP, peer.Port, err)
			}
		}(peer)
	}
	// 等待所有 workers 完成
	wg.Wait()

	// 检查是否所有 pieces 都已下载
	if !buffer.HasAll(piecesLen) {
		downloaded := buffer.Size()
		return fmt.Errorf("not all pieces downloaded: %d/%d pieces downloaded", downloaded, piecesLen)
	}

	// 组合所有 pieces 成完整文件
	combinedFileData, err := combinePieces(buffer.pieces, dataLen)
	if err != nil {
		return fmt.Errorf("error combining pieces: %v", err)
	}

	// 保存文件
	err = os.WriteFile(filePath, combinedFileData, 0644)
	if err != nil {
		return fmt.Errorf("error writing combined file: %v", err)
	}
	return nil
}
