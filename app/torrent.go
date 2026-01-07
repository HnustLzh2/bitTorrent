package main

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
)

// TODO 使用结构体去存储torrent的元数据

func getInfoFromTorrentFile(torrentFile string) string {
	torrentDict, err := getTorrentFileDict(torrentFile)
	if err != nil {
		return fmt.Sprintf("Error getting torrent file dict: %v", err)
	}
	// 提取 announce (tracker URL)
	announce, ok := torrentDict["announce"]
	if !ok {
		return "Error: 'announce' key not found"
	}
	announceStr, ok := announce.(string)
	if !ok {
		return "Error: 'announce' value is not a string"
	}

	// 提取 info 字典
	info, ok := torrentDict["info"]
	if !ok {
		return "Error: 'info' key not found"
	}
	infoDict, ok := info.(map[string]interface{})
	if !ok {
		return "Error: 'info' value is not a dictionary"
	}

	// 提取 length
	length, ok := infoDict["length"]
	if !ok {
		return "Error: 'length' key not found in info"
	}
	lengthInt, ok := length.(int)
	if !ok {
		return "Error: 'length' value is not an integer"
	}

	// 处理 pieces 字段：如果是 string，转换为 []byte 以保持原始字节
	infoDictForEncoding := make(map[string]interface{})
	for k, v := range infoDict {
		if k == "pieces" {
			// pieces 必须是字节数组
			if piecesStr, ok := v.(string); ok {
				infoDictForEncoding[k] = []byte(piecesStr)
			} else if piecesBytes, ok := v.([]byte); ok {
				infoDictForEncoding[k] = piecesBytes
			} else {
				return "Error: 'pieces' value has invalid type"
			}
		} else {
			infoDictForEncoding[k] = v
		}
	}

	// 编码 info 字典
	encodedInfo, err := encodeBencode(infoDictForEncoding)
	if err != nil {
		return fmt.Sprintf("Error encoding info dictionary: %v", err)
	}

	// 计算 SHA-1 哈希
	hash := sha1.Sum([]byte(encodedInfo))
	infoHash := fmt.Sprintf("%x", hash)

	// 获取Piece Length
	pieceLength, ok := infoDict["piece length"]
	if !ok {
		return "Error: 'piece length' key not found in info"
	}
	pieceLengthInt, ok := pieceLength.(int)
	if !ok {
		return "Error: 'piece length' value is not an integer"
	}
	// 获取Piece Hashes
	pieceHashes, ok := infoDict["pieces"]
	if !ok {
		return "Error: 'pieces' key not found in info"
	}
	pieceHashesStr, ok := pieceHashes.(string)
	if !ok {
		return "Error: 'pieces' value is not a string"
	}
	// 格式化输出
	return fmt.Sprintf("Tracker URL: %s\nLength: %d\nInfo Hash: %s\nPiece Length: %d\nPiece Hashes: %x", announceStr, lengthInt, infoHash, pieceLengthInt, pieceHashesStr)
}

func getPeerAddress(torrentFile string) (string, []Address) {
	torrentDict, err := getTorrentFileDict(torrentFile)
	if err != nil {
		return fmt.Sprintf("Error getting torrent file dict: %v", err), nil
	}
	return getPeerAddressFromDict(torrentDict)
}

