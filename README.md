# BitTorrent 客户端

一个用 Go 语言实现的 BitTorrent 客户端，支持通过 `.torrent` 文件或磁力链接下载文件。

## 功能特性

### Torrent 文件支持
- ✅ 解析 Bencode 编码格式
- ✅ 提取 torrent 文件信息（tracker URL、文件长度、piece 信息等）
- ✅ 从 tracker 获取 peer 列表
- ✅ 与 peer 建立连接并执行握手
- ✅ 下载单个 piece 或完整文件
- ✅ 支持并发下载多个 pieces

### 磁力链接支持
- ✅ 解析磁力链接（提取 info hash 和 tracker URL）
- ✅ 通过磁力链接获取元数据（metadata）
- ✅ 使用 ut_metadata 扩展协议
- ✅ 通过磁力链接下载单个 piece
- ✅ 通过磁力链接下载完整文件

## 安装和编译

### 编译
```bash
go build -o your_program.sh ./app
```

## 命令说明

### 1. Bencode 解码 (`decode`)
解码 Bencode 编码的字符串并输出 JSON 格式。

**用法：**
```bash
./your_program.sh decode <bencoded_string>
```

**示例：**
```bash
./your_program.sh decode "i52e"
# 输出: 52

./your_program.sh decode "5:hello"
# 输出: "hello"
```

---

### 2. 获取 Torrent 信息 (`info`)
从 `.torrent` 文件中提取并显示基本信息。

**用法：**
```bash
./your_program.sh info <torrent_file>
```

**输出信息：**
- Tracker URL
- 文件长度
- Info Hash
- Piece Length
- Piece Hashes

**示例：**
```bash
./your_program.sh info sample.torrent
```

---

### 3. 获取 Peer 列表 (`peers`)
从 tracker 获取可用的 peer 地址列表。

**用法：**
```bash
./your_program.sh peers <torrent_file>
```

**输出格式：**
```
<ip1>:<port1>
<ip2>:<port2>
...
```

**示例：**
```bash
./your_program.sh peers sample.torrent
```

---

### 4. 执行握手 (`handshake`)
与指定的 peer 建立连接并执行 BitTorrent 握手。

**用法：**
```bash
./your_program.sh handshake <torrent_file> <peer_address>
```

**参数：**
- `torrent_file`: .torrent 文件路径
- `peer_address`: peer 地址，格式为 `ip:port`

**示例：**
```bash
./your_program.sh handshake sample.torrent 127.0.0.1:6881
```

---

### 5. 下载单个 Piece (`download_piece`)
从 torrent 文件下载指定的 piece 并保存到文件。

**用法：**
```bash
./your_program.sh download_piece <tag> <output_path> <torrent_file> <piece_index>
```

**参数：**
- `tag`: 标签（可选，用于调试）
- `output_path`: 输出文件路径
- `torrent_file`: .torrent 文件路径
- `piece_index`: piece 索引（从 0 开始）

**示例：**
```bash
./your_program.sh download_piece -o /tmp/piece-0 sample.torrent 0
```

---

### 6. 下载完整文件 (`download`)
下载 torrent 文件中的所有 pieces 并组合成完整文件。

**用法：**
```bash
./your_program.sh download <output_path> <torrent_file>
```

**参数：**
- `output_path`: 输出文件路径
- `torrent_file`: .torrent 文件路径

**特性：**
- 支持并发下载多个 pieces
- 自动验证每个 piece 的哈希值
- 自动组合所有 pieces 成完整文件

**示例：**
```bash
./your_program.sh download /tmp/output.bin sample.torrent
```

---

### 7. 解析磁力链接 (`magnet_parse`)
解析磁力链接并显示提取的信息。

**用法：**
```bash
./your_program.sh magnet_parse <magnet_link>
```

**输出信息：**
- Info Hash
- Tracker URL

**示例：**
```bash
./your_program.sh magnet_parse "magnet:?xt=urn:btih:ad42ce8109f54c99613ce38f9b4d87e70f24a165&tr=http://tracker.example.com/announce"
```

---

### 8. 磁力链接握手 (`magnet_handshake`)
通过磁力链接与 peer 建立连接并执行握手（包括扩展握手）。

**用法：**
```bash
./your_program.sh magnet_handshake <magnet_link>
```

**输出信息：**
- Peer ID
- Peer Metadata Extension ID

**示例：**
```bash
./your_program.sh magnet_handshake "magnet:?xt=urn:btih:..."
```