// getPeerAddressFromDict 从已解析的 torrentDict 获取 peer 地址列表
func getPeerAddressFromDict(torrentDict map[string]interface{}) (string, []Address) {

	// 获取 announce URL
	announce, ok := torrentDict["announce"]
	if !ok {
		return "Error: 'announce' key not found", nil
	}
	announceStr, ok := announce.(string)
	if !ok {
		return "Error: 'announce' value is not a string", nil
	}

	// 获取 info 字典
	info, ok := torrentDict["info"]
	if !ok {
		return "Error: 'info' key not found", nil
	}
	infoDict, ok := info.(map[string]interface{})
	if !ok {
		return "Error: 'info' value is not a dictionary", nil
	}

	// 获取 length
	length, ok := infoDict["length"]
	if !ok {
		return "Error: 'length' key not found in info", nil
	}
	lengthInt, ok := length.(int)
	if !ok {
		return "Error: 'length' value is not an integer", nil
	}

	// 处理 pieces 字段：如果是 string，转换为 []byte 以保持原始字节
	infoDictForEncoding := make(map[string]interface{})
	for k, v := range infoDict {
		if k == "pieces" {
			if piecesStr, ok := v.(string); ok {
				infoDictForEncoding[k] = []byte(piecesStr)
			} else if piecesBytes, ok := v.([]byte); ok {
				infoDictForEncoding[k] = piecesBytes
			} else {
				return "Error: 'pieces' value has invalid type", nil
			}
		} else {
			infoDictForEncoding[k] = v
		}
	}

	// 编码 info 字典
	encodedInfo, err := encodeBencode(infoDictForEncoding)
	if err != nil {
		return fmt.Sprintf("Error encoding info dictionary: %v", err), nil
	}

	// 计算 SHA-1 哈希（20 字节的原始字节）
	hash := sha1.Sum([]byte(encodedInfo))
	infoHashBytes := hash[:] // 转换为 []byte

	// 生成 peer_id（20 字节的唯一标识符）
	peerID := []byte("-PC0001-123456789012") // 20 字节

	// 构建完整 URL
	trackerURL, err := url.Parse(announceStr)
	if err != nil {
		return fmt.Sprintf("error parsing tracker URL: %v", err), nil
	}

	// 手动构建查询字符串以确保 info_hash 和 peer_id 正确编码
	queryParts := []string{
		"info_hash=" + url.QueryEscape(string(infoHashBytes)),
		"peer_id=" + url.QueryEscape(string(peerID)),
		"port=6881",
		"uploaded=0",
		"downloaded=0",
		"left=" + strconv.Itoa(lengthInt),
		"compact=1",
	}
	trackerURL.RawQuery = strings.Join(queryParts, "&")

	// 发送 HTTP GET 请求
	resp, err := http.Get(trackerURL.String())
	if err != nil {
		return fmt.Sprintf("Error making request to tracker: %v", err), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("error: tracker returned status code %d", resp.StatusCode), nil
	}

	// 读取响应体
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
	decoded, _, err := decodeBencode(responseStr)
	if err != nil {
		return fmt.Sprintf("Error decoding tracker response: %v", err), nil
	}

	// 类型断言为字典
	responseDict, ok := decoded.(map[string]interface{})
	if !ok {
		return "error: tracker response is not a dictionary", nil
	}

	// 提取 peers（紧凑格式：每个 peer 6 字节，前 4 字节是 IP，后 2 字节是端口）
	peers, ok := responseDict["peers"]
	if !ok {
		return "error: 'peers' key not found in tracker response", nil
	}

	peersStr, ok := peers.(string)
	if !ok {
		return "error: 'peers' value is not a string", nil
	}

	// 解析 peers（紧凑格式）
	peersBytes := []byte(peersStr)
	if len(peersBytes)%6 != 0 {
		return fmt.Sprintf("error: invalid peers format, length is %d (should be multiple of 6)", len(peersBytes)), nil
	}

	// 格式化输出 peer 地址
	// 前四位是ip，后2为是端口
	var result strings.Builder

	var peersList []Address
	for i := 0; i < len(peersBytes); i += 6 {
		ip := fmt.Sprintf("%d.%d.%d.%d", peersBytes[i], peersBytes[i+1], peersBytes[i+2], peersBytes[i+3])
		port := int(peersBytes[i+4])<<8 | int(peersBytes[i+5])
		result.WriteString(fmt.Sprintf("%s:%d", ip, port))
		peersList = append(peersList, Address{IP: ip, Port: port})
	}

	return result.String(), peersList
}

func getTorrentFileDict(torrentFile string) (map[string]interface{}, error) {
	data, err := os.ReadFile(torrentFile)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %v", err)
	}
	// 将字节转换为字符串（Go 的 string 可以包含任意字节）
	bencodedString := string(data)

	// 解析 bencoded 字典
	decoded, _, err := decodeBencode(bencodedString)
	if err != nil {
		return nil, fmt.Errorf("error decoding bencoded string: %v", err)
	}

	// 类型断言为字典
	torrentDict, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("error: decoded value is not a dictionary")
	}

	return torrentDict, nil
}

func handshake(torrentFile string, address string) string {
	// 获取 info hash（20 字节原始字节）
	infoHashBytes, err := getInfoHashBytes(torrentFile)
	if err != nil {
		return fmt.Sprintf("error getting info hash: %v", err)
	}

	// 生成随机 peer id（20 字节）
	peerID := make([]byte, 20)
	_, err = rand.Read(peerID)
	if err != nil {
		return fmt.Sprintf("error generating peer id: %v", err)
	}

	// 建立 TCP 连接
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return fmt.Sprintf("error connecting to peer: %v", err)
	}
	defer conn.Close()

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
		return fmt.Sprintf("error sending handshake: %v", err)
	}

	// 接收握手响应（68 字节）
	response := make([]byte, 68)
	totalRead := 0
	for totalRead < 68 {
		n, err := conn.Read(response[totalRead:])
		if err != nil {
			return fmt.Sprintf("error receiving handshake: %v", err)
		}
		totalRead += n
	}

	// 验证响应格式
	if totalRead < 68 {
		return fmt.Sprintf("error: handshake response too short, got %d bytes", totalRead)
	}

	// 验证协议字符串长度
	if response[0] != 19 {
		return fmt.Sprintf("error: invalid protocol string length, got %d", response[0])
	}

	// 验证协议字符串
	protocolStr := string(response[1:20])
	if protocolStr != "BitTorrent protocol" {
		return fmt.Sprintf("error: invalid protocol string, got %s", protocolStr)
	}

	// 提取对方发送的 peer id（最后 20 字节）
	receivedPeerID := response[48:68]

	// 转换为十六进制字符串
	peerIDHex := hex.EncodeToString(receivedPeerID)

	return fmt.Sprintf("Peer ID: %s", peerIDHex)
}

// getInfoHashBytes 获取 info hash 的原始字节（20 字节）
func getInfoHashBytes(torrentFile string) ([]byte, error) {
	torrentDict, err := getTorrentFileDict(torrentFile)
	if err != nil {
		return nil, err
	}
	return getInfoHashBytesFromDict(torrentDict)
}

// getInfoHashBytesFromDict 从已解析的 torrentDict 获取 info hash 字节
func getInfoHashBytesFromDict(torrentDict map[string]interface{}) ([]byte, error) {
	// 获取 info 字典
	info, ok := torrentDict["info"]
	if !ok {
		return nil, errors.New("'info' key not found")
	}
	infoDict, ok := info.(map[string]interface{})
	if !ok {
		return nil, errors.New("'info' value is not a dictionary")
	}

	// 处理 pieces 字段：如果是 string，转换为 []byte 以保持原始字节
	infoDictForEncoding := make(map[string]interface{})
	for k, v := range infoDict {
		if k == "pieces" {
			if piecesStr, ok := v.(string); ok {
				infoDictForEncoding[k] = []byte(piecesStr)
			} else {
				return nil, errors.New("'pieces' value has invalid type")
			}
		} else {
			infoDictForEncoding[k] = v
		}
	}

	// 编码 info 字典
	encodedInfo, err := encodeBencode(infoDictForEncoding)
	if err != nil {
		return nil, fmt.Errorf("error encoding info dictionary: %v", err)
	}

	// 计算 SHA-1 哈希（20 字节的原始字节）
	hash := sha1.Sum([]byte(encodedInfo))
	return hash[:], nil
}