---

### 9. 获取磁力链接元数据 (`magnet_info`)
通过磁力链接获取并显示 torrent 元数据信息。

**用法：**
```bash
./your_program.sh magnet_info <magnet_link>
```

**输出信息：**
- Tracker URL
- 文件长度
- Info Hash
- Piece Length
- Piece Hashes（每行一个）

**示例：**
```bash
./your_program.sh magnet_info "magnet:?xt=urn:btih:..."
```

---

### 10. 通过磁力链接下载 Piece (`magnet_download_piece`)
通过磁力链接下载指定的 piece 并保存到文件。

**用法：**
```bash
./your_program.sh magnet_download_piece <tag> <output_path> <magnet_link> <piece_index>
```

**参数：**
- `tag`: 标签（可选，用于调试）
- `output_path`: 输出文件路径
- `magnet_link`: 磁力链接
- `piece_index`: piece 索引（从 0 开始）

**工作流程：**
1. 解析磁力链接获取 tracker URL 和 info hash
2. 向 tracker 发送请求获取 peer 列表
3. 与 peer 建立 TCP 连接并执行基础握手
4. 执行扩展握手（ut_metadata 协议）
5. 通过元数据扩展获取 info 字典
6. 下载指定 piece 的所有 blocks
7. 验证 piece 哈希并保存到文件

**示例：**
```bash
./your_program.sh magnet_download_piece -o /tmp/test-piece-0 "magnet:?xt=urn:btih:..." 0
```

---

### 11. 通过磁力链接下载完整文件 (`magnet_download`)
通过磁力链接下载所有 pieces 并组合成完整文件。

**用法：**
```bash
./your_program.sh magnet_download <output_path> <magnet_link>
```

**参数：**
- `output_path`: 输出文件路径
- `magnet_link`: 磁力链接

**工作流程：**
1. 解析磁力链接获取 tracker URL 和 info hash
2. 通过 ut_metadata 扩展获取元数据（info 字典）
3. 从 tracker 获取 peer 列表
4. 并发连接多个 peers，每个 peer 下载不同的 pieces
5. 使用连接复用技术，每个 peer 连接只建立一次
6. 自动验证每个 piece 的哈希值
7. 组合所有 pieces 成完整文件并保存

**特性：**
- 支持并发下载多个 pieces
- 连接复用：每个 worker 只建立一次连接，用于下载多个 pieces
- 自动验证每个 piece 的哈希值
- 自动组合所有 pieces 成完整文件

**示例：**
```bash
./your_program.sh magnet_download -o /tmp/sample "magnet:?xt=urn:btih:..."
```

## 技术实现

### 核心协议
- **Bencode 编码/解码**：BitTorrent 使用的数据编码格式
- **BitTorrent 协议**：peer-to-peer 文件传输协议
- **ut_metadata 扩展**：用于通过磁力链接获取元数据

### 关键功能
- **管道化下载**：同时保持最多 5 个待处理的 block 请求，提高下载效率
- **并发下载**：支持多个 peer 同时下载不同的 pieces
- **连接复用**：每个 worker 只建立一次连接，用于下载多个 pieces，大幅减少网络开销
- **避免重复解析**：Torrent 文件只解析一次，所有信息从已解析的字典中获取，避免重复 I/O 操作
- **哈希验证**：自动验证每个 piece 的 SHA-1 哈希值，确保数据完整性
- **错误处理**：完善的错误处理和重试机制，下载失败自动放回队列重试
- **元数据缓存**：磁力链接下载时，元数据只获取一次，传递给所有 workers

### 文件结构
```
app/
├── main.go          # 主程序入口，命令解析
├── torrent.go       # Torrent 文件相关功能（解析、下载等）
├── magnet.go        # 磁力链接相关功能（解析、元数据获取、下载等）
├── download.go      # 下载相关的数据结构（WorkQueue、PieceBuffer 等）
├── utils.go         # 工具函数（下载、握手、消息处理、连接复用等）
├── decode.go        # Bencode 解码和磁力链接解析
└── encode.go        # Bencode 编码
```

### 性能优化
- **连接复用**：`downloadPieceWithPeer` 和 `downloadPieceWithPeerByMagnet` 都实现了连接复用
  - 每个 worker 只建立一次 TCP 连接
  - 使用同一连接下载多个 pieces
  - 减少握手和连接建立的开销