func downloadPiece(tag string, piecePath string, torrentFile string, pieceIndex int) ([]byte, error) {
	_, peersList := getPeerAddress(torrentFile)
	if len(peersList) == 0 {
		return nil, fmt.Errorf("no peers found")
	}
	infoHashBytes, err := getInfoHashBytes(torrentFile)
	if err != nil {
		return nil, fmt.Errorf("error getting info hash: %v", err)
	}

	// 尝试连接到每个 peer，直到成功
	var conn net.Conn
	for _, address := range peersList {
		conn, err = performHandshakeWithPeer(address, infoHashBytes)
		if err != nil {
			continue // 尝试下一个 peer
		}

		// 等待 bitfield 消息
		err = waitForBitfield(conn)
		if err != nil {
			conn.Close()
			continue // 尝试下一个 peer
		}

		// 发送 interested 消息
		err = sendInterested(conn)
		if err != nil {
			conn.Close()
			continue // 尝试下一个 peer
		}

		// 等待 unchoke 消息
		err = waitForUnchoke(conn)
		if err != nil {
			conn.Close()
			continue // 尝试下一个 peer
		}

		// 成功建立连接并完成初始消息交换
		break
	}

	if conn == nil {
		return nil, fmt.Errorf("failed to connect to any peer")
	}

	defer conn.Close()

	// 获取 info 字典
	torrentDict, err := getTorrentFileDict(torrentFile)
	if err != nil {
		return nil, fmt.Errorf("error parsing torrent file: %v", err)
	}
	info, ok := torrentDict["info"]
	if !ok {
		return nil, fmt.Errorf("'info' key not found")
	}
	infoDict, ok := info.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("'info' value is not a dictionary")
	}

	data, err := downloadPieceReuseConn(conn, infoDict, pieceIndex)
	if err != nil {
		return nil, fmt.Errorf("error downloading piece: %v", err)
	}

	// 保存 piece 到文件
	err = savePieceToFile(data, piecePath)
	if err != nil {
		return nil, fmt.Errorf("error saving piece: %v", err)
	}

	return data, nil
}

// downloadPieceReuseConn 使用已建立的连接下载 piece（不保存到文件）
func downloadPieceReuseConn(conn net.Conn, infoDict map[string]interface{}, pieceIndex int) ([]byte, error) {
	pieceLength, pieceHash, err := getPieceInfoFromDict(infoDict, pieceIndex)
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

func download(savePath string, torrentFile string) error {
	torrentDict, err := getTorrentFileDict(torrentFile)
	if err != nil {
		return fmt.Errorf("error parsing torrent file: %v", err)
	}
	piecesLen, dataLen, err := getTotalPiecesFromDict(torrentDict)
	if err != nil {
		return err
	}
	piecesMap := map[int][]byte{}
	for i := 0; i < piecesLen; i++ {
		pieceBytes, err := downloadPiece("", PieceFilesDir, torrentFile, i)
		if err != nil {
			return err
		}
		piecesMap[i] = pieceBytes
	}
	combinedFileData, err := combinePieces(piecesMap, dataLen)
	if err != nil {
		return err
	}
	err = os.WriteFile(savePath, combinedFileData, 0644)
	if err != nil {
		return fmt.Errorf("error writing combined file: %v", err)
	}
	return nil
}

func downloadFileConcurrent(torrentFile string, savePath string) error {
	// 只解析一次 torrent 文件
	torrentDict, err := getTorrentFileDict(torrentFile)
	if err != nil {
		return fmt.Errorf("error parsing torrent file: %v", err)
	}

	// 从已解析的 torrentDict 获取信息
	piecesLen, dataLen, err := getTotalPiecesFromDict(torrentDict)
	if err != nil {
		return err
	}
	_, peerList := getPeerAddressFromDict(torrentDict)
	if len(peerList) == 0 {
		return fmt.Errorf("no peers found")
	}
	infoHashBytes, err := getInfoHashBytesFromDict(torrentDict)
	if err != nil {
		return err
	}

	// 获取 info 字典，传递给 workers
	info, ok := torrentDict["info"]
	if !ok {
		return errors.New("'info' key not found")
	}
	infoDict, ok := info.(map[string]interface{})
	if !ok {
		return errors.New("'info' value is not a dictionary")
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
	for i := 0; i < len(peerList); i++ {
		wg.Add(1)
		peer := peerList[i] // 创建局部变量，避免闭包问题
		go func(peer Address) {
			defer wg.Done()
			err := downloadPieceWithPeer(peer, infoDict, queue, buffer, infoHashBytes)
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
	err = os.WriteFile(savePath, combinedFileData, 0644)
	if err != nil {
		return fmt.Errorf("error writing combined file: %v", err)
	}

	return nil
}