- **避免重复解析**：Torrent 文件解析优化
  - `downloadFileConcurrent` 只解析一次 torrent 文件
  - 所有信息（pieces 数量、peer 列表、info hash、piece 信息）都从已解析的字典中获取
  - 避免在多个函数中重复读取和解析文件
  - 将 `infoDict` 传递给所有 workers，避免重复解析
- **元数据缓存**：磁力链接下载时，元数据只获取一次并传递给所有 workers
- **管道化请求**：每个 piece 下载时，同时保持最多 5 个待处理的 block 请求
- **并发 workers**：根据可用 peer 数量启动多个并发 workers

## 注意事项

1. **网络连接**：确保能够访问 tracker 和 peer 地址
2. **文件权限**：确保有写入输出目录的权限
3. **Piece 索引**：piece 索引从 0 开始
4. **磁力链接格式**：磁力链接必须包含 `xt`（info hash）和 `tr`（tracker URL）参数
5. **并发下载**：`download` 和 `magnet_download` 命令使用并发下载，会根据可用 peer 数量自动调整 worker 数量
6. **连接管理**：所有连接都会在函数结束时自动关闭，使用 `defer` 确保资源释放
7. **错误重试**：下载失败的 piece 会自动放回队列重试，最多重试 3 次

## 使用示例

### 完整下载流程示例

**使用 Torrent 文件下载：**
```bash
# 1. 查看 torrent 文件信息
./your_program.sh info sample.torrent

# 2. 查看可用的 peers
./your_program.sh peers sample.torrent

# 3. 下载完整文件
./your_program.sh download /tmp/output.bin sample.torrent
```

**使用磁力链接下载：**
```bash
# 1. 解析磁力链接
./your_program.sh magnet_parse "magnet:?xt=urn:btih:..."

# 2. 获取元数据信息
./your_program.sh magnet_info "magnet:?xt=urn:btih:..."

# 3. 下载完整文件
./your_program.sh magnet_download -o /tmp/sample "magnet:?xt=urn:btih:..."
```

## 实现细节

### 连接复用机制

为了提高下载效率，实现了连接复用机制：

1. **Torrent 文件下载**：
   - `downloadFileConcurrent` 只解析一次 torrent 文件，获取 `torrentDict` 和 `infoDict`
   - `downloadPieceWithPeer` 建立连接后，使用 `downloadPieceReuseConn` 复用连接
   - 每个 worker 只建立一次连接，用于下载多个 pieces
   - `downloadPieceReuseConn` 接受 `infoDict` 参数，避免重复解析 torrent 文件

2. **磁力链接下载**：
   - `downloadFileConcurrentWithMagnet` 只获取一次元数据，传递给所有 workers
   - `downloadPieceWithPeerByMagnet` 使用 `performMagnetHandshakeWithPeer` 连接到指定 peer
   - 使用 `downloadPieceWithMagnetReuseConn` 复用连接下载piece
   - 元数据只获取一次，传递给所有 workers

### Torrent 文件解析优化

为了避免重复解析 torrent 文件，实现了以下优化：

1. **统一解析入口**：
   - `downloadFileConcurrent` 函数开始时只解析一次 torrent 文件
   - 使用 `getTorrentFileDict` 获取 `torrentDict`

2. **从字典获取信息**：
   - `getTotalPiecesFromDict(torrentDict)` - 从已解析的字典获取 pieces 数量和文件长度
   - `getPeerAddressFromDict(torrentDict)` - 从已解析的字典获取 peer 列表
   - `getInfoHashBytesFromDict(torrentDict)` - 从已解析的字典获取 info hash
   - `getPieceInfoFromDict(infoDict, pieceIndex)` - 从已解析的 info 字典获取 piece 信息

3. **传递给 Workers**：
   - 将 `infoDict` 传递给 `downloadPieceWithPeer` 函数
   - Workers 使用 `downloadPieceReuseConn(conn, infoDict, pieceIndex)` 下载 pieces
   - 完全避免在 worker 中重复解析 torrent 文件

### 消息协议

- **BitTorrent 握手**：68 字节，包含协议字符串、保留字节、info hash 和 peer ID
- **扩展握手**：支持 ut_metadata 扩展，用于获取元数据
- **Peer 消息**：4 字节长度前缀 + 1 字节消息 ID + payload
- **Piece 消息**：消息 ID 7，包含 piece index、begin offset 和 block 数据

## 许可证

本项目为 CodeCrafters BitTorrent 挑战的实现。
